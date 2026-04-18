package gateway

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionManager manages active user sessions.
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	ttl      time.Duration
}

// Session is a single client session.
type Session struct {
	ID         string
	Channel    string
	User       string
	AgentID    string
	Scope      string
	Workstream string
	CreatedAt  time.Time
	LastActive time.Time
	Metadata   map[string]interface{}
}

// NewSessionManager creates a manager with the given TTL for expiry.
func NewSessionManager(ttl time.Duration) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

// Create registers a new session.
func (m *SessionManager) Create(channel, user string) *Session {
	return m.CreateWithOptions(channel, user, "", "")
}

// CreateWithOptions registers a new session with optional scope/workstream hints.
func (m *SessionManager) CreateWithOptions(channel, user, scope, workstream string) *Session {
	now := time.Now()
	s := &Session{
		ID:         uuid.NewString(),
		Channel:    channel,
		User:       user,
		Scope:      scope,
		Workstream: workstream,
		CreatedAt:  now,
		LastActive: now,
		Metadata:   make(map[string]interface{}),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	return s
}

// Get returns a session by id.
func (m *SessionManager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok || m.isExpiredLocked(s) {
		return nil, false
	}
	return s, true
}

func (m *SessionManager) isExpiredLocked(s *Session) bool {
	if m.ttl <= 0 {
		return false
	}
	return time.Since(s.LastActive) > m.ttl
}

// Touch updates LastActive if the session exists and is valid.
func (m *SessionManager) Touch(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || m.isExpiredLocked(s) {
		return
	}
	s.LastActive = time.Now()
}

// Delete removes a session.
func (m *SessionManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Cleanup removes expired sessions and returns how many were removed.
func (m *SessionManager) Cleanup() int {
	if m.ttl <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, s := range m.sessions {
		if m.isExpiredLocked(s) {
			delete(m.sessions, id)
			n++
		}
	}
	return n
}

// Count returns the number of non-expired sessions.
func (m *SessionManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if !m.isExpiredLocked(s) {
			n++
		}
	}
	return n
}
