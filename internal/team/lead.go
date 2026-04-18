package team

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/pkg/types"
)

const leadName = "lead"

// Lead is the user-facing teammate that routes work and manages the team.
// Unlike regular teammates, it receives user input via a synchronous channel
// so HandleRequest can block until a response is ready.
type Lead struct {
	*Teammate
	mgr *Manager

	// requestCh carries user inputs; replyCh returns the response.
	requestCh chan leadRequest
}

type leadRequest struct {
	ctx       context.Context
	sessionID string
	input     string
	replyCh   chan leadReply
}

type leadReply struct {
	output string
	err    error
}

// newLead constructs the lead teammate. Not started until Start is called.
func newLead(mgr *Manager, model core.ChatModel, deps *core.AgentDependencies,
	baseTools []*types.ToolMeta, systemPrompt string) *Lead {

	cfg := TeammateConfig{
		Name:         leadName,
		Role:         "lead",
		Model:        model,
		Deps:         deps,
		Tools:        baseTools,
		SystemPrompt: systemPrompt,
		Bus:          mgr.bus,
		Roster:       mgr.roster,
		Tracker:      mgr.tracker,
		TaskManager:  mgr.taskManager,
		ClaimLogger:  mgr.claimLogger,
		Observer:     mgr.observer,
		Runtime:      mgr.runtime,
		PollInterval: 2 * time.Second,
		MaxIdlePolls: 600, // Lead stays alive much longer (20 min idle)
	}

	l := &Lead{
		Teammate:  NewTeammate(cfg),
		mgr:       mgr,
		requestCh: make(chan leadRequest, 8),
	}
	return l
}

// HandleRequest delivers user input to the lead and waits for its response.
// This implements the synchronous request-response pattern the gateway expects.
func (l *Lead) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	replyCh := make(chan leadReply, 1)
	req := leadRequest{
		ctx:       ctx,
		sessionID: sessionID,
		input:     input,
		replyCh:   replyCh,
	}
	select {
	case l.requestCh <- req:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case reply := <-replyCh:
		return reply.output, reply.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Start launches the lead's goroutine.
func (l *Lead) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	go l.leadLoop(loopCtx)
}

// leadLoop is the lead's main loop. It processes user requests synchronously,
// then checks inbox for teammate responses between requests.
func (l *Lead) leadLoop(ctx context.Context) {
	defer close(l.done)
	defer func() {
		l.setRosterState(StatusShutdown, "shutdown", 0)
	}()

	l.setRosterState(StatusWorking, "waiting_for_request", 0)

	// Initial system context.
	l.messages = append(l.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   "You are the team lead. You will receive user requests and coordinate with teammates to complete them.",
		CreatedAt: time.Now(),
	})
	l.appendAssistant("I am the team lead. Ready to receive requests.")

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-l.requestCh:
			output, err := l.handleOneRequest(req.ctx, req.input)
			req.replyCh <- leadReply{output: output, err: err}
		case <-time.After(l.pollInterval):
			l.drainInboxSilent()
		}
	}
}

// handleOneRequest runs a full work phase for one user input and returns the final text.
func (l *Lead) handleOneRequest(ctx context.Context, input string) (output string, err error) {
	reqTrace, reqCtx := l.startRequestSpan(ctx, "user_turn")
	defer func() { l.endRequestSpan(reqTrace, err) }()
	runCtx := reqCtx
	if runCtx == nil {
		runCtx = ctx
	}
	if l.runtime != nil {
		output, err = l.runtime.RunWithReflection(
			runCtx,
			l.name,
			"team lead",
			l.systemPrompt,
			l.tools,
			input,
			l.executeRequest,
		)
		if err == nil {
			l.runtime.RecordTurn(runCtx, input, output)
		}
		return output, err
	}
	return l.executeRequest(runCtx, input)
}

