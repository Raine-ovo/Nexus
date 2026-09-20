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

// resolveManagerForSession resolves the session's scope and returns (or lazily
// creates) the scoped team manager.
func (r *Registry) resolveManagerForSession(ctx context.Context, session *gateway.Session) (*Manager, string, error) {
	if session == nil {
		session = &gateway.Session{}
	}
	scope, _ := r.resolveScope(session, "")
	mgr, err := r.getOrCreateManager(ctx, scope)
	if err != nil {
		return nil, scope, err
	}
	return mgr, scope, nil
}

// existingManagerForSession returns the scoped manager only if it already
// exists; it never creates one.
func (r *Registry) existingManagerForSession(session *gateway.Session) (*Manager, string, bool) {
	if session == nil {
		session = &gateway.Session{}
	}
	scope, _ := r.resolveScope(session, "")
	r.mu.Lock()
	mgr := r.managers[scope]
	r.mu.Unlock()
	return mgr, scope, mgr != nil
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

// TeamInfo returns the roster + role templates for the session's scope.
func (r *Registry) TeamInfo(session *gateway.Session) gateway.TeamInfo {
	ctx := context.Background()
	mgr, scope, err := r.resolveManagerForSession(ctx, session)
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

// SpawnTeammate creates and starts a persistent teammate in the session's scope.
func (r *Registry) SpawnTeammate(ctx context.Context, session *gateway.Session, name, role, prompt string) error {
	mgr, _, err := r.resolveManagerForSession(context.Background(), session)
	if err != nil {
		return err
	}
	return mgr.Spawn(context.Background(), name, role, prompt)
}

// ShutdownTeammate requests graceful shutdown of a persistent teammate.
func (r *Registry) ShutdownTeammate(ctx context.Context, session *gateway.Session, name string) error {
	mgr, _, ok := r.existingManagerForSession(session)
	if !ok || mgr == nil {
		return fmt.Errorf("team: team not initialized for this session")
	}
	return mgr.ShutdownTeammate(name)
}
