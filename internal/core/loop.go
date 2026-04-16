package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// ChatModel abstracts an LLM with function-calling. This mirrors the shape of Eino chat models
// without importing github.com/cloudwego/eino so the core package compiles in isolation and
// tests can stub a trivial implementation.
type ChatModel interface {
	Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*ChatModelResponse, error)
}

// ChatModelResponse is a normalized generation result for the ReAct loop.
type ChatModelResponse struct {
	Content      string
	ToolCalls    []types.ToolCall
	FinishReason string
}

// Normalized finish reasons used when the provider does not populate FinishReason consistently.
const (
	FinishReasonEndTurn   = "end_turn"
	FinishReasonToolUse   = "tool_use"
	FinishReasonStop      = "stop"
	FinishReasonToolCalls = "tool_calls"
)

// LoopHooks are extension points for tracing, policy injection, and metrics.
type LoopHooks struct {
	PreAPI   func(ctx context.Context, state *LoopState, iteration int) error
	PostAPI  func(ctx context.Context, state *LoopState, resp *ChatModelResponse, iteration int) error
	PreTool  func(ctx context.Context, state *LoopState, call *types.ToolCall) error
	PostTool func(ctx context.Context, state *LoopState, result *types.ToolResult, execErr error) error
}

// AgentLoop executes the ReAct cycle: user -> LLM -> (tools?)+... -> final text.
type AgentLoop struct {
	deps     *AgentDependencies
	model    ChatModel
	guard    *ContextGuard
	recovery *RecoveryManager
	hooks    *LoopHooks

	getTools      func() []*types.ToolMeta
	getSystem     func() string
	workspaceRoot string
	convWindow    int
}

// NewAgentLoop wires the loop. getTools/getSystem are evaluated each iteration so AddTool mutations apply.
// workspaceRoot feeds ContextGuard path resolution; if empty, "." is used.
func NewAgentLoop(
	deps *AgentDependencies,
	model ChatModel,
	guard *ContextGuard,
	recovery *RecoveryManager,
	getTools func() []*types.ToolMeta,
	getSystem func() string,
	workspaceRoot string,
	convWindow int,
	hooks *LoopHooks,
) *AgentLoop {
	if getTools == nil {
		getTools = func() []*types.ToolMeta { return nil }
	}
	if getSystem == nil {
		getSystem = func() string { return "" }
	}
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	return &AgentLoop{
		deps:          deps,
		model:         model,
		guard:         guard,
		recovery:      recovery,
		hooks:         hooks,
		getTools:      getTools,
		getSystem:     getSystem,
		workspaceRoot: workspaceRoot,
		convWindow:    convWindow,
	}
}

// SetHooks replaces hook callbacks (nil-safe).
func (l *AgentLoop) SetHooks(h *LoopHooks) {
	l.hooks = h
}

// classifyFinishReason maps provider-specific reasons to tool_use vs end_turn.
//
// Providers disagree on semantics: some emit finish_reason=stop alongside parallel tool_calls;
// others use tool_calls or function_call. We treat non-empty ToolCalls as authoritative for
// continuing the ReAct loop unless we later add explicit "refusal" handling.
func classifyFinishReason(resp *ChatModelResponse) string {
	if resp == nil {
		return FinishReasonEndTurn
	}
	r := strings.ToLower(strings.TrimSpace(resp.FinishReason))
	switch r {
	case "tool_calls", "tool_use", "function_call", "required_action":
		return FinishReasonToolUse
	case "stop", "end_turn", "completed", "length", "content_filter":
		if len(resp.ToolCalls) > 0 {
			return FinishReasonToolUse
		}
		return FinishReasonEndTurn
	case "":
		if len(resp.ToolCalls) > 0 {
			return FinishReasonToolUse
		}
		return FinishReasonEndTurn
	default:
		// Unknown string: prefer explicit tool calls over trusting the label alone.
		if len(resp.ToolCalls) > 0 {
			return FinishReasonToolUse
		}
		return FinishReasonEndTurn
	}
}

// shouldContinueWithTools returns true when the loop must execute tool calls before the next LLM turn.
func shouldContinueWithTools(resp *ChatModelResponse) bool {
	if resp == nil {
		return false
	}
	if len(resp.ToolCalls) == 0 {
		return false
	}
	reason := classifyFinishReason(resp)
	return reason == FinishReasonToolUse || reason == FinishReasonToolCalls
}