func (l *Lead) executeRequest(ctx context.Context, input string) (string, error) {
	l.setRosterState(StatusWorking, "handling_request", 0)
	defer func() { l.setRosterState(StatusIdle, "waiting_for_request", 0) }()

	// Drain any pending teammate messages first.
	l.drainInbox()

	l.messages = append(l.messages, types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleUser,
		Content:   input,
		CreatedAt: time.Now(),
	})

	var dispatch *dispatchProfile

	// Run the model/tool loop and collect the final assistant message.
	for iter := 0; iter < maxTeammateIterations; iter++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		l.drainInbox()
		l.compactTranscript(ctx)

		toolDefs := toolDefinitions(l.tools)
		llmTrace, llmCtx := l.startLLMSpan(ctx)
		var resp *core.ChatModelResponse
		err := l.callWithRecovery(llmCtx, func(callCtx context.Context) error {
			var genErr error
			resp, genErr = l.model.Generate(callCtx, l.buildSystem(), l.messages, toolDefs)
			return genErr
		})
		l.endLLMSpan(llmTrace, err)
		if err != nil {
			return "", fmt.Errorf("lead: model generate: %w", err)
		}
		if resp == nil {
			return "", fmt.Errorf("lead: nil model response")
		}

		if !hasToolCalls(resp) {
			text := strings.TrimSpace(resp.Content)
			l.appendAssistant(text)
			return text, nil
		}

		if hasLeadRoutingToolCalls(resp) {
			parsed, err := parseDispatchProfile(resp.Content)
			if err != nil {
				if l.observer != nil {
					l.observer.Warn("dispatch profile rejected", "error", err)
				}
				l.appendAssistantWithCalls(resp)
				for _, call := range resp.ToolCalls {
					result := &types.ToolResult{
						ToolID:  call.ID,
						Name:    call.Name,
						Content: fmt.Sprintf("Dispatch gate: %v", err),
						IsError: true,
					}
					l.appendToolResult(call, result)
				}
				continue
			}
			dispatch = parsed
			if l.observer != nil {
				l.observer.Info("dispatch profile accepted",
					"profile", dispatch.logValue(),
					"active_teammates", strings.Join(l.roster.ActiveNames(), ","),
					"reusable_teammate", findReusableTeammateForRole(l.roster, dispatch.SpecialistRole),
				)
			}
		}

		l.appendAssistantWithCalls(resp)
		for _, call := range resp.ToolCalls {
			if isLeadRoutingTool(call.Name) {
				if l.observer != nil {
					l.observer.Info("dispatch route proposed",
						"tool", call.Name,
						"mode", dispatchModeForCall(dispatch, l.roster, call),
						"profile", dispatch.logValue(),
					)
				}
				if err := validateLeadRoutingCall(dispatch, l.roster, call); err != nil {
					if l.observer != nil {
						l.observer.Warn("dispatch route rejected",
							"tool", call.Name,
							"mode", dispatchModeForCall(dispatch, l.roster, call),
							"profile", dispatch.logValue(),
							"error", err,
						)
					}
					result := &types.ToolResult{
						ToolID:  call.ID,
						Name:    call.Name,
						Content: fmt.Sprintf("Dispatch gate: %v", err),
						IsError: true,
					}
					l.appendToolResult(call, result)
					continue
				}
				if l.observer != nil {
					l.observer.Info("dispatch route approved",
						"tool", call.Name,
						"mode", dispatchModeForCall(dispatch, l.roster, call),
						"profile", dispatch.logValue(),
					)
				}
			}
			toolTrace, toolCtx := l.startToolSpan(ctx, call.Name)
			result := l.executeTool(toolCtx, call)
			l.endToolSpan(toolTrace, toolResultErr(result))
			l.appendToolResult(call, result)
		}
	}

	return "", fmt.Errorf("lead: exceeded max iterations")
}

func (l *Lead) drainInboxSilent() {
	msgs, _ := l.bus.ReadInbox(leadName)
	for _, env := range msgs {
		l.messages = append(l.messages, types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleUser,
			Content:   formatEnvelope(env),
			CreatedAt: time.Now(),
		})
	}
}

func (l *Lead) buildSystem() string {
	base := l.systemPrompt
	teamCtx := fmt.Sprintf(
		"\n\nYou are the team lead. Your team members: %s\n"+
			"Use spawn_teammate to create workers. Use send_message to assign work.\n"+
			"Use assign_task to reserve task-board items for a teammate or role before they claim them.\n"+
			"Use shutdown_teammate to stop workers. Use review_plan to approve risky plans.\n"+
			"For simple tasks, handle them yourself using available tools.\n"+
			"Before using ANY routing tool (delegate_task, spawn_teammate, send_message), include a dispatch block in the assistant text using this exact schema:\n"+
			"<dispatch_profile>\n"+
			"simple=true|false\n"+
			"needs_persistence=true|false\n"+
			"needs_isolation=true|false\n"+
			"expected_follow_up=true|false\n"+
			"specialist_role=<role-or-empty>\n"+
			"reason=<short_reason>\n"+
			"</dispatch_profile>\n"+
			"Rules: simple=true means do not use routing tools. needs_isolation=true means use delegate_task. needs_persistence=true or expected_follow_up=true means use persistent teammates: reuse existing teammates with send_message when possible, otherwise spawn_teammate.",
		strings.Join(l.roster.ActiveNames(), ", "),
	)
	if l.runtime != nil {
		return l.runtime.BuildSystemPrompt(base, nil, strings.TrimSpace(teamCtx))
	}
	return base + teamCtx
}

func (l *Lead) startRequestSpan(ctx context.Context, phase string) (*runtimeTrace, context.Context) {
	if l.runtime == nil {
		return nil, ctx
	}
	return l.runtime.StartRequestSpan(ctx, l.name, phase)
}

func (l *Lead) endRequestSpan(trace *runtimeTrace, err error) {
	if l.runtime == nil {
		return
	}
	l.runtime.endSpan(trace, err, "on_end")
}

func (l *Lead) startLLMSpan(ctx context.Context) (*runtimeTrace, context.Context) {
	if l.runtime == nil {
		return nil, ctx
	}
	return l.runtime.StartLLMSpan(ctx, l.name)
}

func (l *Lead) endLLMSpan(trace *runtimeTrace, err error) {
	if l.runtime == nil {
		return
	}
	l.runtime.EndLLMSpan(trace, err)
}

func (l *Lead) startToolSpan(ctx context.Context, toolName string) (*runtimeTrace, context.Context) {
	if l.runtime == nil {
		return nil, ctx
	}
	return l.runtime.StartToolSpan(ctx, l.name, toolName)
}

func (l *Lead) endToolSpan(trace *runtimeTrace, err error) {
	if l.runtime == nil {
		return
	}
	l.runtime.EndToolSpan(trace, err)
}

func (l *Lead) callWithRecovery(ctx context.Context, fn func(context.Context) error) error {
	if l.runtime == nil {
		return fn(ctx)
	}
	return l.runtime.CallWithRecovery(ctx, func() error { return fn(ctx) })
}

func (l *Lead) compactTranscript(ctx context.Context) {
	if l.runtime == nil {
		return
	}
	l.messages = l.runtime.SummarizeTranscriptMessages(ctx, l.messages)
}

func hasLeadRoutingToolCalls(resp *core.ChatModelResponse) bool {
	if resp == nil {
		return false
	}
	for _, call := range resp.ToolCalls {
		if isLeadRoutingTool(call.Name) {
			return true
		}
	}
	return false
}
