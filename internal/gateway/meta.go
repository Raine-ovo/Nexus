package gateway

import (
	"encoding/json"
	"net/http"
)

// Version is the Nexus release version. It may be overridden at build time via
// -ldflags "-X github.com/rainea/nexus/internal/gateway.Version=vX.Y.Z".
var Version = "0.1.0"

// MetaInfo is the non-sensitive server summary exposed to the web UI at
// /api/meta so it can render the model name, permission mode, and feature
// toggles without receiving credentials.
type MetaInfo struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	Model            string `json:"model"`
	Mode             string `json:"mode"`
	ApprovalEnabled  bool   `json:"approval_enabled"`
	WorkspaceRoot    string `json:"workspace_root"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

// SetMeta records server metadata for the /api/meta endpoint.
func (g *Gateway) SetMeta(m MetaInfo) {
	g.meta = m
}

func (g *Gateway) handleMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	meta := g.meta
	if meta.Name == "" {
		meta.Name = "nexus"
	}
	if meta.Version == "" {
		meta.Version = Version
	}
	_ = json.NewEncoder(w).Encode(meta)
}
