package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rainea/nexus/internal/planning"
)

const (
	defaultPollInterval = 3 * time.Second
	defaultMaxIdlePolls = 40 // ~2 min of idle before auto-shutdown
)

// ClaimEvent is appended to the claim event log whenever a task is claimed.
type ClaimEvent struct {
	Event  string  `json:"event"`
	TaskID int     `json:"task_id"`
	Owner  string  `json:"owner"`
	Role   string  `json:"role"`
	Source string  `json:"source"` // "auto" or "manual"
	TS     float64 `json:"ts"`
}

// ClaimLogger appends claim events to a JSONL file.
type ClaimLogger struct {
	path string
	mu   sync.Mutex
}

// NewClaimLogger opens or creates the claim event log.
func NewClaimLogger(taskDir string) *ClaimLogger {
	return &ClaimLogger{path: filepath.Join(taskDir, "claim_events.jsonl")}
}

// Log appends one claim event.
func (cl *ClaimLogger) Log(taskID int, owner, role, source string) error {
	ev := ClaimEvent{
		Event:  "task.claimed",
		TaskID: taskID,
		Owner:  owner,
		Role:   role,
		Source: source,
		TS:     float64(time.Now().UnixMilli()) / 1000.0,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	dir := filepath.Dir(cl.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(cl.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// IsClaimable checks whether a task can be safely claimed by a teammate identity.
// A task is claimable when: pending, no owner, not blocked, and any assignee/role constraints match.
func IsClaimable(t *planning.Task, owner, role string) bool {
	if t.Status != planning.TaskPending {
		return false
	}
	if t.ClaimedBy != "" {
		return false
	}
	if len(t.BlockedBy) > 0 {
		// BlockedBy non-empty might still be claimable if all prereqs completed,
		// but GetUnclaimed already filters that. For standalone checks, treat non-empty as blocked.
		return false
	}
	if t.AssignedTo != "" && owner != "" && t.AssignedTo != owner {
		return false
	}
	requiredRole := t.AssignedRole
	if requiredRole == "" {
		requiredRole = t.ClaimRole
	}
	if requiredRole != "" && role != "" && requiredRole != role {
		return false
	}
	return true
}

// ScanClaimable returns unclaimed tasks that match the given role from the TaskManager.
func ScanClaimable(tm *planning.TaskManager, owner, role string) []planning.Task {
	unclaimed := tm.GetUnclaimed()
	var out []planning.Task
	for i := range unclaimed {
		if IsClaimable(&unclaimed[i], owner, role) {
			out = append(out, unclaimed[i])
		}
	}
	return out
}

// identityBlock returns a user message that re-establishes the teammate's identity
// after context compression or idle resume.
func identityBlock(name, role, teamName string) string {
	return fmt.Sprintf(
		"<identity>You are '%s', role: %s, team: %s. Continue your work.</identity>",
		name, role, teamName,
	)
}

// identityAck is the assistant message paired with identityBlock.
func identityAck(name string) string {
	return fmt.Sprintf("I am %s. Continuing.", name)
}