// RunLoop executes the agent conversation until the model ends the turn with text or limits hit.
func (l *AgentLoop) RunLoop(ctx context.Context, input string) (string, error) {
	if l == nil {
		return "", fmt.Errorf("core/loop: nil AgentLoop")
	}
	if l.model == nil {
		return "", fmt.Errorf("core/loop: ChatModel is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	state := NewLoopState()
	if err := state.TransitionTo(PhaseRunning, "run_start"); err != nil {
		return "", err
	}

	userMsg := types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   input,
		CreatedAt: state.StartedAt,
	}
	state.AppendMessage(userMsg)
	state.BumpTurn()

	maxIter := 20
	if l.deps != nil && l.deps.AgentConfig.MaxIterations > 0 {
		maxIter = l.deps.AgentConfig.MaxIterations
	}

	var lastAssistant string
	for iter := 0; iter < maxIter; iter++ {
		state.BumpIteration()

		if l.guard != nil {
			if _, err := l.guard.MaybeCompact(ctx, state); err != nil && l.deps != nil && l.deps.Observer != nil {
				l.deps.Observer.Warn("core/loop: compaction failed", "err", err.Error())
			}
		}

		if l.hooks != nil && l.hooks.PreAPI != nil {
			if err := l.hooks.PreAPI(ctx, state, iter); err != nil {
				_ = state.TransitionTo(PhaseError, "pre_api_hook")
				return "", fmt.Errorf("core/loop: pre_api hook: %w", err)
			}
		}

		tools := toolDefinitions(l.getTools())
		sys := l.getSystem()

		var resp *ChatModelResponse
		generateOnce := func() error {
			var genErr error
			resp, genErr = l.model.Generate(ctx, sys, state.MessagesSnapshot(), tools)
			if genErr != nil {
				if IsContextOverflowError(genErr) && l.guard != nil {
					if cErr := l.guard.ForceManualCompact(ctx, state); cErr != nil && l.deps != nil && l.deps.Observer != nil {
						l.deps.Observer.Warn("core/loop: force compact failed", "err", cErr.Error())
					}
				}
				return genErr
			}
			return nil
		}

		var err error
		if l.recovery != nil {
			err = l.recovery.CallWithRetry(ctx, state, generateOnce)
		} else {
			err = generateOnce()
		}
		if err != nil {
			_ = state.TransitionTo(PhaseError, "model_generate")
			return "", fmt.Errorf("core/loop: model generate: %w", err)
		}
		if resp == nil {
			_ = state.TransitionTo(PhaseError, "nil_response")
			return "", fmt.Errorf("core/loop: nil model response")
		}

		state.RecordResponseTokens(utils.EstimateTokens(resp.Content))
		for _, tc := range resp.ToolCalls {
			state.RecordResponseTokens(utils.EstimateTokens(tc.Name))
			state.RecordResponseTokens(utils.EstimateTokens(fmt.Sprintf("%v", tc.Arguments)))
		}

		if l.hooks != nil && l.hooks.PostAPI != nil {
			if err := l.hooks.PostAPI(ctx, state, resp, iter); err != nil {
				_ = state.TransitionTo(PhaseError, "post_api_hook")
				return "", fmt.Errorf("core/loop: post_api hook: %w", err)
			}
		}

		if !shouldContinueWithTools(resp) {
			lastAssistant = strings.TrimSpace(resp.Content)
			assistantMsg := types.Message{
				ID:        uuid.NewString(),
				Role:      types.RoleAssistant,
				Content:   resp.Content,
				ToolCalls: nil,
				CreatedAt: time.Now(),
				Metadata: map[string]interface{}{
					"finish_reason": classifyFinishReason(resp),
				},
			}
			state.AppendMessage(assistantMsg)
			_ = state.TransitionTo(PhaseCompleted, "model_finished")
			return lastAssistant, nil
		}

		if err := state.TransitionTo(PhaseToolExecution, "tool_calls"); err != nil {
			return "", err
		}

		assistantCallMsg := types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleAssistant,
			Content:   utils.Coalesce(resp.Content, "[tool_calls]"),
			ToolCalls: resp.ToolCalls,
			CreatedAt: time.Now(),
			Metadata: map[string]interface{}{
				"finish_reason": classifyFinishReason(resp),
			},
		}
		state.AppendMessage(assistantCallMsg)

		for _, call := range resp.ToolCalls {
			call := call
			if call.ID == "" {
				call.ID = uuid.NewString()
			}
			if l.hooks != nil && l.hooks.PreTool != nil {
				if err := l.hooks.PreTool(ctx, state, &call); err != nil {
					_ = state.TransitionTo(PhaseError, "pre_tool_hook")
					return "", fmt.Errorf("core/loop: pre_tool hook: %w", err)
				}
			}

			result, execErr := l.executeToolCall(ctx, state, call)

			if l.hooks != nil && l.hooks.PostTool != nil {
				if err := l.hooks.PostTool(ctx, state, result, execErr); err != nil {
					_ = state.TransitionTo(PhaseError, "post_tool_hook")
					return "", fmt.Errorf("core/loop: post_tool hook: %w", err)
				}
			}

			if result == nil {
				result = WrapToolError(call.Name, call.ID, fmt.Errorf("tool returned nil result"))
			}

			toolMsg := types.Message{
				ID:        uuid.NewString(),
				Role:      types.RoleTool,
				Content:   result.Content,
				ToolID:    call.ID,
				Metadata: map[string]interface{}{
					"tool_name": call.Name,
					"is_error":  result.IsError,
				},
				CreatedAt: time.Now(),
			}
			state.AppendMessage(toolMsg)
		}

		if err := state.TransitionTo(PhaseRunning, "tools_done"); err != nil {
			return "", err
		}
	}

	_ = state.TransitionTo(PhaseError, "max_iterations")
	return "", fmt.Errorf("core/loop: exceeded max_iterations=%d", maxIter)
}

