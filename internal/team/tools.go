package team

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// BuildTeammateTools returns the set of team-specific tools that every teammate gets.
func BuildTeammateTools(name, role string, bus *MessageBus, roster *Roster, tracker *RequestTracker, tm *planning.TaskManager, claimLogger *ClaimLogger, mgr *Manager) []*types.ToolMeta {
	return []*types.ToolMeta{
		toolSendMessage(name, bus, mgr),
		toolReadInbox(name, bus),
		toolListTeammates(roster),
		toolAssignTask(name, tm),
		toolClaimTask(name, role, roster, tm, claimLogger),
		toolSubmitPlan(name, bus, tracker),
	}
}

// BuildLeadTools returns additional tools that only the lead teammate gets.
func BuildLeadTools(mgr *Manager, bus *MessageBus, roster *Roster, tracker *RequestTracker) []*types.ToolMeta {
	return []*types.ToolMeta{
		toolSpawnTeammate(mgr),
		toolShutdownTeammate(bus, tracker),
		toolBroadcast(bus, roster),
		toolReviewPlan(bus, tracker),
		toolListPendingRequests(tracker),
		toolDelegateTask(mgr),
	}
}

func toolSendMessage(sender string, bus *MessageBus, mgr *Manager) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "send_message",
			Description: "Send a message to another teammate's inbox.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"to":      map[string]interface{}{"type": "string", "description": "Recipient name"},
					"content": map[string]interface{}{"type": "string", "description": "Message content"},
				},
				"required": []string{"to", "content"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			to := utils.GetString(args, "to")
			content := utils.GetString(args, "content")
			if to == "" || content == "" {
				return &types.ToolResult{Content: "Error: to and content required", IsError: true}, nil
			}
			if mgr != nil {
				if err := mgr.SendMessage(ctx, sender, to, content); err != nil {
					return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
				}
				return &types.ToolResult{Content: fmt.Sprintf("Sent message to %s", to)}, nil
			}
			if err := bus.Send(sender, to, content, MsgTypeMessage, nil); err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return &types.ToolResult{Content: fmt.Sprintf("Sent message to %s", to)}, nil
		},
	}
}

func toolReadInbox(name string, bus *MessageBus) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "read_inbox",
			Description: "Read and drain your inbox. Returns all pending messages.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		Permission: types.PermRead,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			msgs, err := bus.ReadInbox(name)
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			if len(msgs) == 0 {
				return &types.ToolResult{Content: "No messages."}, nil
			}
			data, _ := json.MarshalIndent(msgs, "", "  ")
			return &types.ToolResult{Content: string(data)}, nil
		},
	}
}

func toolListTeammates(roster *Roster) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "list_teammates",
			Description: "List all team members with name, role, status, current activity, and claimed task id.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		Permission: types.PermRead,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			members := roster.List()
			if len(members) == 0 {
				return &types.ToolResult{Content: "No teammates."}, nil
			}
			data, _ := json.MarshalIndent(members, "", "  ")
			return &types.ToolResult{Content: string(data)}, nil
		},
	}
}

func toolAssignTask(name string, tm *planning.TaskManager) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "assign_task",
			Description: "Reserve a task for a teammate or role before it is claimed.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id":  map[string]interface{}{"type": "integer", "description": "Task ID to assign"},
					"assignee": map[string]interface{}{"type": "string", "description": "Optional teammate name that should own the task"},
					"role":     map[string]interface{}{"type": "string", "description": "Optional role that is allowed to claim the task"},
					"reason":   map[string]interface{}{"type": "string", "description": "Short routing reason for audit/debugging"},
				},
				"required": []string{"task_id"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			taskID := utils.GetInt(args, "task_id")
			assignee := utils.GetString(args, "assignee")
			role := utils.GetString(args, "role")
			reason := utils.GetString(args, "reason")
			if taskID == 0 {
				return &types.ToolResult{Content: "Error: task_id required", IsError: true}, nil
			}
			if tm == nil {
				return &types.ToolResult{Content: "Error: task manager not available", IsError: true}, nil
			}
			assigned, err := tm.Assign(taskID, name, assignee, role, reason)
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			target := assigned.AssignedTo
			if target == "" {
				target = assigned.AssignedRole
			}
			return &types.ToolResult{Content: fmt.Sprintf("Assigned task #%d to %s", assigned.ID, target)}, nil
		},
	}
}

