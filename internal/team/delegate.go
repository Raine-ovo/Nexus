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

const maxDelegateIterations = 30

// DelegateWork runs a task in a fully isolated context using the given role template.
//
// Unlike Teammate.workPhase, this:
//   - Creates a fresh message list (clean context — no prior conversation history)
//   - Uses the role template's system prompt and tools
//   - Runs a synchronous model/tool loop until the model produces final text
//   - Returns the result and discards all state (no roster entry, no inbox, no idle loop)
//
// This is the s04 subagent pattern preserved inside the team system:
// "create → execute → return → destroyed", providing context isolation.
func DelegateWork(ctx context.Context, model core.ChatModel, deps *core.AgentDependencies,
	tmpl AgentTemplate, task string) (string, error) {

	if model == nil {
		return "", fmt.Errorf("delegate: model is nil")
	}
	if task == "" {
		return "", fmt.Errorf("delegate: empty task")
	}

	sysPrompt := tmpl.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = fmt.Sprintf("You are a helpful agent specialized in %s tasks.", tmpl.Role)
	}

	// Fresh message list — the core of context isolation.
	messages := []types.Message{
		{
			ID:        uuid.NewString(),
			Role:      types.RoleUser,
			Content:   task,
			CreatedAt: time.Now(),
		},
	}

	tools := tmpl.Tools
	toolDefs := toolDefinitions(tools)

	for iter := 0; iter < maxDelegateIterations; iter++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		resp, err := model.Generate(ctx, sysPrompt, messages, toolDefs)
		if err != nil {
			return "", fmt.Errorf("delegate: model generate: %w", err)
		}
		if resp == nil {
			return "", fmt.Errorf("delegate: nil model response")
		}

		if !hasToolCalls(resp) {
			return strings.TrimSpace(resp.Content), nil
		}

		// Append assistant message with tool calls.
		messages = append(messages, types.Message{
			ID:        uuid.NewString(),
			Role:      types.RoleAssistant,
			Content:   coalesce(resp.Content, "[tool_calls]"),
			ToolCalls: resp.ToolCalls,
			CreatedAt: time.Now(),
		})

		for _, call := range resp.ToolCalls {
			if call.ID == "" {
				call.ID = uuid.NewString()
			}
			result := executeToolWithDeps(ctx, deps, tools, call)
			messages = append(messages, types.Message{
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
	}

	return "", fmt.Errorf("delegate: exceeded max iterations (%d)", maxDelegateIterations)
}

// executeToolWithDeps resolves and executes a tool call, checking permissions.
// Shared by DelegateWork (no Teammate receiver needed).
func executeToolWithDeps(ctx context.Context, deps *core.AgentDependencies,
	localTools []*types.ToolMeta, call types.ToolCall) *types.ToolResult {

	meta := findToolIn(localTools, call.Name)
	if meta == nil && deps != nil && deps.ToolRegistry != nil {
		meta = deps.ToolRegistry.Get(call.Name)
	}
	if meta == nil {
		return &types.ToolResult{
			ToolID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Unknown tool: %s", call.Name),
			IsError: true,
		}
	}

	if deps != nil && deps.PermPipeline != nil {
		if err := deps.PermPipeline.CheckTool(ctx, call.Name, call.Arguments); err != nil {
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

func findToolIn(tools []*types.ToolMeta, name string) *types.ToolMeta {
	for _, t := range tools {
		if t != nil && t.Definition.Name == name {
			return t
		}
	}
	return nil
}
