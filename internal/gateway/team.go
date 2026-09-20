package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// TeamMemberInfo is the API view of one persistent team member (roster entry).
type TeamMemberInfo struct {
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	Activity      string    `json:"activity,omitempty"`
	ClaimedTaskID int       `json:"claimed_task_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// TeamInfo is the API view of a scope's team: its roster plus the role
// templates available for spawning new teammates.
type TeamInfo struct {
	Scope    string           `json:"scope"`
	TeamName string           `json:"team_name"`
	Members  []TeamMemberInfo `json:"members"`
	Roles    []string         `json:"roles"`
}

// TeamController is implemented by the team supervisor so the desktop UI can
// inspect and manage any scope's team by scope key.
type TeamController interface {
	ResolveScope(session *Session) string
	TeamInfoByScope(scope string) TeamInfo
	SpawnTeammate(ctx context.Context, scope, name, role, prompt string) error
	ShutdownTeammate(ctx context.Context, scope, name string) error
}

func (g *Gateway) teamController() (TeamController, bool) {
	c, ok := g.supervisor.(TeamController)
	return c, ok
}

// teamScopeFromRequest resolves a scope from either an explicit scope param or
// a session_id. It reports false on validation failure.
func (g *Gateway) teamScopeFromRequest(w http.ResponseWriter, r *http.Request, sessionID, scope string) (string, bool) {
	scope = strings.TrimSpace(scope)
	if scope != "" {
		return scope, true
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "scope or session_id required"})
		return "", false
	}
	sess, ok := g.sessions.Get(sessionID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown session"})
		return "", false
	}
	ctrl, ok := g.teamController()
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "team control unavailable"})
		return "", false
	}
	return ctrl.ResolveScope(sess), true
}

func (g *Gateway) handleTeamInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctrl, ok := g.teamController()
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "team control unavailable"})
		return
	}
	scope, ok := g.teamScopeFromRequest(w, r, r.URL.Query().Get("session_id"), r.URL.Query().Get("scope"))
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ctrl.TeamInfoByScope(scope))
}

type spawnTeammateReq struct {
	SessionID string `json:"session_id"`
	Scope     string `json:"scope"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Prompt    string `json:"prompt"`
}

func (g *Gateway) handleSpawnTeammate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctrl, ok := g.teamController()
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "team control unavailable"})
		return
	}
	var req spawnTeammateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
		return
	}
	scope, ok := g.teamScopeFromRequest(w, r, req.SessionID, req.Scope)
	if !ok {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Role = strings.TrimSpace(req.Role)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Name == "" || req.Role == "" || req.Prompt == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "name, role, and prompt required"})
		return
	}
	if err := ctrl.SpawnTeammate(r.Context(), scope, req.Name, req.Role, req.Prompt); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "spawned", "name": req.Name, "role": req.Role})
}

func (g *Gateway) handleShutdownTeammate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctrl, ok := g.teamController()
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "team control unavailable"})
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "teammate name required"})
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
		Scope     string `json:"scope"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	scope, ok := g.teamScopeFromRequest(w, r, body.SessionID, body.Scope)
	if !ok {
		return
	}
	if err := ctrl.ShutdownTeammate(r.Context(), scope, name); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutdown_requested", "name": name})
}
