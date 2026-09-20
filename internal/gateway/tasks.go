package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/rainea/nexus/internal/planning"
)

// TaskController exposes the persistent task DAG for the desktop UI's task
// board: listing plus human-in-the-loop create/claim/complete/cancel actions.
type TaskController interface {
	ListTasks() []planning.Task
	CreateTask(title, desc string, blockedBy []int) (*planning.Task, error)
	ClaimTask(id int, agentName, agentRole string) (*planning.Task, error)
	UpdateTask(id int, status string) error
}

// SetTaskController wires the task manager into the gateway.
func (g *Gateway) SetTaskController(c TaskController) {
	g.taskController = c
}

// handleListTasks returns the full task DAG (all statuses) as JSON.
func (g *Gateway) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.taskController == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "task provider unavailable"})
		return
	}
	tasks := g.taskController.ListTasks()
	if tasks == nil {
		tasks = []planning.Task{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count": len(tasks),
		"tasks": tasks,
	})
}

type createTaskReq struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	BlockedBy   []int  `json:"blocked_by"`
}

func (g *Gateway) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.taskController == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "task provider unavailable"})
		return
	}
	var req createTaskReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "title required"})
		return
	}
	task, err := g.taskController.CreateTask(req.Title, strings.TrimSpace(req.Description), req.BlockedBy)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

type claimTaskReq struct {
	AgentName string `json:"agent_name"`
	AgentRole string `json:"agent_role"`
}

func (g *Gateway) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.taskController == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "task provider unavailable"})
		return
	}
	id, ok := taskIDFromPath(w, r)
	if !ok {
		return
	}
	var req claimTaskReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.AgentName = strings.TrimSpace(req.AgentName)
	if req.AgentName == "" {
		req.AgentName = "lead"
	}
	task, err := g.taskController.ClaimTask(id, req.AgentName, strings.TrimSpace(req.AgentRole))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

func (g *Gateway) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.taskController == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "task provider unavailable"})
		return
	}
	id, ok := taskIDFromPath(w, r)
	if !ok {
		return
	}
	// The route is either /api/tasks/{id}/complete or /api/tasks/{id}/cancel;
	// derive the target status from the URL suffix.
	status := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/complete"):
		status = planning.TaskCompleted
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		status = planning.TaskCancelled
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown task action"})
		return
	}
	if err := g.taskController.UpdateTask(id, status); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": status, "id": id})
}

func taskIDFromPath(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.PathValue("id"))
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid task id"})
		return 0, false
	}
	return id, true
}
