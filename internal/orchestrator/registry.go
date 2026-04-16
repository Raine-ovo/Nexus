// Package orchestrator implements the multi-agent supervisor: intent routing,
// agent registry, and handoff protocol for sequential delegation across agents.
package orchestrator

import (
	"fmt"
	"sort"
	"sync"

	"github.com/rainea/nexus/internal/core"
)

// AgentRegistry is a thread-safe registry of named agents.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]core.Agent
}

// NewAgentRegistry returns an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]core.Agent),
	}
}

// Register adds or replaces an agent under name. Name must match agent.Name() when non-empty.
func (r *AgentRegistry) Register(name string, agent core.Agent) error {
	if r == nil {
		return fmt.Errorf("orchestrator: nil AgentRegistry")
	}
	if name == "" {
		return fmt.Errorf("orchestrator: agent name required")
	}
	if agent == nil {
		return fmt.Errorf("orchestrator: nil agent")
	}
	if n := agent.Name(); n != "" && n != name {
		return fmt.Errorf("orchestrator: register name %q does not match agent.Name() %q", name, n)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = make(map[string]core.Agent)
	}
	r.agents[name] = agent
	return nil
}

// Unregister removes an agent by name. No-op if missing.
func (r *AgentRegistry) Unregister(name string) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, name)
}

// Get returns the agent and whether it was found.
func (r *AgentRegistry) Get(name string) (core.Agent, bool) {
	if r == nil || name == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok && a != nil
}

// Has reports whether a non-nil agent is registered under name.
func (r *AgentRegistry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// Names returns registered agent names in sorted order.
func (r *AgentRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.agents))
	for n := range r.agents {
		if r.agents[n] == nil {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Count returns how many agents are registered.
func (r *AgentRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, a := range r.agents {
		if a != nil {
			n++
		}
	}
	return n
}

// List returns a shallow copy of the name→agent map. Values are the same interface instances as in the registry.
func (r *AgentRegistry) List() map[string]core.Agent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make(map[string]core.Agent, len(r.agents))
	for k, v := range r.agents {
		if v == nil {
			continue
		}
		cp[k] = v
	}
	return cp
}

// AgentInfo describes an agent for the supervisor's routing decisions.
type AgentInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

// ListInfo returns metadata for all registered agents (sorted by name).
func (r *AgentRegistry) ListInfo() []AgentInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.agents))
	for n, a := range r.agents {
		if a == nil {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]AgentInfo, 0, len(names))
	for _, name := range names {
		a := r.agents[name]
		if a == nil {
			continue
		}
		info := AgentInfo{
			Name:         name,
			Description:  a.Description(),
			Capabilities: toolCapabilityNames(a),
		}
		out = append(out, info)
	}
	return out
}

func toolCapabilityNames(a core.Agent) []string {
	tools := a.GetTools()
	if len(tools) == 0 {
		return nil
	}
	cap := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		n := t.Definition.Name
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		cap = append(cap, n)
	}
	sort.Strings(cap)
	return cap
}
