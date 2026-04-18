package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/pkg/types"
)

const maxTeammateIterations = 50

// TeammateConfig holds everything needed to construct a Teammate.
type TeammateConfig struct {
	Name         string
	Role         string
	Model        core.ChatModel
	Deps         *core.AgentDependencies
	Tools        []*types.ToolMeta
	SystemPrompt string
	Bus          *MessageBus
	Roster       *Roster
	Tracker      *RequestTracker
	TaskManager  *planning.TaskManager
	ClaimLogger  *ClaimLogger
	Observer     core.Observer
	Runtime      *Runtime
	// PollInterval controls idle-phase polling frequency.
	PollInterval time.Duration
	// MaxIdlePolls controls how many idle polls before auto-shutdown.
	MaxIdlePolls int
}

// Teammate is a long-lived agent with its own goroutine, inbox, and persistent conversation.
type Teammate struct {
	name         string
	role         string
	model        core.ChatModel
	deps         *core.AgentDependencies
	tools        []*types.ToolMeta
	systemPrompt string
	bus          *MessageBus
	roster       *Roster
	tracker      *RequestTracker
	taskManager  *planning.TaskManager
	claimLogger  *ClaimLogger
	observer     core.Observer
	runtime      *Runtime

	pollInterval time.Duration
	maxIdlePolls int

	messages []types.Message
	cancel   context.CancelFunc
	done     chan struct{}

	mu             sync.RWMutex
	shutdownSignal chan string // receives request_id when shutdown requested
}

// NewTeammate constructs a teammate but does not start it.
func NewTeammate(cfg TeammateConfig) *Teammate {
	pi := cfg.PollInterval
	if pi <= 0 {
		pi = defaultPollInterval
	}
	mp := cfg.MaxIdlePolls
	if mp <= 0 {
		mp = defaultMaxIdlePolls
	}
	return &Teammate{
		name:           cfg.Name,
		role:           cfg.Role,
		model:          cfg.Model,
		deps:           cfg.Deps,
		tools:          cfg.Tools,
		systemPrompt:   cfg.SystemPrompt,
		bus:            cfg.Bus,
		roster:         cfg.Roster,
		tracker:        cfg.Tracker,
		taskManager:    cfg.TaskManager,
		claimLogger:    cfg.ClaimLogger,
		observer:       cfg.Observer,
		runtime:        cfg.Runtime,
		pollInterval:   pi,
		maxIdlePolls:   mp,
		done:           make(chan struct{}),
		shutdownSignal: make(chan string, 1),
	}
}

// Start launches the teammate goroutine with the initial prompt.
func (t *Teammate) Start(ctx context.Context, initialPrompt string) {
	loopCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	go t.run(loopCtx, initialPrompt)
}

// Done returns a channel closed when the teammate exits.
func (t *Teammate) Done() <-chan struct{} { return t.done }

// Stop triggers immediate cancellation.
func (t *Teammate) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

// RequestShutdown signals the teammate to shut down gracefully.
func (t *Teammate) RequestShutdown(requestID string) {
	select {
	case t.shutdownSignal <- requestID:
	default:
	}
}

func (t *Teammate) setRosterState(status, activity string, taskID int) {
	if t.roster == nil {
		return
	}
	if status != "" {
		_ = t.roster.UpdateStatus(t.name, status)
	}
	_ = t.roster.UpdateActivity(t.name, activity, taskID)
}

func (t *Teammate) log(msg string, kv ...interface{}) {
	if t.observer != nil {
		t.observer.Info(fmt.Sprintf("[%s] %s", t.name, msg), kv...)
	}
}

func (t *Teammate) logWarn(msg string, kv ...interface{}) {
	if t.observer != nil {
		t.observer.Warn(fmt.Sprintf("[%s] %s", t.name, msg), kv...)
	}
}