func toolClaimTask(name, role string, roster *Roster, tm *planning.TaskManager, cl *ClaimLogger) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "claim_task",
			Description: "Claim an unclaimed pending task by its ID.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{"type": "integer", "description": "Task ID to claim"},
				},
				"required": []string{"task_id"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			taskID := utils.GetInt(args, "task_id")
			if taskID == 0 {
				return &types.ToolResult{Content: "Error: task_id required", IsError: true}, nil
			}
			if tm == nil {
				return &types.ToolResult{Content: "Error: task manager not available", IsError: true}, nil
			}
			claimed, err := tm.Claim(taskID, name, role, "manual")
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			if cl != nil {
				_ = cl.Log(claimed.ID, name, role, "manual")
			}
			if roster != nil {
				_ = roster.UpdateStatus(name, StatusWorking)
				_ = roster.UpdateActivity(name, "claimed_task", claimed.ID)
			}
			return &types.ToolResult{Content: fmt.Sprintf("Claimed task #%d: %s", claimed.ID, claimed.Title)}, nil
		},
	}
}

func toolSubmitPlan(from string, bus *MessageBus, tracker *RequestTracker) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "submit_plan",
			Description: "Submit a plan for lead approval before executing risky operations.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"plan": map[string]interface{}{"type": "string", "description": "The plan text to submit for review"},
				},
				"required": []string{"plan"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			plan := utils.GetString(args, "plan")
			if plan == "" {
				return &types.ToolResult{Content: "Error: plan text required", IsError: true}, nil
			}
			rec, err := SubmitPlan(bus, tracker, from, plan)
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return &types.ToolResult{Content: fmt.Sprintf("Plan submitted (request_id: %s). Waiting for lead review.", rec.RequestID)}, nil
		},
	}
}

// --- Lead-only tools ---

func toolSpawnTeammate(mgr *Manager) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "spawn_teammate",
			Description: "Spawn a persistent teammate that runs in its own goroutine with the given role and initial prompt.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":   map[string]interface{}{"type": "string", "description": "Teammate name"},
					"role":   map[string]interface{}{"type": "string", "description": "Role (e.g. coder, tester, devops)"},
					"prompt": map[string]interface{}{"type": "string", "description": "Initial task/prompt for the teammate"},
				},
				"required": []string{"name", "role", "prompt"},
			},
		},
		Permission: types.PermExecute,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			name := utils.GetString(args, "name")
			role := utils.GetString(args, "role")
			prompt := utils.GetString(args, "prompt")
			if name == "" || role == "" || prompt == "" {
				return &types.ToolResult{Content: "Error: name, role, and prompt required", IsError: true}, nil
			}
			if err := mgr.Spawn(ctx, name, role, prompt); err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return &types.ToolResult{Content: fmt.Sprintf("Spawned teammate '%s' (role: %s)", name, role)}, nil
		},
	}
}

func toolShutdownTeammate(bus *MessageBus, tracker *RequestTracker) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "shutdown_teammate",
			Description: "Request a teammate to shut down gracefully.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Teammate to shut down"},
				},
				"required": []string{"name"},
			},
		},
		Permission: types.PermExecute,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			name := utils.GetString(args, "name")
			if name == "" {
				return &types.ToolResult{Content: "Error: name required", IsError: true}, nil
			}
			rec, err := RequestShutdown(bus, tracker, name)
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return &types.ToolResult{Content: fmt.Sprintf("Shutdown request sent to '%s' (request_id: %s)", name, rec.RequestID)}, nil
		},
	}
}

func toolBroadcast(bus *MessageBus, roster *Roster) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "broadcast",
			Description: "Send a message to all active teammates.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{"type": "string", "description": "Message to broadcast"},
				},
				"required": []string{"content"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			content := utils.GetString(args, "content")
			if content == "" {
				return &types.ToolResult{Content: "Error: content required", IsError: true}, nil
			}
			count, err := bus.Broadcast("lead", content, roster.ActiveNames())
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			return &types.ToolResult{Content: fmt.Sprintf("Broadcast to %d teammates", count)}, nil
		},
	}
}

