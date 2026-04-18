package planning

import (
	"context"
	"fmt"
)

// PlanExecutor takes a pending task and dispatches it to a background slot with an agent resolved from the title.
type PlanExecutor struct {
	taskManager   *TaskManager
	bgManager     *BackgroundManager
	agentResolver func(taskTitle string) string
	runner        func(ctx context.Context, agentName string, task *Task) (string, error)
}

// NewPlanExecutor wires task storage, background slots, and execution callbacks.
// agentResolver may be nil (defaults to "default"). runner performs the work and should be set for non-trivial use.
func NewPlanExecutor(
	tm *TaskManager,
	bg *BackgroundManager,
	agentResolver func(taskTitle string) string,
	runner func(ctx context.Context, agentName string, task *Task) (string, error),
) *PlanExecutor {
	return &PlanExecutor{
		taskManager:   tm,
		bgManager:     bg,
		agentResolver: agentResolver,
		runner:        runner,
	}
}

func (e *PlanExecutor) resolveAgent(title string) string {
	if e.agentResolver != nil {
		if a := e.agentResolver(title); a != "" {
			return a
		}
	}
	return "default"
}

// ExecuteNext claims the first available unclaimed pending task and submits it to the background manager.
// Returns the background slot ID, or an error if no work or submission fails.
func (e *PlanExecutor) ExecuteNext(ctx context.Context) (slotID string, err error) {
	if e.taskManager == nil || e.bgManager == nil {
		return "", fmt.Errorf("plan executor: task manager or background manager is nil")
	}
	tasks := e.taskManager.GetUnclaimed()
	if len(tasks) == 0 {
		return "", fmt.Errorf("plan executor: no unclaimed pending tasks")
	}
	return e.ExecuteTask(ctx, tasks[0].ID)
}

// ExecuteTask claims a specific task (if still unclaimed and runnable) and starts it in a background slot.
func (e *PlanExecutor) ExecuteTask(ctx context.Context, taskID int) (slotID string, err error) {
	if e.taskManager == nil || e.bgManager == nil {
		return "", fmt.Errorf("plan executor: task manager or background manager is nil")
	}

	t, err := e.taskManager.Get(taskID)
	if err != nil {
		return "", err
	}
	agent := e.resolveAgent(t.Title)

	claimed, err := e.taskManager.Claim(taskID, agent, "", "manual")
	if err != nil {
		return "", err
	}

	taskCopy := *claimed
	tm := e.taskManager
	runner := e.runner

	wrapped := func(runCtx context.Context) (string, error) {
		if runner == nil {
			return "", fmt.Errorf("plan executor: runner not configured")
		}
		res, runErr := runner(runCtx, agent, &taskCopy)
		if runErr != nil {
			_ = tm.Update(taskID, TaskPending)
			return res, runErr
		}
		if uerr := tm.Update(taskID, TaskCompleted); uerr != nil {
			return res, fmt.Errorf("mark completed: %w", uerr)
		}
		return res, nil
	}

	return e.bgManager.Submit(ctx, taskID, agent, wrapped)
}

// PlanStats summarizes task distribution for monitoring.
type PlanStats struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Blocked    int `json:"blocked"`
	Cancelled  int `json:"cancelled"`
}

// MonitorProgress returns completion and status counts across all tasks.
func (e *PlanExecutor) MonitorProgress() PlanStats {
	var s PlanStats
	if e.taskManager == nil {
		return s
	}
	all := e.taskManager.List(nil)
	s.Total = len(all)
	for i := range all {
		switch all[i].Status {
		case TaskPending:
			s.Pending++
		case TaskInProgress:
			s.InProgress++
		case TaskCompleted:
			s.Completed++
		case TaskBlocked:
			s.Blocked++
		case TaskCancelled:
			s.Cancelled++
		}
	}
	return s
}
