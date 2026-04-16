package team

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/pkg/utils"
)

// Request status constants.
const (
	ReqStatusPending  = "pending"
	ReqStatusApproved = "approved"
	ReqStatusRejected = "rejected"
	ReqStatusExpired  = "expired"
)

// Request kind constants.
const (
	ReqKindShutdown     = "shutdown"
	ReqKindPlanApproval = "plan_approval"
)

// RequestRecord tracks one protocol request through its lifecycle.
type RequestRecord struct {
	RequestID string  `json:"request_id"`
	Kind      string  `json:"kind"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Status    string  `json:"status"`
	CreatedAt float64 `json:"created_at"`
	UpdatedAt float64 `json:"updated_at,omitempty"`
	// Plan holds the plan text for plan_approval requests.
	Plan string `json:"plan,omitempty"`
	// Feedback holds the reviewer's optional feedback.
	Feedback string `json:"feedback,omitempty"`
}

// RequestTracker manages protocol request records on disk.
type RequestTracker struct {
	dir string
	mu  sync.Mutex
}

// NewRequestTracker creates or opens the request store at reqDir.
func NewRequestTracker(reqDir string) (*RequestTracker, error) {
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		return nil, fmt.Errorf("protocol: mkdir %s: %w", reqDir, err)
	}
	return &RequestTracker{dir: reqDir}, nil
}

func (rt *RequestTracker) path(id string) string {
	return filepath.Join(rt.dir, id+".json")
}

// NewID generates a fresh request ID.
func NewRequestID() string {
	return "req_" + uuid.New().String()[:8]
}

// Create persists a new pending request and returns it.
func (rt *RequestTracker) Create(kind, from, to string) (*RequestRecord, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rec := &RequestRecord{
		RequestID: NewRequestID(),
		Kind:      kind,
		From:      from,
		To:        to,
		Status:    ReqStatusPending,
		CreatedAt: float64(time.Now().UnixMilli()) / 1000.0,
	}
	if err := utils.WriteJSON(rt.path(rec.RequestID), rec); err != nil {
		return nil, fmt.Errorf("protocol: create: %w", err)
	}
	return rec, nil
}

// Get loads a request record by ID.
func (rt *RequestTracker) Get(id string) (*RequestRecord, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var rec RequestRecord
	if err := utils.ReadJSON(rt.path(id), &rec); err != nil {
		return nil, fmt.Errorf("protocol: get %s: %w", id, err)
	}
	return &rec, nil
}

// UpdateStatus transitions a request to a new status.
func (rt *RequestTracker) UpdateStatus(id, status string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var rec RequestRecord
	if err := utils.ReadJSON(rt.path(id), &rec); err != nil {
		return fmt.Errorf("protocol: get %s: %w", id, err)
	}
	rec.Status = status
	rec.UpdatedAt = float64(time.Now().UnixMilli()) / 1000.0
	return utils.WriteJSON(rt.path(id), &rec)
}

// SetFeedback stores reviewer feedback on a request.
func (rt *RequestTracker) SetFeedback(id, feedback string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var rec RequestRecord
	if err := utils.ReadJSON(rt.path(id), &rec); err != nil {
		return fmt.Errorf("protocol: get %s: %w", id, err)
	}
	rec.Feedback = feedback
	rec.UpdatedAt = float64(time.Now().UnixMilli()) / 1000.0
	return utils.WriteJSON(rt.path(id), &rec)
}

// ListPending returns all requests in "pending" status.
func (rt *RequestTracker) ListPending() ([]RequestRecord, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	entries, err := os.ReadDir(rt.dir)
	if err != nil {
		return nil, fmt.Errorf("protocol: list: %w", err)
	}
	var out []RequestRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var rec RequestRecord
		if err := utils.ReadJSON(filepath.Join(rt.dir, e.Name()), &rec); err != nil {
			continue
		}
		if rec.Status == ReqStatusPending {
			out = append(out, rec)
		}
	}
	return out, nil
}

// --- Protocol helpers: Shutdown ---

// RequestShutdown creates a shutdown request and delivers it via the bus.
func RequestShutdown(bus *MessageBus, tracker *RequestTracker, target string) (*RequestRecord, error) {
	rec, err := tracker.Create(ReqKindShutdown, "lead", target)
	if err != nil {
		return nil, err
	}
	extra := map[string]interface{}{"request_id": rec.RequestID}
	if err := bus.Send("lead", target, "Please shut down gracefully.", MsgTypeShutdownRequest, extra); err != nil {
		return nil, err
	}
	return rec, nil
}

// RespondShutdown handles the teammate's response to a shutdown request.
func RespondShutdown(bus *MessageBus, tracker *RequestTracker, requestID string, approve bool, responder string) error {
	status := ReqStatusApproved
	if !approve {
		status = ReqStatusRejected
	}
	if err := tracker.UpdateStatus(requestID, status); err != nil {
		return err
	}
	content := "Shutdown approved. Exiting."
	if !approve {
		content = "Shutdown rejected."
	}
	extra := map[string]interface{}{"request_id": requestID, "approve": approve}
	return bus.Send(responder, "lead", content, MsgTypeShutdownResponse, extra)
}

// --- Protocol helpers: Plan Approval ---

// SubmitPlan creates a plan_approval request from a teammate to the lead.
func SubmitPlan(bus *MessageBus, tracker *RequestTracker, from, planText string) (*RequestRecord, error) {
	rec, err := tracker.Create(ReqKindPlanApproval, from, "lead")
	if err != nil {
		return nil, err
	}
	// Store the plan text in the record.
	rec.Plan = planText
	rec.UpdatedAt = float64(time.Now().UnixMilli()) / 1000.0
	if err := utils.WriteJSON(filepath.Join(tracker.dir, rec.RequestID+".json"), rec); err != nil {
		return nil, err
	}
	extra := map[string]interface{}{
		"request_id": rec.RequestID,
		"plan":       planText,
	}
	return rec, bus.Send(from, "lead", "Requesting plan review.", MsgTypePlanApproval, extra)
}

// ReviewPlan processes the lead's approval or rejection of a plan.
func ReviewPlan(bus *MessageBus, tracker *RequestTracker, requestID string, approve bool, feedback string) error {
	rec, err := tracker.Get(requestID)
	if err != nil {
		return err
	}
	status := ReqStatusApproved
	if !approve {
		status = ReqStatusRejected
	}
	if err := tracker.UpdateStatus(requestID, status); err != nil {
		return err
	}
	if feedback != "" {
		_ = tracker.SetFeedback(requestID, feedback)
	}
	extra := map[string]interface{}{
		"request_id": requestID,
		"approve":    approve,
	}
	content := feedback
	if content == "" {
		if approve {
			content = "Plan approved."
		} else {
			content = "Plan rejected."
		}
	}
	return bus.Send("lead", rec.From, content, MsgTypePlanApprovalResponse, extra)
}