func toolReviewPlan(bus *MessageBus, tracker *RequestTracker) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "review_plan",
			Description: "Approve or reject a teammate's plan submission.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"request_id": map[string]interface{}{"type": "string", "description": "Request ID of the plan to review"},
					"approve":    map[string]interface{}{"type": "boolean", "description": "Whether to approve the plan"},
					"feedback":   map[string]interface{}{"type": "string", "description": "Optional feedback"},
				},
				"required": []string{"request_id", "approve"},
			},
		},
		Permission: types.PermWrite,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			reqID := utils.GetString(args, "request_id")
			approve := utils.GetBool(args, "approve")
			feedback := utils.GetString(args, "feedback")
			if reqID == "" {
				return &types.ToolResult{Content: "Error: request_id required", IsError: true}, nil
			}
			if err := ReviewPlan(bus, tracker, reqID, approve, feedback); err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			action := "approved"
			if !approve {
				action = "rejected"
			}
			return &types.ToolResult{Content: fmt.Sprintf("Plan %s %s", reqID, action)}, nil
		},
	}
}

func toolListPendingRequests(tracker *RequestTracker) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "list_pending_requests",
			Description: "List all pending protocol requests (shutdowns, plan approvals).",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		Permission: types.PermRead,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			pending, err := tracker.ListPending()
			if err != nil {
				return &types.ToolResult{Content: fmt.Sprintf("Error: %v", err), IsError: true}, nil
			}
			if len(pending) == 0 {
				return &types.ToolResult{Content: "No pending requests."}, nil
			}
			data, _ := json.MarshalIndent(pending, "", "  ")
			return &types.ToolResult{Content: string(data)}, nil
		},
	}
}

func toolDelegateTask(mgr *Manager) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name: "delegate_task",
			Description: "Run a focused task in a clean, isolated context using a role-specialized agent. " +
				"The delegate starts with ZERO conversation history (context isolation), " +
				"executes the task with the role's system prompt and tools, returns the result, " +
				"and all its state is discarded. Use this instead of send_message when: " +
				"(1) the task is self-contained and doesn't need prior conversation context, " +
				"(2) you want the agent to focus purely on this task without distraction, or " +
				"(3) you want to avoid polluting an existing teammate's context window.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"role": map[string]interface{}{
						"type":        "string",
						"description": "Role template to use (e.g. code_reviewer, knowledge, devops, planner)",
					},
					"task": map[string]interface{}{
						"type":        "string",
						"description": "Complete task description. Be thorough — the delegate has no prior context.",
					},
				},
				"required": []string{"role", "task"},
			},
		},
		Permission: types.PermExecute,
		Source:     "team",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			role := utils.GetString(args, "role")
			task := utils.GetString(args, "task")
			if role == "" || task == "" {
				return &types.ToolResult{Content: "Error: role and task required", IsError: true}, nil
			}

			mgr.mu.RLock()
			tmpl, ok := mgr.templates[role]
			mgr.mu.RUnlock()
			if !ok {
				avail := make([]string, 0, len(mgr.templates))
				mgr.mu.RLock()
				for k := range mgr.templates {
					avail = append(avail, k)
				}
				mgr.mu.RUnlock()
				return &types.ToolResult{
					Content: fmt.Sprintf("Error: unknown role %q. Available: %v", role, avail),
					IsError: true,
				}, nil
			}

			if mgr.observer != nil {
				mgr.observer.Info("delegate_task started", "role", role, "task_len", len(task))
			}

			result, err := DelegateWork(ctx, mgr.model, mgr.deps, tmpl, task)
			if err != nil {
				return &types.ToolResult{
					Content: fmt.Sprintf("Delegate (%s) failed: %v", role, err),
					IsError: true,
				}, nil
			}

			if mgr.observer != nil {
				mgr.observer.Info("delegate_task completed", "role", role, "result_len", len(result))
			}

			return &types.ToolResult{Content: result}, nil
		},
	}
}
