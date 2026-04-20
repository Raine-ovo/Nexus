package team

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/gateway"
	gatewaymw "github.com/rainea/nexus/internal/gateway/middleware"
	"github.com/rainea/nexus/internal/memory"
	"github.com/rainea/nexus/pkg/utils"
)

const recentInputLimit = 6
const scopeMatchThreshold = 6

// RegistryConfig describes how scoped teams should be created on demand.
type RegistryConfig struct {
	BaseManagerConfig ManagerConfig
	MemoryConfig      configs.MemoryConfig
	ReflectionConfig  configs.ReflectionConfig
	RunLabel          string
	SandboxDir        string
	ManagerTTL        time.Duration
}

type scopeState struct {
	Scope      string
	Slug       string
	ScopeKind  string
	Channel    string
	User       string
	Workstream string
	Summary    string
	Keywords   []string
	Recent     []string
	UpdatedAt  time.Time
}

type scopeCandidate struct {
	Scope      string `json:"scope"`
	Workstream string `json:"workstream,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Score      int    `json:"score"`
}

type scopeResolution struct {
	Scope      string           `json:"scope"`
	Decision   string           `json:"decision"`
	Reason     string           `json:"reason,omitempty"`
	Score      int              `json:"score,omitempty"`
	Threshold  int              `json:"threshold,omitempty"`
	Candidates []scopeCandidate `json:"candidates,omitempty"`
}

type scopeIndexFile struct {
	Scopes []scopeState `json:"scopes"`
}

// Registry multiplexes many team managers and reuses them by scope/workstream.
type Registry struct {
	cfg RegistryConfig

	mu             sync.Mutex
	managers       map[string]*Manager
	managerAccess  map[string]time.Time
	states         map[string]*scopeState
	sessionBinding map[string]string
	templates      map[string]AgentTemplate
}

// NewRegistry returns an empty scoped team registry.
func NewRegistry(cfg RegistryConfig) *Registry {
	r := &Registry{
		cfg:            cfg,
		managers:       make(map[string]*Manager),
		managerAccess:  make(map[string]time.Time),
		states:         make(map[string]*scopeState),
		sessionBinding: make(map[string]string),
		templates:      make(map[string]AgentTemplate),
	}
	if err := r.loadIndex(); err != nil && cfg.BaseManagerConfig.Observer != nil {
		cfg.BaseManagerConfig.Observer.Warn("team registry: failed to load persisted scope index", "err", err)
	}
	return r
}

// HandleRequest satisfies the gateway supervisor interface without session metadata.
func (r *Registry) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	return r.HandleScopedRequest(ctx, &gateway.Session{ID: sessionID}, input)
}

// HandleScopedRequest routes the request to a scope-specific team manager.
func (r *Registry) HandleScopedRequest(ctx context.Context, session *gateway.Session, input string) (string, error) {
	if session == nil {
		session = &gateway.Session{}
	}
	scope, resolution := r.resolveScope(session, input)
	r.reapIdleManagers(ctx, scope)
	manager, err := r.getOrCreateManager(ctx, scope)
	if err != nil {
		return "", err
	}
	r.bindSession(scope, session)
	r.recordScope(scope, session, input)
	r.attachResolution(session, resolution)
	r.logResolution(session, input, resolution)
	ctx = gatewaymw.WithScopeDecision(ctx, gatewaymw.ScopeDecision{
		Scope:      resolution.Scope,
		Workstream: r.scopeWorkstream(scope),
		Decision:   resolution.Decision,
		Reason:     resolution.Reason,
		Score:      resolution.Score,
		Threshold:  resolution.Threshold,
		Candidates: toGatewayScopeCandidates(resolution.Candidates),
	})
	return manager.HandleRequest(ctx, session.ID, input)
}

// RegisterTemplate stores the template and applies it to all existing scoped teams.
func (r *Registry) RegisterTemplate(role string, tmpl AgentTemplate) {
	r.mu.Lock()
	r.templates[role] = tmpl
	managers := make([]*Manager, 0, len(r.managers))
	for _, mgr := range r.managers {
		managers = append(managers, mgr)
	}
	r.mu.Unlock()
	for _, mgr := range managers {
		mgr.RegisterTemplate(role, tmpl)
	}
}

// Shutdown stops all scoped team managers.
func (r *Registry) Shutdown(ctx context.Context) {
	r.mu.Lock()
	managers := make([]*Manager, 0, len(r.managers))
	for _, mgr := range r.managers {
		managers = append(managers, mgr)
	}
	r.mu.Unlock()
	for _, mgr := range managers {
		mgr.Shutdown(ctx)
	}
}

func (r *Registry) resolveScope(session *gateway.Session, input string) (string, scopeResolution) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if session != nil && session.ID != "" {
		if scope := strings.TrimSpace(r.sessionBinding[session.ID]); scope != "" {
			return scope, scopeResolution{
				Scope:    scope,
				Decision: "session_binding",
				Reason:   "existing session-to-scope binding",
			}
		}
	}
	if session != nil {
		if explicit := normalizeScopeKey(session.Scope); explicit != "" {
			return explicit, scopeResolution{
				Scope:    explicit,
				Decision: "explicit_scope",
				Reason:   "session provided an explicit scope",
			}
		}
		if workstream := strings.TrimSpace(session.Workstream); workstream != "" {
			scope := buildWorkstreamScope(session.Channel, session.User, workstream)
			return scope, scopeResolution{
				Scope:    scope,
				Decision: "explicit_workstream",
				Reason:   "session provided an explicit workstream",
			}
		}
	}
	if looksLikeContinuation(input) {
		if scope, match := r.bestMatchingScopeLocked(session, input); scope != "" {
			return scope, match
		}
	}
	if session != nil && session.ID != "" {
		scope := "session:" + session.ID
		return scope, scopeResolution{
			Scope:    scope,
			Decision: "new_session_scope",
			Reason:   "no reusable scope matched; isolated to current session",
		}
	}
	scope := fmt.Sprintf("scope:%d", time.Now().UTC().UnixNano())
	return scope, scopeResolution{
		Scope:    scope,
		Decision: "new_generated_scope",
		Reason:   "generated a fresh scope because no session id was available",
	}
}

func (r *Registry) bestMatchingScopeLocked(session *gateway.Session, input string) (string, scopeResolution) {
	if session == nil {
		return "", scopeResolution{}
	}
	var candidates []*scopeState
	for _, state := range r.states {
		if state == nil {
			continue
		}
		if session.User != "" && state.User != "" && state.User != session.User {
			continue
		}
		if session.Channel != "" && state.Channel != "" && state.Channel != session.Channel {
			continue
		}
		if time.Since(state.UpdatedAt) > 7*24*time.Hour {
			continue
		}
		candidates = append(candidates, state)
	}
	if len(candidates) == 1 {
		return candidates[0].Scope, scopeResolution{
			Scope:     candidates[0].Scope,
			Decision:  "continuation_match",
			Reason:    "single recent candidate matched the continuation query",
			Score:     weightedOverlapScore(input, candidates[0].Workstream+" "+candidates[0].Summary+" "+strings.Join(candidates[0].Keywords, " ")),
			Threshold: scopeMatchThreshold,
			Candidates: []scopeCandidate{{
				Scope:      candidates[0].Scope,
				Workstream: candidates[0].Workstream,
				Summary:    candidates[0].Summary,
				Score:      weightedOverlapScore(input, candidates[0].Workstream+" "+candidates[0].Summary+" "+strings.Join(candidates[0].Keywords, " ")),
			}},
		}
	}
	bestScope := ""
	bestScore := 0
	ranked := make([]scopeCandidate, 0, len(candidates))
	for _, state := range candidates {
		score := weightedOverlapScore(input, state.Workstream+" "+state.Summary+" "+strings.Join(state.Keywords, " "))
		if !state.UpdatedAt.IsZero() {
			switch {
			case time.Since(state.UpdatedAt) <= 24*time.Hour:
				score += 3
			case time.Since(state.UpdatedAt) <= 72*time.Hour:
				score += 1
			}
		}
		if score > bestScore {
			bestScore = score
			bestScope = state.Scope
		}
		ranked = append(ranked, scopeCandidate{
			Scope:      state.Scope,
			Workstream: state.Workstream,
			Summary:    state.Summary,
			Score:      score,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Scope < ranked[j].Scope
	})
	if len(ranked) > 3 {
		ranked = ranked[:3]
	}
	if bestScore >= scopeMatchThreshold {
		return bestScope, scopeResolution{
			Scope:      bestScope,
			Decision:   "continuation_match",
			Reason:     "continuation cue + summary retrieval exceeded the reuse threshold",
			Score:      bestScore,
			Threshold:  scopeMatchThreshold,
			Candidates: ranked,
		}
	}
	return "", scopeResolution{
		Decision:   "continuation_rejected",
		Reason:     "continuation cue detected but no candidate exceeded the reuse threshold",
		Score:      bestScore,
		Threshold:  scopeMatchThreshold,
		Candidates: ranked,
	}
}

func (r *Registry) bindSession(scope string, session *gateway.Session) {
	if session == nil || strings.TrimSpace(scope) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if session.ID != "" {
		r.sessionBinding[session.ID] = scope
	}
	session.Scope = scope
	if session.Metadata == nil {
		session.Metadata = make(map[string]interface{})
	}
	session.Metadata["resolved_scope"] = scope
}

func (r *Registry) attachResolution(session *gateway.Session, resolution scopeResolution) {
	if session == nil {
		return
	}
	if session.Metadata == nil {
		session.Metadata = make(map[string]interface{})
	}
	session.Metadata["scope_resolution"] = resolution
}

func (r *Registry) recordScope(scope string, session *gateway.Session, input string) {
	if strings.TrimSpace(scope) == "" {
		return
	}
	trimmed := strings.TrimSpace(input)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.states[scope]
	if state == nil {
		state = &scopeState{Scope: scope}
		r.states[scope] = state
	}
	if session != nil {
		if strings.TrimSpace(session.Channel) != "" {
			state.Channel = session.Channel
		}
		if strings.TrimSpace(session.User) != "" {
			state.User = session.User
		}
		if strings.TrimSpace(session.Workstream) != "" {
			state.Workstream = session.Workstream
		}
	}
	if state.Workstream == "" {
		state.Workstream = deriveWorkstreamTitle(trimmed)
	}
	if trimmed != "" {
		state.Recent = append(state.Recent, trimmed)
		if len(state.Recent) > recentInputLimit {
			state.Recent = append([]string(nil), state.Recent[len(state.Recent)-recentInputLimit:]...)
		}
	}
	state.refreshDerivedFields()
	state.UpdatedAt = time.Now().UTC()
	if err := r.saveIndexLocked(); err != nil && r.cfg.BaseManagerConfig.Observer != nil {
		r.cfg.BaseManagerConfig.Observer.Warn("team registry: failed to persist scope index", "scope", scope, "err", err)
	}
}

func (r *Registry) getOrCreateManager(ctx context.Context, scope string) (*Manager, error) {
	r.mu.Lock()
	if mgr := r.managers[scope]; mgr != nil {
		r.managerAccess[scope] = time.Now().UTC()
		r.mu.Unlock()
		return mgr, nil
	}
	scopeDir := r.scopeDir(scope)
	deps, err := r.buildScopedDeps(scopeDir)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	managerCfg := r.cfg.BaseManagerConfig
	managerCfg.TeamDir = scopeDir
	managerCfg.Deps = deps
	managerCfg.Runtime = nil
	if managerCfg.Model != nil {
		scopeRunLabel := strings.TrimSpace(r.cfg.RunLabel)
		if scopeRunLabel != "" {
			scopeRunLabel += "/" + slugify(scope)
		} else {
			scopeRunLabel = slugify(scope)
		}
		rt, rtErr := NewRuntime(
			deps,
			managerCfg.Model,
			managerCfg.Observer,
			scopeRunLabel,
			r.cfg.SandboxDir,
			scopedReflectionConfig(r.cfg.ReflectionConfig, scopeDir),
		)
		if rtErr != nil {
			r.mu.Unlock()
			return nil, rtErr
		}
		managerCfg.Runtime = rt
	}
	mgr, err := NewManager(ctx, managerCfg)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	for role, tmpl := range r.templates {
		mgr.RegisterTemplate(role, tmpl)
	}
	r.managers[scope] = mgr
	r.managerAccess[scope] = time.Now().UTC()
	r.mu.Unlock()
	return mgr, nil
}

func (r *Registry) buildScopedDeps(scopeDir string) (*core.AgentDependencies, error) {
	if r.cfg.BaseManagerConfig.Deps == nil {
		return nil, nil
	}
	cloned := *r.cfg.BaseManagerConfig.Deps
	memCfg := r.cfg.MemoryConfig
	memCfg.SemanticFile = filepath.Join(scopeDir, "memory", "semantic.yaml")
	memMgr, err := memory.NewManager(memCfg)
	if err != nil {
		return nil, fmt.Errorf("team/registry: scoped memory: %w", err)
	}
	cloned.MemManager = memMgr
	return &cloned, nil
}

// DebugScopes exposes persisted scope/workstream summaries for debug endpoints.
func (r *Registry) DebugScopes() []gateway.ScopeDebugInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]gateway.ScopeDebugInfo, 0, len(r.states))
	for _, state := range r.snapshotStatesLocked() {
		teamDir := r.scopeDir(state.Scope)
		info := gateway.ScopeDebugInfo{
			Scope:             state.Scope,
			ScopeKind:         state.ScopeKind,
			StorageBucket:     scopeStorageBucket(state.Slug),
			Lifecycle:         scopeLifecycle(state.UpdatedAt, r.managers[state.Scope] != nil),
			Channel:           state.Channel,
			User:              state.User,
			Workstream:        state.Workstream,
			Summary:           state.Summary,
			Keywords:          append([]string(nil), state.Keywords...),
			Recent:            append([]string(nil), state.Recent...),
			UpdatedAt:         state.UpdatedAt,
			ManagerRunning:    r.managers[state.Scope] != nil,
			ManagerLastUsedAt: r.managerAccess[state.Scope],
			TeamDir:           teamDir,
		}
		out = append(out, info)
	}
	return out
}

func (r *Registry) scopeWorkstream(scope string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.states[scope]; state != nil {
		return state.Workstream
	}
	return ""
}

func scopedReflectionConfig(cfg configs.ReflectionConfig, scopeDir string) configs.ReflectionConfig {
	next := cfg
	if !next.Enabled {
		return next
	}
	next.MemoryFile = filepath.Join(scopeDir, "memory", "reflections.yaml")
	return next
}

func buildWorkstreamScope(channel, user, workstream string) string {
	parts := []string{"workstream"}
	if s := slugify(channel); s != "" {
		parts = append(parts, s)
	}
	if s := slugify(user); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, slugify(workstream))
	return strings.Join(parts, ":")
}

func normalizeScopeKey(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	return scope
}

func deriveWorkstreamTitle(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if idx := strings.IndexByte(input, '\n'); idx >= 0 {
		input = input[:idx]
	}
	runes := []rune(input)
	if len(runes) > 80 {
		return strings.TrimSpace(string(runes[:80]))
	}
	return input
}

func (r *Registry) logResolution(session *gateway.Session, input string, resolution scopeResolution) {
	if r == nil || r.cfg.BaseManagerConfig.Observer == nil {
		return
	}
	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}
	r.cfg.BaseManagerConfig.Observer.Info(
		"team scope resolved",
		"session_id", sessionID,
		"scope", resolution.Scope,
		"decision", resolution.Decision,
		"reason", resolution.Reason,
		"score", resolution.Score,
		"threshold", resolution.Threshold,
		"candidate_count", len(resolution.Candidates),
		"input", deriveWorkstreamTitle(input),
	)
}

func toGatewayScopeCandidates(in []scopeCandidate) []gatewaymw.ScopeDecisionCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]gatewaymw.ScopeDecisionCandidate, 0, len(in))
	for _, item := range in {
		out = append(out, gatewaymw.ScopeDecisionCandidate{
			Scope:      item.Scope,
			Workstream: item.Workstream,
			Summary:    item.Summary,
			Score:      item.Score,
		})
	}
	return out
}

func looksLikeContinuation(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	cues := []string{
		"继续", "接着", "刚才", "昨天", "上次", "之前", "顺着", "沿着", "那个",
		"continue", "resume", "follow up", "follow-up", "pick up where we left off", "previous",
	}
	for _, cue := range cues {
		if strings.Contains(text, cue) {
			return true
		}
	}
	return false
}

func weightedOverlapScore(a, b string) int {
	ta := lexicalTokens(a)
	tb := lexicalTokens(b)
	score := 0
	for token := range ta {
		if _, ok := tb[token]; !ok {
			continue
		}
		switch {
		case len([]rune(token)) >= 10:
			score += 4
		case len([]rune(token)) >= 6:
			score += 2
		default:
			score++
		}
	}
	return score
}

func (s *scopeState) refreshDerivedFields() {
	if s == nil {
		return
	}
	s.Scope = normalizeScopeKey(s.Scope)
	s.Slug = slugify(s.Scope)
	s.ScopeKind = detectScopeKind(s.Scope)
	if strings.TrimSpace(s.Workstream) == "" && len(s.Recent) > 0 {
		s.Workstream = deriveWorkstreamTitle(s.Recent[0])
	}
	var summaryParts []string
	if strings.TrimSpace(s.Workstream) != "" {
		summaryParts = append(summaryParts, strings.TrimSpace(s.Workstream))
	}
	if n := len(s.Recent); n > 0 {
		if n == 1 {
			summaryParts = append(summaryParts, strings.TrimSpace(s.Recent[0]))
		} else {
			summaryParts = append(summaryParts, strings.TrimSpace(s.Recent[n-2]))
			summaryParts = append(summaryParts, strings.TrimSpace(s.Recent[n-1]))
		}
	}
	summary := strings.Join(summaryParts, " | ")
	summary = strings.TrimSpace(summary)
	if len([]rune(summary)) > 240 {
		summary = string([]rune(summary)[:240])
	}
	s.Summary = summary
	s.Keywords = topKeywords(summary, 8)
}

func topKeywords(text string, limit int) []string {
	tokens := lexicalTokens(text)
	if len(tokens) == 0 || limit <= 0 {
		return nil
	}
	list := make([]string, 0, len(tokens))
	for token := range tokens {
		if len([]rune(token)) < 2 {
			continue
		}
		list = append(list, token)
	}
	sort.Slice(list, func(i, j int) bool {
		li := len([]rune(list[i]))
		lj := len([]rune(list[j]))
		if li != lj {
			return li > lj
		}
		return list[i] < list[j]
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

func (r *Registry) indexFilePath() string {
	base := strings.TrimSpace(r.cfg.BaseManagerConfig.TeamDir)
	if base == "" {
		base = ".team"
	}
	return filepath.Join(base, "index", "scopes.json")
}

func (r *Registry) loadIndex() error {
	path := r.indexFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload scopeIndexFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range payload.Scopes {
		state := payload.Scopes[i]
		state.Scope = normalizeScopeKey(state.Scope)
		if state.Scope == "" {
			continue
		}
		state.refreshDerivedFields()
		copied := state
		r.states[state.Scope] = &copied
	}
	return nil
}

func (r *Registry) saveIndexLocked() error {
	payload := scopeIndexFile{Scopes: r.snapshotStatesLocked()}
	return utils.WriteJSON(r.indexFilePath(), payload)
}

func (r *Registry) snapshotStatesLocked() []scopeState {
	out := make([]scopeState, 0, len(r.states))
	for _, state := range r.states {
		if state == nil || strings.TrimSpace(state.Scope) == "" {
			continue
		}
		copied := *state
		copied.Recent = append([]string(nil), state.Recent...)
		copied.Keywords = append([]string(nil), state.Keywords...)
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].Scope < out[j].Scope
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func lexicalTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		token := strings.ToLower(string(current))
		out[token] = struct{}{}
		// Add short bigrams for CJK-heavy tokens to make continuation matching less brittle.
		if containsHan(token) {
			runes := []rune(token)
			for i := 0; i+1 < len(runes); i++ {
				out[string(runes[i:i+2])] = struct{}{}
			}
		}
		current = current[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func slugify(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "default"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	if len(out) > 80 {
		return out[:80]
	}
	return out
}

func (r *Registry) scopeDir(scope string) string {
	legacy := r.legacyScopeDir(scope)
	grouped := r.groupedScopeDir(scope)
	if pathExists(legacy) && !pathExists(grouped) {
		return legacy
	}
	return grouped
}

func (r *Registry) legacyScopeDir(scope string) string {
	return filepath.Join(r.scopeRootDir(), slugify(scope))
}

func (r *Registry) groupedScopeDir(scope string) string {
	slug := slugify(scope)
	return filepath.Join(r.scopeRootDir(), detectScopeKind(scope), scopeStorageBucket(slug), slug)
}

func (r *Registry) scopeRootDir() string {
	base := strings.TrimSpace(r.cfg.BaseManagerConfig.TeamDir)
	if base == "" {
		base = ".team"
	}
	return filepath.Join(base, "scopes")
}

func detectScopeKind(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "custom"
	}
	if idx := strings.Index(scope, ":"); idx > 0 {
		if kind := slugify(scope[:idx]); kind != "" && kind != "default" {
			return kind
		}
	}
	return "custom"
}

func scopeStorageBucket(slug string) string {
	slug = strings.TrimSpace(slug)
	switch len(slug) {
	case 0:
		return "de"
	case 1:
		return slug + "0"
	default:
		return slug[:2]
	}
}

func scopeLifecycle(updatedAt time.Time, managerRunning bool) string {
	if managerRunning {
		return "active"
	}
	if updatedAt.IsZero() {
		return "unknown"
	}
	age := time.Since(updatedAt)
	switch {
	case age <= 6*time.Hour:
		return "warm"
	case age <= 72*time.Hour:
		return "cool"
	default:
		return "cold"
	}
}

func (r *Registry) managerTTL() time.Duration {
	if r == nil {
		return 0
	}
	if r.cfg.ManagerTTL > 0 {
		return r.cfg.ManagerTTL
	}
	if r.cfg.BaseManagerConfig.IdleTimeout > 0 {
		ttl := 2 * r.cfg.BaseManagerConfig.IdleTimeout
		if ttl < 15*time.Minute {
			return 15 * time.Minute
		}
		return ttl
	}
	return 45 * time.Minute
}

func (r *Registry) reapIdleManagers(ctx context.Context, keepScope string) {
	ttl := r.managerTTL()
	if ttl <= 0 {
		return
	}
	type idleManager struct {
		scope string
		mgr   *Manager
	}
	now := time.Now().UTC()
	var idle []idleManager

	r.mu.Lock()
	for scope, mgr := range r.managers {
		if mgr == nil || scope == keepScope {
			continue
		}
		lastUsed := r.managerAccess[scope]
		if lastUsed.IsZero() || now.Sub(lastUsed) < ttl {
			continue
		}
		idle = append(idle, idleManager{scope: scope, mgr: mgr})
		delete(r.managers, scope)
		delete(r.managerAccess, scope)
	}
	r.mu.Unlock()

	for _, item := range idle {
		item.mgr.Shutdown(ctx)
		if r.cfg.BaseManagerConfig.Observer != nil {
			r.cfg.BaseManagerConfig.Observer.Info(
				"team registry evicted idle scoped manager",
				"scope", item.scope,
				"ttl", ttl.String(),
			)
		}
	}
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
