// Package approval implements the human-in-the-loop approval center: a pending
// approval queue, one-time and persistent grants, expiry, and audit surface.
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of an approval request.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusExpired  Status = "expired"
)

// Request is one pending (or decided) approval for a tool invocation.
type Request struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
	Reason    string                 `json:"reason"`
	Status    Status                 `json:"status"`
	CreatedAt time.Time              `json:"created_at"`
	ExpiresAt time.Time              `json:"expires_at,omitempty"`
	DecidedAt time.Time              `json:"decided_at,omitempty"`
}

// Manager is a thread-safe in-memory approval queue plus grant store.
type Manager struct {
	mu       sync.RWMutex
	ttl      time.Duration
	requests map[string]*Request
	once     map[string]time.Time // fingerprint -> expiry (consumed on use)
	always   map[string]struct{}  // fingerprint -> persistent allow
}

// NewManager creates a manager with the given pending-request TTL.
func NewManager(ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Manager{
		ttl:      ttl,
		requests: make(map[string]*Request),
		once:     make(map[string]time.Time),
		always:   make(map[string]struct{}),
	}
}

// Create registers a pending approval and returns a copy of it.
func (m *Manager) Create(sessionID, toolName string, args map[string]interface{}, reason string) *Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.expireLocked(now)
	r := &Request{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		ToolName:  toolName,
		Arguments: args,
		Reason:    reason,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(m.ttl),
	}
	m.requests[r.ID] = r
	return cloneRequest(r)
}

// Get returns a copy of a request by id.
func (m *Manager) Get(id string) (*Request, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	r, ok := m.requests[id]
	if !ok {
		return nil, false
	}
	return cloneRequest(r), true
}

// List returns requests, optionally limited to pending, newest first.
func (m *Manager) List(pendingOnly bool) []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	out := make([]Request, 0, len(m.requests))
	for _, r := range m.requests {
		if pendingOnly && r.Status != StatusPending {
			continue
		}
		out = append(out, *cloneRequest(r))
	}
	sortRequests(out)
	return out
}

// Approve grants the request. If persist is true the grant is remembered as a
// persistent allow rule for the same tool+arguments; otherwise it is a one-shot
// grant consumed on the next matching call.
func (m *Manager) Approve(id string, persist bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requests[id]
	if !ok {
		return fmt.Errorf("approval: unknown request %q", id)
	}
	if r.Status != StatusPending {
		return fmt.Errorf("approval: request %q already %s", id, r.Status)
	}
	fp := fingerprint(r.ToolName, r.Arguments)
	if persist {
		m.always[fp] = struct{}{}
	} else {
		m.once[fp] = time.Now().Add(m.ttl)
	}
	r.Status = StatusApproved
	r.DecidedAt = time.Now()
	return nil
}

// Deny rejects the request.
func (m *Manager) Deny(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requests[id]
	if !ok {
		return fmt.Errorf("approval: unknown request %q", id)
	}
	if r.Status != StatusPending {
		return fmt.Errorf("approval: request %q already %s", id, r.Status)
	}
	r.Status = StatusDenied
	r.DecidedAt = time.Now()
	return nil
}

// IsGranted reports whether a one-time or persistent grant authorizes this call.
// One-time grants are consumed on a successful match.
func (m *Manager) IsGranted(toolName string, args map[string]interface{}) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	fp := fingerprint(toolName, args)
	if _, ok := m.always[fp]; ok {
		return true
	}
	if exp, ok := m.once[fp]; ok {
		delete(m.once, fp)
		return !time.Now().After(exp)
	}
	return false
}

// PendingCount returns the number of pending approvals.
func (m *Manager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	n := 0
	for _, r := range m.requests {
		if r.Status == StatusPending {
			n++
		}
	}
	return n
}

func (m *Manager) expireLocked(now time.Time) {
	for _, r := range m.requests {
		if r.Status == StatusPending && !r.ExpiresAt.IsZero() && now.After(r.ExpiresAt) {
			r.Status = StatusExpired
			r.DecidedAt = now
		}
	}
	for fp, exp := range m.once {
		if now.After(exp) {
			delete(m.once, fp)
		}
	}
}

func cloneRequest(r *Request) *Request {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Arguments != nil {
		cp.Arguments = make(map[string]interface{}, len(r.Arguments))
		for k, v := range r.Arguments {
			cp.Arguments[k] = v
		}
	}
	return &cp
}

func sortRequests(rs []Request) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].CreatedAt.After(rs[j-1].CreatedAt); j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

// fingerprint produces a deterministic identity for a tool invocation.
// encoding/json sorts map keys, so marshaling is stable across runs.
func fingerprint(toolName string, args map[string]interface{}) string {
	b, _ := json.Marshal(args)
	h := sha256.Sum256(append([]byte(toolName+"\x00"), b...))
	return hex.EncodeToString(h[:])
}
