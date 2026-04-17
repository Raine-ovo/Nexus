package team

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/pkg/types"
)

// AgentTemplate holds the configuration needed to spawn a teammate of a given role.
type AgentTemplate struct {
	Role         string
	SystemPrompt string
	Tools        []*types.ToolMeta
}

// ManagerConfig holds configuration for the TeammateManager.
type ManagerConfig struct {
	TeamDir      string
	PollInterval time.Duration
	IdleTimeout  time.Duration
	Model        core.ChatModel
	Deps         *core.AgentDependencies
	TaskManager  *planning.TaskManager
	Observer     core.Observer
	Runtime      *Runtime
	// LeadSystemPrompt is the base system prompt for the lead teammate.
	LeadSystemPrompt string
	// LeadBaseTools are non-team tools the lead should have (e.g. file/bash from registry).
	LeadBaseTools []*types.ToolMeta
}

// Manager orchestrates the team: roster, bus, protocol tracker, and teammates.
// It implements the gateway.Supervisor interface via HandleRequest.
type Manager struct {
	teamDir     string
	model       core.ChatModel
	deps        *core.AgentDependencies
	taskManager *planning.TaskManager
	observer    core.Observer
	runtime     *Runtime

	pollInterval time.Duration
	idleTimeout  time.Duration

	roster      *Roster
	bus         *MessageBus
	tracker     *RequestTracker
	claimLogger *ClaimLogger

	templates map[string]AgentTemplate

	lead      *Lead
	teammates map[string]*Teammate
	mu        sync.RWMutex
}

// NewManager creates the team infrastructure and starts the lead.
func NewManager(ctx context.Context, cfg ManagerConfig) (*Manager, error) {
	teamDir := cfg.TeamDir
	if teamDir == "" {
		teamDir = ".team"
	}

	roster, err := NewRoster(teamDir)
	if err != nil {
		return nil, fmt.Errorf("team: roster: %w", err)
	}
	bus, err := NewMessageBus(filepath.Join(teamDir, "inbox"))
	if err != nil {
		return nil, fmt.Errorf("team: bus: %w", err)
	}
	tracker, err := NewRequestTracker(filepath.Join(teamDir, "requests"))
	if err != nil {
		return nil, fmt.Errorf("team: tracker: %w", err)
	}

	taskDir := ""
	if cfg.Deps != nil {
		if ac, ok := cfg.Deps.TaskManager.(*planning.TaskManager); ok {
			_ = ac // just type-check
		}
		taskDir = teamDir
	}
	claimLogger := NewClaimLogger(taskDir)

	m := &Manager{
		teamDir:      teamDir,
		model:        cfg.Model,
		deps:         cfg.Deps,
		taskManager:  cfg.TaskManager,
		observer:     cfg.Observer,
		runtime:      cfg.Runtime,
		pollInterval: normalizePollInterval(cfg.PollInterval),
		idleTimeout:  normalizeIdleTimeout(cfg.IdleTimeout),
		roster:       roster,
		bus:          bus,
		tracker:      tracker,
		claimLogger:  claimLogger,
		templates:    make(map[string]AgentTemplate),
		teammates:    make(map[string]*Teammate),
	}

	// Build lead tools: base tools + team tools + lead-only tools.
	leadTools := make([]*types.ToolMeta, 0, len(cfg.LeadBaseTools)+16)
	leadTools = append(leadTools, cfg.LeadBaseTools...)
	leadTools = append(leadTools, BuildTeammateTools(leadName, bus, roster, tracker, cfg.TaskManager, claimLogger, m)...)
	leadTools = append(leadTools, BuildLeadTools(m, bus, roster, tracker)...)

	lead := newLead(m, cfg.Model, cfg.Deps, leadTools, cfg.LeadSystemPrompt)

	// Register lead in roster.
	if _, exists := roster.Get(leadName); !exists {
		_ = roster.Add(leadName, "lead", StatusIdle)
	}
	m.lead = lead
	lead.Start(ctx)

	// Re-hydrate previously active teammates.
	m.rehydrate(ctx)

	if m.observer != nil {
		m.observer.Info("team manager started", "team_dir", teamDir, "members", roster.Names())
	}

	return m, nil
}

// HandleRequest implements gateway.Supervisor: delivers user input to the lead.
func (m *Manager) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	if m.lead == nil {
		return "", fmt.Errorf("team: lead not initialized")
	}
	return m.lead.HandleRequest(ctx, sessionID, input)
}

// RegisterTemplate makes an agent template available for spawning teammates.
func (m *Manager) RegisterTemplate(role string, tmpl AgentTemplate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[role] = tmpl
}