func (l *AgentLoop) executeToolCall(ctx context.Context, state *LoopState, call types.ToolCall) (*types.ToolResult, error) {
	meta, ok := resolveTool(l.deps, call.Name, l.getTools())
	if !ok || meta == nil {
		err := fmt.Errorf("unknown tool %q", call.Name)
		return WrapToolError(call.Name, call.ID, err), err
	}

	if l.deps != nil && l.deps.PermPipeline != nil {
		if err := l.deps.PermPipeline.CheckTool(ctx, call.Name, call.Arguments); err != nil {
			res := WrapToolError(call.Name, call.ID, err)
			return res, err
		}
	}

	if meta.Handler == nil {
		err := fmt.Errorf("tool %q has no handler", call.Name)
		return WrapToolError(call.Name, call.ID, err), err
	}

	res, err := meta.Handler(ctx, call.Arguments)
	if err != nil {
		wrapped := WrapToolError(call.Name, call.ID, err)
		// Layer 1: error text is returned as tool_result content; execErr is still surfaced to PostTool hooks.
		return wrapped, err
	}
	if res == nil {
		return &types.ToolResult{ToolID: call.ID, Name: call.Name, Content: "ok", IsError: false}, nil
	}
	if res.ToolID == "" {
		res.ToolID = call.ID
	}
	if res.Name == "" {
		res.Name = call.Name
	}

	// Micro guard: optionally persist huge successful payloads before they hit the transcript.
	if l.guard != nil && res.Content != "" && !res.IsError {
		tok := utils.EstimateTokens(res.Content)
		limit := 51200
		if l.deps != nil && l.deps.AgentConfig.MicroCompactSize > 0 {
			limit = l.deps.AgentConfig.MicroCompactSize
		}
		if tok >= limit {
			marker, perr := l.guard.PersistLargeOutput(res.Content, utils.SanitizeToolName(call.Name))
			if perr == nil {
				res.Content = marker
			} else if l.deps != nil && l.deps.Observer != nil {
				l.deps.Observer.Warn("core/loop: persist large output failed", "tool", call.Name, "err", perr.Error())
			}
		}
	}

	return res, nil
}

func toolDefinitions(metas []*types.ToolMeta) []types.ToolDefinition {
	var defs []types.ToolDefinition
	for _, m := range metas {
		if m == nil {
			continue
		}
		defs = append(defs, m.Definition)
	}
	return defs
}

func resolveTool(deps *AgentDependencies, name string, local []*types.ToolMeta) (*types.ToolMeta, bool) {
	for _, t := range local {
		if t == nil {
			continue
		}
		if t.Definition.Name == name {
			return t, true
		}
	}
	if deps == nil || deps.ToolRegistry == nil {
		return nil, false
	}
	if m := deps.ToolRegistry.Get(name); m != nil {
		return m, true
	}
	return nil, false
}
