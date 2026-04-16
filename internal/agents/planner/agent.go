// Package planner provides a BaseAgent for DAG-backed task planning and execution.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// PlannerAgent decomposes work into a persistent task DAG and coordinates background execution slots.
type PlannerAgent struct {
	*core.BaseAgent
	taskManager *planning.TaskManager
	bgManager   *planning.BackgroundManager
	executor    *planning.PlanExecutor
}

// New wires planning storage, background slots, and tool handlers.
func New(deps *core.AgentDependencies, tm *planning.TaskManager, bg *planning.BackgroundManager) *PlannerAgent {
	ws := "."
	if deps != nil && deps.WorkspaceRoot != "" {
		ws = deps.WorkspaceRoot
	}
	ba := core.NewBaseAgent(deps, core.DefaultChatModel,
		"planner",
		"Creates and manages task DAGs, updates status, monitors progress, and triggers background task execution.",
		SystemPrompt,
		ws,
	)
	p := &PlannerAgent{
		BaseAgent:   ba,
		taskManager: tm,
		bgManager:   bg,
	}
	p.executor = planning.NewPlanExecutor(tm, bg, nil, p.stubRunner(deps))
	_ = p.BaseAgent.RegisterTools(
		p.toolCreatePlan(),
		p.toolUpdateTask(),
		p.toolListTasks(),
		p.toolGetTask(),
		p.toolExecuteTask(),
		p.toolMonitorProgress(),
	)
	attachRegistryTools(ba, deps, "load_skill", "list_skills")
	return p
}

func (p *PlannerAgent) stubRunner(deps *core.AgentDependencies) func(context.Context, string, *planning.Task) (string, error) {
	return func(ctx context.Context, agentName string, task *planning.Task) (string, error) {
		if deps != nil && deps.Observer != nil {
			deps.Observer.Info("planner: background task finished (stub runner)",
				"task_id", task.ID, "title", task.Title, "agent", agentName)
		}
		return fmt.Sprintf("stub completion for task %d (%s) by %s", task.ID, task.Title, agentName), nil
	}
}

func attachRegistryTools(ba *core.BaseAgent, deps *core.AgentDependencies, names ...string) {
	if ba == nil || deps == nil || deps.ToolRegistry == nil {
		return
	}
	for _, name := range names {
		if m := deps.ToolRegistry.Get(name); m != nil {
			_ = ba.AddTool(m)
		}
	}
}

func toolResult(content string, err error) (*types.ToolResult, error) {
	if err != nil {
		return &types.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return &types.ToolResult{Content: content, IsError: false}, nil
}

func strArg(args map[string]interface{}, key string) string {
	return utils.GetString(args, key)
}

func intArg(args map[string]interface{}, key string) int {
	return utils.GetInt(args, key)
}

// --- create_plan ---

type planTaskSpec struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	BlockedByIndices   []int  `json:"blocked_by_indices"`
}

