package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/pkg/types"
)

// Agent is the core abstraction. Every agent must implement this.
type Agent interface {
	Name() string
	Description() string
	Run(ctx context.Context, input string) (string, error)
	GetTools() []*types.ToolMeta
	GetSystemPrompt() string
}

// Observer is the minimal logging surface expected from internal/observability.
// Concrete implementations may expose richer APIs; the core only needs these events.
type Observer interface {
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Debug(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// PermPipeline checks tool invocations against workspace policy before execution.
// Implemented by internal/permission.Pipeline in the full binary.
type PermPipeline interface {
	CheckTool(ctx context.Context, toolName string, args map[string]interface{}) error
}

// ToolRegistry resolves built-in and shared tools by name (e.g. internal/tool.Registry).
// Using this interface keeps core free of a hard dependency on the tool package while
// matching the common Get(name) *ToolMeta pattern used in main.
type ToolRegistry interface {
	Get(name string) *types.ToolMeta
}

// SkillIndex provides a formatted skill directory for system prompt augmentation.
// Implemented by intelligence.SkillManager without core importing that package.
type SkillIndex interface {
	GetIndexPrompt() string
}

// PromptBuilder assembles bootstrap text, skill index, and memory into a system preamble.
type PromptBuilder interface {
	Build(memorySection string) (string, error)
}

// MemoryPromptBuilder formats dynamic memory for system prompt injection.
type MemoryPromptBuilder interface {
	BuildPromptSection() string
}

// AgentDependencies holds shared infrastructure injected from cmd/nexus/main.go:
//
//	ToolRegistry, PermPipeline, MemManager, TaskManager,
//	PromptAssembler, SkillManager, Observer, AgentConfig, ModelConfig
//
// Optional fields (WorkspaceRoot, ConversationWindow, Summarize) tune ContextGuard and
// auto-compaction. MemManager / TaskManager / PromptAssembler / SkillManager are typed as
// any so agents can cast to concrete types without forcing core to import every subsystem.
type AgentDependencies struct {
	ToolRegistry ToolRegistry
	PermPipeline PermPipeline
	MemManager   any
	TaskManager  any
	// PromptAssembler builds system context (e.g. workspace layout) for specialized agents.
	PromptAssembler any
	SkillManager    any
	Observer        Observer

	AgentConfig configs.AgentConfig
	ModelConfig configs.ModelConfig

	// WorkspaceRoot scopes on-disk output persistence for ContextGuard (defaults to ".").
	WorkspaceRoot string
	// ConversationWindow is the number of recent non-system messages to retain verbatim
	// before auto-summarization when Summarize is set.
	ConversationWindow int

	// Summarize optionally backs CompactionAuto. When nil, only micro/manual compaction runs.
	Summarize SummarizeFunc
}

// BaseAgent provides common functionality: local tool list, registry fallback, and ReAct loop delegation.
type BaseAgent struct {
	name         string
	description  string
	systemPrompt string

	deps       *AgentDependencies
	loop       *AgentLoop
	skillIndex SkillIndex

	mu    sync.RWMutex
	tools []*types.ToolMeta
	model ChatModel

	recovery *RecoveryManager
	guard    *ContextGuard
	hooks    *LoopHooks
}

// NewBaseAgent constructs an agent with a model and empty tool list.
// workspaceRoot overrides deps.WorkspaceRoot when non-empty.
func NewBaseAgent(deps *AgentDependencies, model ChatModel, name, description, systemPrompt, workspaceRoot string) *BaseAgent {
	if deps == nil {
		deps = &AgentDependencies{}
	}
	ws := deps.WorkspaceRoot
	if workspaceRoot != "" {
		ws = workspaceRoot
	}
	if ws == "" {
		ws = "."
	}
	cw := deps.ConversationWindow

	var si SkillIndex
	if deps.SkillManager != nil {
		si, _ = deps.SkillManager.(SkillIndex)
	}

	ba := &BaseAgent{
		name:         name,
		description:  description,
		systemPrompt: systemPrompt,
		deps:         deps,
		skillIndex:   si,
		model:        model,
		tools:        make([]*types.ToolMeta, 0, 8),
	}

	ba.recovery = NewRecoveryManager(DefaultRecoveryPolicy(), deps.Observer)
	ba.guard = NewContextGuard(deps.AgentConfig, ws, cw, deps.Observer, deps.Summarize)
	ba.loop = NewAgentLoop(
		deps,
		model,
		ba.guard,
		ba.recovery,
		ba.listTools,
		ba.getSystem,
		ws,
		cw,
		nil,
	)
	return ba
}

// Model returns the current chat model.
func (b *BaseAgent) Model() ChatModel {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.model
}

// SetModel replaces the chat model at runtime (e.g. after config hot-reload).
func (b *BaseAgent) SetModel(m ChatModel) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.model = m
	if b.loop != nil {
		b.loop.model = m
	}
}

// SetHooks installs loop hooks (tracing, policy, metrics). Safe to call with nil to clear.
func (b *BaseAgent) SetHooks(h *LoopHooks) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = h
	if b.loop != nil {
		b.loop.SetHooks(h)
	}
}

// Loop exposes the underlying AgentLoop for advanced embedding scenarios.
func (b *BaseAgent) Loop() *AgentLoop {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.loop
}

