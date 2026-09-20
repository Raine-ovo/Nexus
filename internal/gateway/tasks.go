package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/rainea/nexus/internal/planning"
)

// TaskProvider exposes the persistent task DAG for the desktop UI's task board.
type TaskProvider interface {
	ListTasks() []planning.Task
}

// SetTaskProvider wires the task manager into the gateway.
func (g *Gateway) SetTaskProvider(p TaskProvider) {
	g.taskProvider = p
}

// handleListTasks returns the full task DAG (all statuses) as JSON.
func (g *Gateway) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.taskProvider == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "task provider unavailable"})
		return
	}
	tasks := g.taskProvider.ListTasks()
	if tasks == nil {
		tasks = []planning.Task{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count": len(tasks),
		"tasks": tasks,
	})
}