// run is the main goroutine: WORK -> IDLE -> WORK -> ... -> shutdown.
func (t *Teammate) run(ctx context.Context, initialPrompt string) {
	defer close(t.done)
	defer func() {
		if t.taskManager != nil {
			released, err := t.taskManager.ReleaseClaimsByOwner(t.name)
			if err != nil {
				t.logWarn("failed to release claimed tasks during shutdown", "err", err)
			} else if len(released) > 0 {
				t.log("released claimed tasks during shutdown", "task_ids", released)
			}
		}
		t.setRosterState(StatusShutdown, "shutdown", 0)
		t.log("shutdown complete")
	}()

	t.setRosterState(StatusWorking, "initial_prompt", 0)

	t.messages = append(t.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   initialPrompt,
		CreatedAt: time.Now(),
	})
	workInput := initialPrompt

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		t.runWorkUnit(ctx, workInput)

		resume, nextInput := t.idlePhase(ctx)
		if !resume {
			return
		}
		workInput = nextInput
	}
}

func (t *Teammate) runWorkUnit(ctx context.Context, input string) {
	if strings.TrimSpace(input) == "" {
		input = "Continue the current teammate work."
	}
	if t.runtime != nil {
		output, err := t.runtime.RunWithReflection(
			ctx,
			t.name,
			fmt.Sprintf("%s teammate", t.role),
			t.systemPrompt,
			t.tools,
			input,
			t.executeWorkPhase,
		)
		if err != nil {
			t.logWarn("work unit failed", "err", err)
			return
		}
		t.runtime.RecordTurn(ctx, input, output)
		return
	}
	if _, err := t.executeWorkPhase(ctx, input); err != nil {
		t.logWarn("work unit failed", "err", err)
	}
}

// executeWorkPhase runs the model/tool loop until the model produces final text or limits are hit.
func (t *Teammate) executeWorkPhase(ctx context.Context, input string) (string, error) {
	taskID := 0
	if t.roster != nil {
		if member, ok := t.roster.Get(t.name); ok {
			taskID = member.ClaimedTaskID
		}
	}
	activity := "self_directed"
	if taskID > 0 {
		activity = "claimed_task"
	}
	t.setRosterState(StatusWorking, activity, taskID)
	_ = input

	for iter := 0; iter < maxTeammateIterations; iter++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Drain inbox before each model call so in-flight messages are seen promptly.
		t.drainInbox()
		t.compactTranscript(ctx)

		toolDefs := toolDefinitions(t.tools)
		llmTrace, llmCtx := t.startLLMSpan(ctx)
		var resp *core.ChatModelResponse
		err := t.callWithRecovery(llmCtx, func(callCtx context.Context) error {
			var genErr error
			resp, genErr = t.model.Generate(callCtx, t.buildSystem(), t.messages, toolDefs)
			return genErr
		})
		t.endLLMSpan(llmTrace, err)
		if err != nil {
			return "", fmt.Errorf("teammate: model generate: %w", err)
		}
		if resp == nil {
			return "", fmt.Errorf("teammate: nil model response")
		}

		if !hasToolCalls(resp) {
			text := strings.TrimSpace(resp.Content)
			t.appendAssistant(text)
			return text, nil
		}

		// Append assistant message with tool calls.
		t.appendAssistantWithCalls(resp)

		for _, call := range resp.ToolCalls {
			toolTrace, toolCtx := t.startToolSpan(ctx, call.Name)
			result := t.executeTool(toolCtx, call)
			t.endToolSpan(toolTrace, toolResultErr(result))
			t.appendToolResult(call, result)
		}
	}
	return "", fmt.Errorf("teammate: exceeded max iterations (%d)", maxTeammateIterations)
}

