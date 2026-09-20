package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rainea/nexus/internal/core"
)

// handleChatStream exposes a purpose-built SSE endpoint for the web UI. It
// streams true token deltas from the model (when the provider supports it) and
// falls back to chunking the fully-assembled output otherwise.
//
// Request body (JSON): {"session_id":"...","input":"...","lane":"main"}
// Response: text/event-stream with one JSON object per `data:` line:
//
//	{"type":"delta","content":"..."}   // token deltas
//	{"type":"done","output":"...","error":""}  // terminal (error non-empty on failure)
//	{"type":"error","message":"..."}   // runtime failure after streaming started
func (g *Gateway) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "invalid json"})
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Input = strings.TrimSpace(req.Input)
	if req.SessionID == "" || req.Input == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "session_id and input required"})
		return
	}
	sess, ok := g.sessions.Get(req.SessionID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "unknown session"})
		return
	}
	g.sessions.Touch(req.SessionID)

	lane := strings.TrimSpace(req.Lane)
	if lane == "" {
		lane = "main"
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		// No flushing support: fall back to the synchronous path.
		out, err := g.lanes.Submit(r.Context(), lane, func(ctx context.Context) (string, error) {
			return g.runSupervisor(ctx, sess, req.Input)
		})
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(chatResp{Error: err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatResp{Output: out})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeEvent := func(payload map[string]interface{}) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}

	streamed := false
	sink := func(delta string) error {
		streamed = true
		return writeEvent(map[string]interface{}{"type": "delta", "content": delta})
	}
	ctx := core.WithStreamSink(r.Context(), sink)

	out, err := g.lanes.Submit(ctx, lane, func(c context.Context) (string, error) {
		return g.runSupervisor(c, sess, req.Input)
	})

	if err != nil {
		_ = writeEvent(map[string]interface{}{"type": "error", "message": err.Error()})
		return
	}

	// Fallback for models/providers without streaming support.
	if !streamed && out != "" {
		const chunkRunes = 64
		runes := []rune(out)
		for i := 0; i < len(runes); i += chunkRunes {
			j := i + chunkRunes
			if j > len(runes) {
				j = len(runes)
			}
			if werr := writeEvent(map[string]interface{}{"type": "delta", "content": string(runes[i:j])}); werr != nil {
				return
			}
		}
	}

	_ = writeEvent(map[string]interface{}{"type": "done", "output": out, "error": ""})
}

// runSupervisor dispatches a request through the supervisor, preferring the
// scoped variant when available.
func (g *Gateway) runSupervisor(ctx context.Context, sess *Session, input string) (string, error) {
	if scoped, ok := g.supervisor.(ScopedSupervisor); ok {
		return scoped.HandleScopedRequest(ctx, sess, input)
	}
	return g.supervisor.HandleRequest(ctx, sess.ID, input)
}
