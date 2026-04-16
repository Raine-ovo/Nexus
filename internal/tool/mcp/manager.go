package mcp

import (
	"io"
	"sync"
)

// Manager tracks MCP clients or transports that should be closed on shutdown.
type Manager struct {
	mu      sync.Mutex
	closers []io.Closer
}

// NewManager creates an empty manager (used from main for lifecycle hooks).
func NewManager() *Manager {
	return &Manager{}
}

// Add registers a closer invoked by CloseAll.
func (m *Manager) Add(c io.Closer) {
	if c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closers = append(m.closers, c)
}

// CloseAll closes all registered closers; errors after the first are ignored.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	list := m.closers
	m.closers = nil
	m.mu.Unlock()

	var first error
	for _, c := range list {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
