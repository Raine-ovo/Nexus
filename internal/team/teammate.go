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
		_ = t.roster.UpdateStatus(t.name, StatusShutdown)
		t.log("shutdown complete")
	}()

	_ = t.roster.UpdateStatus(t.name, StatusWorking)

	t.messages = append(t.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   initialPrompt,
		CreatedAt: time.Now(),
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		t.workPhase(ctx)

		resume := t.idlePhase(ctx)
		if !resume {
			return
		}
	}
}

// workPhase runs the model/tool loop until the model produces final text or limits are hit.
func (t *Teammate) workPhase(ctx context.Context) {
	_ = t.roster.UpdateStatus(t.name, StatusWorking)

	for iter := 0; iter < maxTeammateIterations; iter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Drain inbox before each model call so in-flight messages are seen promptly.
		t.drainInbox()

		toolDefs := toolDefinitions(t.tools)
		resp, err := t.model.Generate(ctx, t.buildSystem(), t.messages, toolDefs)
		if err != nil {
			t.logWarn("model generate error", "err", err)
			return
		}
		if resp == nil {
			return
		}

		if !hasToolCalls(resp) {
			t.appendAssistant(resp.Content)
			return
		}

		// Append assistant message with tool calls.
		t.appendAssistantWithCalls(resp)

		for _, call := range resp.ToolCalls {
			result := t.executeTool(ctx, call)
			t.appendToolResult(call, result)
		}
	}
	t.logWarn("work phase hit max iterations")
}

// idlePhase checks inbox then task board; returns true to resume work.
func (t *Teammate) idlePhase(ctx context.Context) bool {
	_ = t.roster.UpdateStatus(t.name, StatusIdle)

	for poll := 0; poll < t.maxIdlePolls; poll++ {
		select {
		case <-ctx.Done():
			return false
		case reqID := <-t.shutdownSignal:
			t.handleShutdownRequest(reqID)
			return false
		default:
		}

		// Priority 1: inbox messages.
		msgs, _ := t.bus.ReadInbox(t.name)
		if len(msgs) > 0 {
			for _, env := range msgs {
				if env.Type == MsgTypeShutdownRequest && env.RequestID != "" {
					t.handleShutdownRequest(env.RequestID)
					return false
				}
				t.messages = append(t.messages, types.Message{
					ID:        uuid.NewString(),
					Role:      types.RoleUser,
					Content:   formatEnvelope(env),
					CreatedAt: time.Now(),
				})
			}
			t.ensureIdentity()
			return true
		}

		// Priority 2: claimable tasks.
		if t.taskManager != nil {
			claimable := ScanClaimable(t.taskManager, t.role)
			if len(claimable) > 0 {
				task := claimable[0]
				claimed, err := t.taskManager.Claim(task.ID, t.name, "auto")
				if err == nil {
					if t.claimLogger != nil {
						_ = t.claimLogger.Log(claimed.ID, t.name, t.role, "auto")
					}
					t.log("auto-claimed task", "task_id", claimed.ID, "title", claimed.Title)
					t.ensureIdentity()
					t.messages = append(t.messages, types.Message{
						ID:        uuid.NewString(),
						Role:      types.RoleUser,
						Content:   fmt.Sprintf("<auto-claimed>Task #%d: %s\n%s</auto-claimed>", claimed.ID, claimed.Title, claimed.Description),
						CreatedAt: time.Now(),
					})
					t.appendAssistant(fmt.Sprintf("Claimed task #%d. Working on it.", claimed.ID))
					return true
				}
				// Claim race lost -- continue polling.
			}
		}

		select {
		case <-ctx.Done():
			return false
		case reqID := <-t.shutdownSignal:
			t.handleShutdownRequest(reqID)
			return false
		case <-time.After(t.pollInterval):
		}
	}

	t.log("idle timeout, auto-shutting down")
	return false
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
			"Use read_inbox to check for messages. Use claim_task to pick up work.",
		t.name, t.role,
	)
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
		ID:     uuid.NewString(),
		Role:   types.RoleTool,
		ToolID: call.ID,
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
