// Package tool provides a thread-safe tool registry, execution engine, and wiring
// for built-in tools. MCP integration lives in the mcp subpackage.
package tool

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/tool/builtin"
	"github.com/rainea/nexus/pkg/types"
)

// Registry holds all available tools indexed by name.
// Thread-safe. Supports dynamic registration and unregistration.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*types.ToolMeta
}

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]*types.ToolMeta),
	}
}

// Register adds or replaces a tool by definition name.
func (r *Registry) Register(meta *types.ToolMeta) error {
	if meta == nil {
		return fmt.Errorf("tool meta is nil")
	}
	name := meta.Definition.Name
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}
	if meta.Handler == nil {
		return fmt.Errorf("tool %q: handler is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = meta
	return nil
}

// MustRegister registers a tool or panics on validation failure.
func (r *Registry) MustRegister(meta *types.ToolMeta) {
	if err := r.Register(meta); err != nil {
		panic(err)
	}
}

// RegisterMany registers multiple tools; stops and returns the first error.
func (r *Registry) RegisterMany(metas []*types.ToolMeta) error {
	for _, m := range metas {
		if err := r.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// Unregister removes a tool by name. Returns false if it was not registered.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// Get returns metadata for a tool by name, or nil if missing.
func (r *Registry) Get(name string) *types.ToolMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Has reports whether a tool name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// List returns a snapshot of all registered tools sorted by name.
func (r *Registry) List() []*types.ToolMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*types.ToolMeta, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// ListNames returns sorted tool names (snapshot).
func (r *Registry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// GetDefinitions returns ToolDefinition slices suitable for LLM tool schemas.
func (r *Registry) GetDefinitions() []types.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]types.ToolDefinition, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n].Definition)
	}
	return out
}

// FilterBySource returns tools whose Source equals the given value (snapshot, sorted by name).
func (r *Registry) FilterBySource(source string) []*types.ToolMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for n, m := range r.tools {
		if m != nil && m.Source == source {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]*types.ToolMeta, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// NewToolID generates a unique identifier for a tool invocation.
func NewToolID() string {
	return uuid.NewString()
}

// RegisterBuiltins registers file, shell, HTTP, and search tools with the registry.
// workspaceRoot is used for sandbox path resolution; dangerousPatterns augments shell blocking (defaults apply if empty).
func RegisterBuiltins(r *Registry, workspaceRoot string, dangerousPatterns []string) {
	reg := func(meta *types.ToolMeta) error {
		return r.Register(meta)
	}
	_ = builtin.RegisterFileTools(reg, workspaceRoot)
	_ = builtin.RegisterShellTools(reg, workspaceRoot, dangerousPatterns)
	_ = builtin.RegisterHTTPTools(reg)
	_ = builtin.RegisterSearchTools(reg, workspaceRoot)
}