func (p *PlannerAgent) toolCreatePlan() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "create_plan",
			Description: "Create one or more tasks forming a DAG. blocked_by_indices refer to 0-based indices within the tasks array (not database ids).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"goal": map[string]interface{}{
						"type":        "string",
						"description": "High-level objective (stored as context, also used if tasks array is empty)",
					},
					"tasks": map[string]interface{}{
						"type":        "array",
						"description": "Ordered list of task specs",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"title": map[string]interface{}{
									"type": "string",
								},
								"description": map[string]interface{}{
									"type": "string",
								},
								"blocked_by_indices": map[string]interface{}{
									"type": "array",
									"items": map[string]interface{}{
										"type": "integer",
									},
								},
							},
							"required": []string{"title"},
						},
					},
				},
				"required": []string{"goal"},
			},
		},
		Permission: types.PermWrite,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			if p.taskManager == nil {
				return toolResult("", fmt.Errorf("task manager not configured"))
			}
			goal := strings.TrimSpace(strArg(args, "goal"))
			rawTasks, _ := args["tasks"].([]interface{})
			var specs []planTaskSpec
			for _, rt := range rawTasks {
				m, ok := rt.(map[string]interface{})
				if !ok {
					continue
				}
				var s planTaskSpec
				s.Title = utils.GetString(m, "title")
				s.Description = utils.GetString(m, "description")
				if bi, ok := m["blocked_by_indices"].([]interface{}); ok {
					for _, v := range bi {
						switch n := v.(type) {
						case float64:
							s.BlockedByIndices = append(s.BlockedByIndices, int(n))
						case int:
							s.BlockedByIndices = append(s.BlockedByIndices, n)
						case int64:
							s.BlockedByIndices = append(s.BlockedByIndices, int(n))
						}
					}
				}
				if s.Title != "" {
					specs = append(specs, s)
				}
			}
			if goal == "" && len(specs) == 0 {
				return toolResult("", fmt.Errorf("goal or tasks required"))
			}
			if len(specs) == 0 {
				specs = []planTaskSpec{{
					Title:       "Execute plan",
					Description: goal,
				}}
			}

			idMap := make([]int, len(specs))
			var created []map[string]interface{}
			for i, sp := range specs {
				var blocked []int
				for _, idx := range sp.BlockedByIndices {
					if idx < 0 || idx >= i {
						return toolResult("", fmt.Errorf("invalid blocked_by_indices %d for task %d", idx, i))
					}
					blocked = append(blocked, idMap[idx])
				}
				desc := sp.Description
				if desc == "" && i == 0 && goal != "" {
					desc = goal
				}
				t, err := p.taskManager.Create(sp.Title, desc, blocked)
				if err != nil {
					return toolResult("", err)
				}
				idMap[i] = t.ID
				created = append(created, map[string]interface{}{
					"local_index": i,
					"task":        t,
				})
			}
			out := map[string]interface{}{
				"goal":         goal,
				"tasks_created": created,
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (p *PlannerAgent) toolUpdateTask() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "update_task",
			Description: "Update a task's status: pending, in_progress, completed, blocked, cancelled.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "Task id",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "New status",
					},
				},
				"required": []string{"task_id", "status"},
			},
		},
		Permission: types.PermWrite,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			if p.taskManager == nil {
				return toolResult("", fmt.Errorf("task manager not configured"))
			}
			id := intArg(args, "task_id")
			if id <= 0 {
				if s := strArg(args, "task_id"); s != "" {
					if n, err := strconv.Atoi(s); err == nil {
						id = n
					}
				}
			}
			if id <= 0 {
				return toolResult("", fmt.Errorf("task_id must be positive"))
			}
			status := strings.TrimSpace(strArg(args, "status"))
			if err := p.taskManager.Update(id, status); err != nil {
				return toolResult("", err)
			}
			t, err := p.taskManager.Get(id)
			if err != nil {
				return toolResult("", err)
			}
			b, err := json.MarshalIndent(t, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (p *PlannerAgent) toolListTasks() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "list_tasks",
			Description: "List tasks sorted by id. Optional status filter.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status (optional)",
					},
				},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			if p.taskManager == nil {
				return toolResult("", fmt.Errorf("task manager not configured"))
			}
			filter := strings.TrimSpace(strArg(args, "status"))
			list := p.taskManager.List(func(t *planning.Task) bool {
				if filter == "" {
					return true
				}
				return strings.EqualFold(t.Status, filter)
			})
			b, err := json.MarshalIndent(list, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (p *PlannerAgent) toolGetTask() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "get_task",
			Description: "Fetch one task by id.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "Task id",
					},
				},
				"required": []string{"task_id"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			if p.taskManager == nil {
				return toolResult("", fmt.Errorf("task manager not configured"))
			}
			id := intArg(args, "task_id")
			if id <= 0 {
				if s := strArg(args, "task_id"); s != "" {
					if n, err := strconv.Atoi(s); err == nil {
						id = n
					}
				}
			}
			if id <= 0 {
				return toolResult("", fmt.Errorf("task_id must be positive"))
			}
			t, err := p.taskManager.Get(id)
			if err != nil {
				return toolResult("", err)
			}
			b, err := json.MarshalIndent(t, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (p *PlannerAgent) toolExecuteTask() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "execute_task",
			Description: "Claim a runnable task and submit it to the background manager (stub runner marks completed on success).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "Task id to execute",
					},
				},
				"required": []string{"task_id"},
			},
		},
		Permission: types.PermExecute,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			if p.executor == nil || p.bgManager == nil {
				return toolResult("", fmt.Errorf("executor not configured"))
			}
			id := intArg(args, "task_id")
			if id <= 0 {
				if s := strArg(args, "task_id"); s != "" {
					if n, err := strconv.Atoi(s); err == nil {
						id = n
					}
				}
			}
			if id <= 0 {
				return toolResult("", fmt.Errorf("task_id must be positive"))
			}
			slotID, err := p.executor.ExecuteTask(ctx, id)
			if err != nil {
				return toolResult("", err)
			}
			out := map[string]interface{}{
				"slot_id": slotID,
				"task_id": id,
				"note":    "Use monitor_progress or wait on slot completion in a future integration",
			}
			b, jerr := json.MarshalIndent(out, "", "  ")
			if jerr != nil {
				return toolResult("", jerr)
			}
			return toolResult(string(b), nil)
		},
	}
}

func (p *PlannerAgent) toolMonitorProgress() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "monitor_progress",
			Description: "Aggregate task counts by status and background slot usage.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/planner",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			_ = args
			if p.executor == nil {
				return toolResult("", fmt.Errorf("executor not configured"))
			}
			stats := p.executor.MonitorProgress()
			out := map[string]interface{}{
				"tasks": stats,
			}
			if p.bgManager != nil {
				out["background_slots_active"] = p.bgManager.ActiveCount()
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}