// idlePhase checks inbox then task board; returns true to resume work.
func (t *Teammate) idlePhase(ctx context.Context) (bool, string) {
	t.setRosterState(StatusIdle, "waiting_for_work", 0)

	for poll := 0; poll < t.maxIdlePolls; poll++ {
		select {
		case <-ctx.Done():
			return false, ""
		case reqID := <-t.shutdownSignal:
			t.handleShutdownRequest(reqID)
			return false, ""
		default:
		}

		// Priority 1: inbox messages.
		msgs, _ := t.bus.ReadInbox(t.name)
		if len(msgs) > 0 {
			var resumedInput []string
			for _, env := range msgs {
				if env.Type == MsgTypeShutdownRequest && env.RequestID != "" {
					t.handleShutdownRequest(env.RequestID)
					return false, ""
				}
				payload := formatEnvelope(env)
				t.messages = append(t.messages, types.Message{
					ID:        uuid.NewString(),
					Role:      types.RoleUser,
					Content:   payload,
					CreatedAt: time.Now(),
				})
				resumedInput = append(resumedInput, payload)
			}
			t.ensureIdentity()
			t.setRosterState(StatusWorking, "processing_inbox", 0)
			return true, strings.Join(resumedInput, "\n")
		}

		// Priority 2: claimable tasks.
		if t.taskManager != nil {
			claimable := ScanClaimable(t.taskManager, t.name, t.role)
			if len(claimable) > 0 {
				task := claimable[0]
				claimed, err := t.taskManager.Claim(task.ID, t.name, t.role, "auto")
				if err == nil {
					if t.claimLogger != nil {
						_ = t.claimLogger.Log(claimed.ID, t.name, t.role, "auto")
					}
					t.log("auto-claimed task", "task_id", claimed.ID, "title", claimed.Title)
					t.ensureIdentity()
					t.setRosterState(StatusWorking, "claimed_task", claimed.ID)
					taskPrompt := fmt.Sprintf("<auto-claimed>Task #%d: %s\n%s</auto-claimed>", claimed.ID, claimed.Title, claimed.Description)
					t.messages = append(t.messages, types.Message{
						ID:        uuid.NewString(),
						Role:      types.RoleUser,
						Content:   taskPrompt,
						CreatedAt: time.Now(),
					})
					t.appendAssistant(fmt.Sprintf("Claimed task #%d. Working on it.", claimed.ID))
					return true, taskPrompt
				}
				// Claim race lost -- continue polling.
			}
		}

		select {
		case <-ctx.Done():
			return false, ""
		case reqID := <-t.shutdownSignal:
			t.handleShutdownRequest(reqID)
			return false, ""
		case <-time.After(t.pollInterval):
		}
	}

	t.log("idle timeout, auto-shutting down")
	return false, ""
}

func (t *Teammate) handleShutdownRequest(requestID string) {
	t.log("shutdown requested", "request_id", requestID)
	if t.tracker != nil && requestID != "" {
		_ = RespondShutdown(t.bus, t.tracker, requestID, true, t.name)
	}
}

func (t *Teammate) drainInbox() {
	msgs, _ := t.bus.ReadInbox(t.name)
	for _, env := range msgs {
		if env.Type == MsgTypeShutdownRequest && env.RequestID != "" {
			select {
			case t.shutdownSignal <- env.RequestID:
			default:
			}
			continue
		}
		t.messages = append(t.messages, types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleUser,
			Content:   formatEnvelope(env),
			CreatedAt: time.Now(),
		})
	}
}

func (t *Teammate) ensureIdentity() {
	teamName := "default"
	if t.roster != nil {
		teamName = t.roster.TeamName()
	}
	t.messages = append(t.messages,
		types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleUser,
			Content:   identityBlock(t.name, t.role, teamName),
			CreatedAt: time.Now(),
		},
		types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleAssistant,
			Content:   identityAck(t.name),
			CreatedAt: time.Now(),
		},
	)
}

func (t *Teammate) buildSystem() string {
	base := t.systemPrompt
	teamCtx := fmt.Sprintf(
		"\n\nYou are '%s', role: %s. Use send_message to communicate with teammates. "+
			"Use read_inbox to check for messages. Use assign_task to route task-board items before handoff. Use claim_task to pick up work.",
		t.name, t.role,
	)
	if t.runtime != nil {
		return t.runtime.BuildSystemPrompt(base, nil, strings.TrimSpace(teamCtx))
	}
	return base + teamCtx
}

func (t *Teammate) appendAssistant(content string) {
	t.messages = append(t.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleAssistant,
		Content:   content,
		CreatedAt: time.Now(),
	})
}

func (t *Teammate) appendAssistantWithCalls(resp *core.ChatModelResponse) {
	t.messages = append(t.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleAssistant,
		Content:   coalesce(resp.Content, "[tool_calls]"),
		ToolCalls: resp.ToolCalls,
		CreatedAt: time.Now(),
	})
}

func (t *Teammate) appendToolResult(call types.ToolCall, result *types.ToolResult) {
	t.messages = append(t.messages, types.Message{
		ID:      uuid.NewString(),
		Role:    types.RoleTool,
		ToolID:  call.ID,
		Content: result.Content,
		Metadata: map[string]interface{}{
			"tool_name": call.Name,
			"is_error":  result.IsError,
		},
		CreatedAt: time.Now(),
	})
}