// Dependencies returns the injected dependency bundle (may be nil if constructed improperly).
func (b *BaseAgent) Dependencies() *AgentDependencies {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.deps
}

// Name returns the stable agent identifier.
func (b *BaseAgent) Name() string { return b.name }

// Description returns human-readable purpose text for routing UIs.
func (b *BaseAgent) Description() string { return b.description }

// GetSystemPrompt returns the system instruction used for each model call.
func (b *BaseAgent) GetSystemPrompt() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.systemPrompt
}

// SetSystemPrompt updates the system prompt for subsequent turns.
func (b *BaseAgent) SetSystemPrompt(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.systemPrompt = s
}

// GetTools returns a shallow copy of registered tools (per-agent tools only; registry tools are resolved at execution time).
func (b *BaseAgent) GetTools() []*types.ToolMeta {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*types.ToolMeta, len(b.tools))
	copy(out, b.tools)
	return out
}

// AddTool registers a tool for this agent instance. Duplicate names replace the previous entry.
func (b *BaseAgent) AddTool(meta *types.ToolMeta) error {
	if meta == nil {
		return fmt.Errorf("core: nil ToolMeta")
	}
	if meta.Definition.Name == "" {
		return fmt.Errorf("core: tool name required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	trimmed := make([]*types.ToolMeta, 0, len(b.tools)+1)
	for _, t := range b.tools {
		if t == nil {
			continue
		}
		if t.Definition.Name == meta.Definition.Name {
			continue
		}
		trimmed = append(trimmed, t)
	}
	trimmed = append(trimmed, meta)
	b.tools = trimmed
	return nil
}

// RemoveTool drops a tool by name.
func (b *BaseAgent) RemoveTool(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.tools[:0]
	for _, t := range b.tools {
		if t == nil || t.Definition.Name == name {
			continue
		}
		out = append(out, t)
	}
	b.tools = out
}

// ClearTools removes all locally registered tools.
func (b *BaseAgent) ClearTools() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = b.tools[:0]
}

// Run executes the ReAct loop for a single user utterance.
func (b *BaseAgent) Run(ctx context.Context, input string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("core: nil BaseAgent")
	}
	b.mu.RLock()
	loop := b.loop
	b.mu.RUnlock()
	if loop == nil {
		return "", fmt.Errorf("core: loop not initialized")
	}
	return loop.RunLoop(ctx, input)
}

func (b *BaseAgent) listTools() []*types.ToolMeta {
	return b.GetTools()
}

func (b *BaseAgent) getSystem() string {
	return BuildSystemPrompt(b.GetSystemPrompt(), b.deps, b.skillIndex, "", "")
}

// RegisterTools adds multiple tools; the first error stops the batch (earlier tools remain registered).
func (b *BaseAgent) RegisterTools(metas ...*types.ToolMeta) error {
	for _, m := range metas {
		if err := b.AddTool(m); err != nil {
			return err
		}
	}
	return nil
}

// LocalToolCount returns how many tools are attached to this agent (excluding global registry-only tools).
func (b *BaseAgent) LocalToolCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, t := range b.tools {
		if t != nil {
			n++
		}
	}
	return n
}

// ShallowCopyDependencies returns a copy of AgentConfig/ModelConfig and pointer-stable fields for forked agents.
// ToolRegistry and Observer references are shared (not deep-copied).
func ShallowCopyDependencies(d *AgentDependencies) *AgentDependencies {
	if d == nil {
		return &AgentDependencies{}
	}
	cp := *d
	return &cp
}

// BuildSystemPrompt composes the final system prompt from prompt assembler output,
// injected memory, the role-specific base prompt, and optional suffix instructions.
func BuildSystemPrompt(base string, deps *AgentDependencies, skillIndex SkillIndex, extraMemorySection, extraSuffix string) string {
	base = strings.TrimSpace(base)
	extraMemorySection = strings.TrimSpace(extraMemorySection)
	extraSuffix = strings.TrimSpace(extraSuffix)

	memorySection := ""
	if deps != nil && deps.MemManager != nil {
		if mp, ok := deps.MemManager.(MemoryPromptBuilder); ok {
			memorySection = strings.TrimSpace(mp.BuildPromptSection())
		}
	}
	if extraMemorySection != "" {
		if memorySection != "" {
			memorySection += "\n\n" + extraMemorySection
		} else {
			memorySection = extraMemorySection
		}
	}

	var preamble string
	if deps != nil && deps.PromptAssembler != nil {
		if pb, ok := deps.PromptAssembler.(PromptBuilder); ok {
			if built, err := pb.Build(memorySection); err == nil {
				preamble = strings.TrimSpace(built)
			}
		}
	}
	if preamble == "" && skillIndex != nil {
		idx := strings.TrimSpace(skillIndex.GetIndexPrompt())
		if idx != "" {
			preamble = idx + "\nUse list_skills / load_skill tools to access full skill content when needed."
		}
	}

	parts := make([]string, 0, 3)
	if preamble != "" {
		parts = append(parts, preamble)
	}
	if base != "" {
		parts = append(parts, base)
	}
	if extraSuffix != "" {
		parts = append(parts, extraSuffix)
	}
	return strings.Join(parts, "\n\n")
}
