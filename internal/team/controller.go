package team

import (
	"context"
	"fmt"
	"sort"

	"github.com/rainea/nexus/internal/gateway"
)

// TeamController implementation: exposes scoped team inspection and management
// to the gateway so the desktop UI can visualize the roster and spawn/shutdown
// persistent teammates without going through the lead's LLM-driven tools.
//
// Lifecycle note: teammates and the lead are long-lived goroutines whose
// context must outlive any single HTTP request. Normal chat requests arrive
// through the gateway lane (app-scoped) context; the HTTP handlers here pass a
// background context for team/teammate lifecycle instead, mirroring that
// lifetime and avoiding the request context cancelling freshly spawned workers.

// ResolveScope returns the scope key for a session (creating a stable
// session-scoped key if no explicit scope/workstream is set).
func (r *Registry) ResolveScope(session *gateway.Session) string {
	if session == nil {
		session = &gateway.Session{}
	}
	scope, _ := r.resolveScope(session, "")
	return scope
}

func (r *Registry) resolveManagerForScope(ctx context.Context, scope string) (*Manager, error) {
	if scope == "" {
		return nil, fmt.Errorf("team: scope required")
	}
	return r.getOrCreateManager(ctx, scope)
}

func (r *Registry) existingManagerForScope(scope string) (*Manager, bool) {
	r.mu.Lock()
	mgr := r.managers[scope]
	r.mu.Unlock()
	return mgr, mgr != nil
}

func (r *Registry) roleNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	roles := make([]string, 0, len(r.templates))
	for role := range r.templates {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// TeamInfoByScope returns the roster + role templates for a scope.
func (r *Registry) TeamInfoByScope(scope string) gateway.TeamInfo {
	ctx := context.Background()
	mgr, err := r.resolveManagerForScope(ctx, scope)
	roles := r.roleNames()
	if err != nil || mgr == nil {
		return gateway.TeamInfo{Scope: scope, Roles: roles}
	}

	members := mgr.ListTeammates()
	infos := make([]gateway.TeamMemberInfo, 0, len(members))
	for _, m := range members {
		infos = append(infos, gateway.TeamMemberInfo{
			Name:          m.Name,
			Role:          m.Role,
			Status:        m.Status,
			Activity:      m.Activity,
			ClaimedTaskID: m.ClaimedTaskID,
			UpdatedAt:     m.UpdatedAt,
		})
	}

	teamName := "default"
	if roster := mgr.Roster(); roster != nil {
		teamName = roster.TeamName()
	}

	return gateway.TeamInfo{
		Scope:    scope,
		TeamName: teamName,
		Members:  infos,
		Roles:    roles,
	}
}

// SpawnTeammate creates and starts a persistent teammate in the given scope.
func (r *Registry) SpawnTeammate(ctx context.Context, scope, name, role, prompt string) error {
	mgr, err := r.resolveManagerForScope(context.Background(), scope)
	if err != nil {
		return err
	}
	return mgr.Spawn(context.Background(), name, role, prompt)
}

// ShutdownTeammate requests graceful shutdown of a persistent teammate.
func (r *Registry) ShutdownTeammate(ctx context.Context, scope, name string) error {
	mgr, ok := r.existingManagerForScope(scope)
	if !ok || mgr == nil {
		return fmt.Errorf("team: team not initialized for scope %q", scope)
	}
	return mgr.ShutdownTeammate(name)
}