func (t *Teammate) executeTool(ctx context.Context, call types.ToolCall) *types.ToolResult {
	if call.ID == "" {
		call.ID = uuid.NewString()
	}

	meta := t.findTool(call.Name)
	if meta == nil {
		return &types.ToolResult{
			ToolID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Unknown tool: %s", call.Name),
			IsError: true,
		}
	}

	if t.deps != nil && t.deps.PermPipeline != nil {
		if err := t.deps.PermPipeline.CheckTool(ctx, call.Name, call.Arguments); err != nil {
			return &types.ToolResult{
				ToolID:  call.ID,
				Name:    call.Name,
				Content: fmt.Sprintf("Permission denied: %v", err),
				IsError: true,
			}
		}
	}

	if meta.Handler == nil {
		return &types.ToolResult{
			ToolID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Tool %q has no handler", call.Name),
			IsError: true,
		}
	}

	res, err := meta.Handler(ctx, call.Arguments)
	if err != nil {
		return &types.ToolResult{
			ToolID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Error: %v", err),
			IsError: true,
		}
	}
	if res == nil {
		return &types.ToolResult{ToolID: call.ID, Name: call.Name, Content: "ok"}
	}
	if res.ToolID == "" {
		res.ToolID = call.ID
	}
	if res.Name == "" {
		res.Name = call.Name
	}
	return res
}

func (t *Teammate) findTool(name string) *types.ToolMeta {
	for _, tool := range t.tools {
		if tool != nil && tool.Definition.Name == name {
			return tool
		}
	}
	if t.deps != nil && t.deps.ToolRegistry != nil {
		if m := t.deps.ToolRegistry.Get(name); m != nil {
			return m
		}
	}
	return nil
}

// --- helpers ---

func formatEnvelope(env MessageEnvelope) string {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Sprintf("[%s from %s]: %s", env.Type, env.From, env.Content)
	}
	return string(data)
}

func hasToolCalls(resp *core.ChatModelResponse) bool {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return false
	}
	r := strings.ToLower(strings.TrimSpace(resp.FinishReason))
	switch r {
	case "tool_calls", "tool_use", "function_call", "required_action":
		return true
	case "stop", "end_turn", "completed", "length", "content_filter", "":
		return len(resp.ToolCalls) > 0
	default:
		return len(resp.ToolCalls) > 0
	}
}

func toolDefinitions(metas []*types.ToolMeta) []types.ToolDefinition {
	var defs []types.ToolDefinition
	for _, m := range metas {
		if m != nil {
			defs = append(defs, m.Definition)
		}
	}
	return defs
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func toolResultErr(result *types.ToolResult) error {
	if result == nil || !result.IsError {
		return nil
	}
	return fmt.Errorf(strings.TrimSpace(result.Content))
}

func (t *Teammate) startLLMSpan(ctx context.Context) (*runtimeTrace, context.Context) {
	if t.runtime == nil {
		return nil, ctx
	}
	return t.runtime.StartLLMSpan(ctx, t.name)
}

func (t *Teammate) endLLMSpan(trace *runtimeTrace, err error) {
	if t.runtime == nil {
		return
	}
	t.runtime.EndLLMSpan(trace, err)
}

func (t *Teammate) startToolSpan(ctx context.Context, toolName string) (*runtimeTrace, context.Context) {
	if t.runtime == nil {
		return nil, ctx
	}
	return t.runtime.StartToolSpan(ctx, t.name, toolName)
}

func (t *Teammate) endToolSpan(trace *runtimeTrace, err error) {
	if t.runtime == nil {
		return
	}
	t.runtime.EndToolSpan(trace, err)
}

func (t *Teammate) callWithRecovery(ctx context.Context, fn func(context.Context) error) error {
	if t.runtime == nil {
		return fn(ctx)
	}
	return t.runtime.CallWithRecovery(ctx, func() error { return fn(ctx) })
}

func (t *Teammate) compactTranscript(ctx context.Context) {
	if t.runtime == nil {
		return
	}
	t.messages = t.runtime.SummarizeTranscriptMessages(ctx, t.messages)
}
