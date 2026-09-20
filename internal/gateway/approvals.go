package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (g *Gateway) requireApprovals(w http.ResponseWriter) bool {
	if g.approvals == nil {
		http.Error(w, `{"error":"approval center disabled"}`, http.StatusNotImplemented)
		return false
	}
	return true
}

func (g *Gateway) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if !g.requireApprovals(w) {
		return
	}
	pendingOnly := !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("all")), "true")
	list := g.approvals.List(pendingOnly)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count":     len(list),
		"pending":   g.approvals.PendingCount(),
		"approvals": list,
	})
}

func (g *Gateway) handleGetApproval(w http.ResponseWriter, r *http.Request) {
	if !g.requireApprovals(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "approval id required"})
		return
	}
	req, ok := g.approvals.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown approval"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(req)
}

func (g *Gateway) handleApproveApproval(w http.ResponseWriter, r *http.Request) {
	if !g.requireApprovals(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "approval id required"})
		return
	}
	var body struct {
		Persist bool `json:"persist"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := g.approvals.Approve(id, body.Persist); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "approved", "id": id})
}

func (g *Gateway) handleDenyApproval(w http.ResponseWriter, r *http.Request) {
	if !g.requireApprovals(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "approval id required"})
		return
	}
	if err := g.approvals.Deny(id); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "denied", "id": id})
}