// Spawn creates and starts a new teammate.
func (m *Manager) Spawn(ctx context.Context, name, role, prompt string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == leadName {
		return fmt.Errorf("team: cannot spawn teammate named '%s'", leadName)
	}
	if _, exists := m.teammates[name]; exists {
		return fmt.Errorf("team: teammate '%s' already running", name)
	}

	// Check if re-activating an idle/shutdown member.
	if member, ok := m.roster.Get(name); ok {
		if member.Status == StatusWorking {
			return fmt.Errorf("team: teammate '%s' is currently working", name)
		}
		_ = m.roster.UpdateStatus(name, StatusWorking)
		_ = m.roster.UpdateRole(name, role)
	} else {
		if err := m.roster.Add(name, role, StatusWorking); err != nil {
			return err
		}
	}

	// Resolve tools: template tools + team tools.
	var baseTools []*types.ToolMeta
	sysPrompt := ""
	if tmpl, ok := m.templates[role]; ok {
		baseTools = append(baseTools, tmpl.Tools...)
		sysPrompt = tmpl.SystemPrompt
	}
	if sysPrompt == "" {
		sysPrompt = fmt.Sprintf("You are a helpful agent specialized in %s tasks.", role)
	}

	teamTools := BuildTeammateTools(name, m.bus, m.roster, m.tracker, m.taskManager, m.claimLogger, nil)
	allTools := make([]*types.ToolMeta, 0, len(baseTools)+len(teamTools))
	allTools = append(allTools, baseTools...)
	allTools = append(allTools, teamTools...)

	cfg := TeammateConfig{
		Name:         name,
		Role:         role,
		Model:        m.model,
		Deps:         m.deps,
		Tools:        allTools,
		SystemPrompt: sysPrompt,
		Bus:          m.bus,
		Roster:       m.roster,
		Tracker:      m.tracker,
		TaskManager:  m.taskManager,
		ClaimLogger:  m.claimLogger,
		Observer:     m.observer,
		Runtime:      m.runtime,
		PollInterval: m.pollInterval,
		MaxIdlePolls: maxIdlePollsFor(m.pollInterval, m.idleTimeout),
	}

	teammate := NewTeammate(cfg)
	m.teammates[name] = teammate
	teammate.Start(ctx, prompt)

	// Clean up when teammate exits.
	go func() {
		<-teammate.Done()
		m.mu.Lock()
		delete(m.teammates, name)
		m.mu.Unlock()
	}()

	if m.observer != nil {
		m.observer.Info("teammate spawned", "name", name, "role", role)
	}
	return nil
}

func normalizePollInterval(v time.Duration) time.Duration {
	if v <= 0 {
		return defaultPollInterval
	}
	return v
}

func normalizeIdleTimeout(v time.Duration) time.Duration {
	if v <= 0 {
		return 20 * time.Minute
	}
	return v
}

func maxIdlePollsFor(pollInterval, idleTimeout time.Duration) int {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	if idleTimeout <= 0 {
		idleTimeout = 20 * time.Minute
	}
	polls := int((idleTimeout + pollInterval - 1) / pollInterval)
	if polls < 1 {
		return 1
	}
	return polls
}

func resumePromptFor(member TeamMember) string {
	return fmt.Sprintf(
		"You are resuming work after being reactivated. Your role is %s. Check your inbox immediately for follow-up instructions, then continue any unfinished work.",
		member.Role,
	)
}

func (m *Manager) SendMessage(ctx context.Context, sender, target, content string) error {
	if target == "" {
		return fmt.Errorf("team: target required")
	}
	if content == "" {
		return fmt.Errorf("team: content required")
	}
	if target == leadName {
		return m.bus.Send(sender, target, content, MsgTypeMessage, nil)
	}

	member, ok := m.roster.Get(target)
	if !ok {
		return fmt.Errorf("team: teammate %q does not exist", target)
	}
	if member.Status == StatusShutdown {
		if err := m.Spawn(ctx, member.Name, member.Role, resumePromptFor(member)); err != nil {
			return fmt.Errorf("team: revive teammate %q: %w", target, err)
		}
		if m.observer != nil {
			m.observer.Info("teammate revived for send_message", "name", member.Name, "role", member.Role)
		}
	}
	return m.bus.Send(sender, target, content, MsgTypeMessage, nil)
}

// ShutdownTeammate requests graceful shutdown of a teammate.
func (m *Manager) ShutdownTeammate(name string) error {
	m.mu.RLock()
	t, ok := m.teammates[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("team: teammate '%s' not running", name)
	}
	rec, err := RequestShutdown(m.bus, m.tracker, name)
	if err != nil {
		return err
	}
	t.RequestShutdown(rec.RequestID)
	return nil
}

// ListTeammates returns current roster state.
func (m *Manager) ListTeammates() []TeamMember {
	return m.roster.List()
}

// Roster returns the underlying roster.
func (m *Manager) Roster() *Roster { return m.roster }

// Bus returns the underlying message bus.
func (m *Manager) Bus() *MessageBus { return m.bus }

// Shutdown gracefully stops all teammates and the lead.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.teammates))
	for name := range m.teammates {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		_ = m.ShutdownTeammate(name)
	}

	// Wait briefly for teammates to exit.
	for _, name := range names {
		m.mu.RLock()
		t, ok := m.teammates[name]
		m.mu.RUnlock()
		if ok {
			select {
			case <-t.Done():
			case <-ctx.Done():
			}
		}
	}

	if m.lead != nil {
		m.lead.Stop()
	}
}

// rehydrate restarts teammates that were active (working/idle) in a previous run.
func (m *Manager) rehydrate(ctx context.Context) {
	members := m.roster.List()
	for _, member := range members {
		if member.Name == leadName {
			continue
		}
		if member.Status == StatusShutdown {
			continue
		}
		if _, running := m.teammates[member.Name]; running {
			continue
		}
		if member.Status == StatusWorking {
			if err := m.roster.UpdateStatus(member.Name, StatusIdle); err == nil {
				member.Status = StatusIdle
				if m.observer != nil {
					m.observer.Info("team: reset stale teammate status", "name", member.Name, "from", StatusWorking, "to", StatusIdle)
				}
			}
		}
		// Re-spawn with a resume prompt.
		prompt := fmt.Sprintf("You are resuming work. Your role is %s. Check your inbox for pending messages and the task board for work.", member.Role)
		if err := m.Spawn(ctx, member.Name, member.Role, prompt); err != nil {
			if m.observer != nil {
				m.observer.Warn("team: failed to rehydrate teammate", "name", member.Name, "err", err)
			}
		}
	}
}
