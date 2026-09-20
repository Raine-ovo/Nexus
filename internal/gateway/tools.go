package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/rainea/nexus/pkg/types"
)

// ToolProvider exposes the registered tool set for the desktop UI's tool list.
type ToolProvider interface {
	ListTools() []*types.ToolMeta
}

// SetToolProvider wires the tool registry into the gateway.
func (g *Gateway) SetToolProvider(p ToolProvider) {
	g.toolProvider = p
}

type toolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	Source      string `json:"source"`
}

// handleListTools returns registered tools (name, description, permission,
// source) for the tool list panel.
func (g *Gateway) handleListTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g.toolProvider == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tool provider unavailable"})
		return
	}
	metas := g.toolProvider.ListTools()
	infos := make([]toolInfo, 0, len(metas))
	for _, m := range metas {
		if m == nil {
			continue
		}
		infos = append(infos, toolInfo{
			Name:        m.Definition.Name,
			Description: m.Definition.Description,
			Permission:  string(m.Permission),
			Source:      m.Source,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count": len(infos),
		"tools": infos,
	})
}
