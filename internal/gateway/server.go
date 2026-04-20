package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/gateway/middleware"
	"github.com/rainea/nexus/internal/observability"
)

// Gateway is the multi-channel entry point.
type Gateway struct {
	cfg        configs.GatewayConfig
	serverCfg  configs.ServerConfig
	supervisor Supervisor
	sessions   *SessionManager
	lanes      *LaneManager
	jobs       *JobManager
	router     *BindingRouter
	observer   Observer
	runCtx     context.Context
	mcpHandler http.Handler
}

// Supervisor is the interface that the orchestrator must implement.
type Supervisor interface {
	HandleRequest(ctx context.Context, sessionID, input string) (string, error)
}

// ScopedSupervisor can use full session metadata when routing a request.
type ScopedSupervisor interface {
	HandleScopedRequest(ctx context.Context, session *Session, input string) (string, error)
}

// ScopeDebugInfo is a user-facing debug view of one persisted workstream scope.
type ScopeDebugInfo struct {
	Scope             string    `json:"scope"`
	ScopeKind         string    `json:"scope_kind,omitempty"`
	StorageBucket     string    `json:"storage_bucket,omitempty"`
	Lifecycle         string    `json:"lifecycle,omitempty"`
	Channel           string    `json:"channel,omitempty"`
	User              string    `json:"user,omitempty"`
	Workstream        string    `json:"workstream,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Keywords          []string  `json:"keywords,omitempty"`
	Recent            []string  `json:"recent,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
	ManagerRunning    bool      `json:"manager_running"`
	ManagerLastUsedAt time.Time `json:"manager_last_used_at,omitempty"`
	TeamDir           string    `json:"team_dir,omitempty"`
}

// ScopeDebugger exposes scoped team/workstream state for debug endpoints.
type ScopeDebugger interface {
	DebugScopes() []ScopeDebugInfo
}

// Observer for logging.
type Observer interface {
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

type debugObserver interface {
	MetricsSnapshot() map[string]interface{}
	MetricsSnapshotForRun(runLabel string) map[string]interface{}
	ListTraces(limit int) []observability.TraceSummary
	Trace(traceID string) []*observability.Span
}

type noopObserver struct{}

func (noopObserver) Info(string, ...interface{})  {}
func (noopObserver) Error(string, ...interface{}) {}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// New constructs a Gateway with default session TTL and lane wiring.
func New(cfg configs.GatewayConfig, serverCfg configs.ServerConfig, sup Supervisor, obs Observer) *Gateway {
	if obs == nil {
		obs = noopObserver{}
	}
	return &Gateway{
		cfg:        cfg,
		serverCfg:  serverCfg,
		supervisor: sup,
		sessions:   NewSessionManager(24 * time.Hour),
		lanes:      NewLaneManager(cfg),
		jobs:       NewJobManager(),
		router:     NewBindingRouter(),
		observer:   obs,
	}
}

// Sessions exposes the session manager for advanced integrations.
func (g *Gateway) Sessions() *SessionManager { return g.sessions }

// Lanes exposes the lane manager.
func (g *Gateway) Lanes() *LaneManager { return g.lanes }

// Router exposes the binding router.
func (g *Gateway) Router() *BindingRouter { return g.router }

// SetMCPHandler mounts optional MCP HTTP endpoints into the primary server mux.
func (g *Gateway) SetMCPHandler(h http.Handler) {
	g.mcpHandler = h
}

// Start runs the HTTP server until ctx is cancelled, then shuts down gracefully.
func (g *Gateway) Start(ctx context.Context) error {
	g.lanes.Start(ctx)
	g.runCtx = ctx

	mux := g.newPrimaryMux()
	handler := g.wrapPrimaryHandler(mux)

	srv := &http.Server{
		Addr:         g.serverCfg.HTTPAddr,
		Handler:      handler,
		ReadTimeout:  g.serverCfg.ReadTimeout,
		WriteTimeout: g.serverCfg.WriteTimeout,
	}
	wsSrv := g.newWebSocketServer()

	errCh := make(chan error, 2)
	go func() {
		g.observer.Info("gateway listening", "addr", g.serverCfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	if wsSrv != nil {
		go func() {
			g.observer.Info("gateway websocket listening", "addr", wsSrv.Addr)
			if err := wsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			g.observer.Error("gateway shutdown error", "err", err)
		}
		if wsSrv != nil {
			if err := wsSrv.Shutdown(shutdownCtx); err != nil {
				g.observer.Error("gateway websocket shutdown error", "err", err)
			}
		}
		g.lanes.Stop()
		return ctx.Err()
	case err := <-errCh:
		if wsSrv != nil {
			_ = wsSrv.Close()
		}
		_ = srv.Close()
		g.lanes.Stop()
		return err
	}
}

func (g *Gateway) newPrimaryMux() *http.ServeMux {
	mux := http.NewServeMux()
	if g.mcpHandler != nil {
		mux.Handle("/mcp/", g.mcpHandler)
	}
	mux.HandleFunc("GET /api/health", g.handleHealth)
	mux.HandleFunc("POST /api/sessions", g.handleCreateSession)
	mux.HandleFunc("POST /api/chat", g.handleChat)
	mux.HandleFunc("POST /api/chat/jobs", g.handleCreateChatJob)
	mux.HandleFunc("GET /api/chat/jobs/{id}", g.handleGetChatJob)
	mux.HandleFunc("GET /api/debug/metrics", g.handleDebugMetrics)
	mux.HandleFunc("GET /api/debug/scopes", g.handleDebugScopes)
	mux.HandleFunc("GET /api/debug/traces", g.handleDebugTraces)
	mux.HandleFunc("GET /api/debug/traces/{id}", g.handleDebugTrace)
	mux.HandleFunc("GET /debug/dashboard", g.handleDebugDashboard)
	mux.HandleFunc("GET /api/ws", g.handleWebSocket)
	return mux
}

func (g *Gateway) wrapPrimaryHandler(mux http.Handler) http.Handler {
	private := middleware.NewAuthWithJWT(g.cfg.Auth.APIKeys, g.cfg.Auth.JWTSecret).Wrap(mux)
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicDebugOrHealthRoute(r) {
			mux.ServeHTTP(w, r)
			return
		}
		private.ServeHTTP(w, r)
	})
	handler = middleware.NewTrace().Wrap(handler)
	rps := g.cfg.RateLimit.RPS
	burst := g.cfg.RateLimit.Burst
	if !g.cfg.RateLimit.Enabled {
		rps = 0
		burst = 0
	}
	handler = middleware.NewRateLimiter(rps, burst).Wrap(handler)
	return handler
}

func isPublicDebugOrHealthRoute(r *http.Request) bool {
	if r == nil {
		return false
	}
	path := r.URL.Path
	switch {
	case path == "/api/health":
		return true
	case path == "/debug/dashboard":
		return true
	case strings.HasPrefix(path, "/api/debug/"):
		return true
	default:
		return false
	}
}

func (g *Gateway) newWebSocketServer() *http.Server {
	addr := strings.TrimSpace(g.serverCfg.WSAddr)
	if addr == "" || addr == strings.TrimSpace(g.serverCfg.HTTPAddr) {
		return nil
	}
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /api/ws", g.handleWebSocket)
	return &http.Server{
		Addr:         addr,
		Handler:      g.wrapPrimaryHandler(wsMux),
		ReadTimeout:  g.serverCfg.ReadTimeout,
		WriteTimeout: g.serverCfg.WriteTimeout,
	}
}

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

type createSessionReq struct {
	Channel    string `json:"channel"`
	User       string `json:"user"`
	Scope      string `json:"scope,omitempty"`
	Workstream string `json:"workstream,omitempty"`
}

type createSessionResp struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Workstream string `json:"workstream,omitempty"`
}

func (g *Gateway) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var req createSessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
		return
	}
	s := g.sessions.CreateWithOptions(
		req.Channel,
		req.User,
		strings.TrimSpace(req.Scope),
		strings.TrimSpace(req.Workstream),
	)
	if aid, ok := g.router.Route(req.Channel, req.User); ok {
		s.AgentID = aid
	}
	_ = json.NewEncoder(w).Encode(createSessionResp{
		SessionID:  s.ID,
		AgentID:    s.AgentID,
		Scope:      s.Scope,
		Workstream: s.Workstream,
	})
}

type chatReq struct {
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	Lane      string `json:"lane"`
}

type chatResp struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

type chatJobCreateResp struct {
	JobID     string    `json:"job_id"`
	Status    JobStatus `json:"status"`
	SessionID string    `json:"session_id"`
	Lane      string    `json:"lane"`
}

func (g *Gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "invalid json"})
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Input = strings.TrimSpace(req.Input)
	if req.SessionID == "" || req.Input == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "session_id and input required"})
		return
	}
	sess, ok := g.sessions.Get(req.SessionID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "unknown session"})
		return
	}
	g.sessions.Touch(req.SessionID)

	lane := strings.TrimSpace(req.Lane)
	if lane == "" {
		lane = "main"
	}

	out, err := g.lanes.Submit(r.Context(), lane, func(ctx context.Context) (string, error) {
		if scoped, ok := g.supervisor.(ScopedSupervisor); ok {
			return scoped.HandleScopedRequest(ctx, sess, req.Input)
		}
		return g.supervisor.HandleRequest(ctx, sess.ID, req.Input)
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(chatResp{Error: err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(chatResp{Output: out})
}

func (g *Gateway) handleCreateChatJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	req, sess, lane, ok := g.parseChatRequest(w, r)
	if !ok {
		return
	}
	job := g.jobs.Create(sess.ID, lane, req.Input)
	baseCtx := g.runCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	g.jobs.Run(baseCtx, job.ID, func(ctx context.Context) (string, error) {
		return g.lanes.Submit(ctx, lane, func(c context.Context) (string, error) {
			if scoped, ok := g.supervisor.(ScopedSupervisor); ok {
				return scoped.HandleScopedRequest(c, sess, req.Input)
			}
			return g.supervisor.HandleRequest(c, sess.ID, req.Input)
		})
	})
	_ = json.NewEncoder(w).Encode(chatJobCreateResp{
		JobID:     job.ID,
		Status:    job.Status,
		SessionID: sess.ID,
		Lane:      lane,
	})
}

func (g *Gateway) handleGetChatJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "job id required"})
		return
	}
	job, ok := g.jobs.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown job"})
		return
	}
	_ = json.NewEncoder(w).Encode(job)
}

func (g *Gateway) parseChatRequest(w http.ResponseWriter, r *http.Request) (chatReq, *Session, string, bool) {
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "invalid json"})
		return chatReq{}, nil, "", false
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Input = strings.TrimSpace(req.Input)
	if req.SessionID == "" || req.Input == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "session_id and input required"})
		return chatReq{}, nil, "", false
	}
	sess, ok := g.sessions.Get(req.SessionID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(chatResp{Error: "unknown session"})
		return chatReq{}, nil, "", false
	}
	g.sessions.Touch(req.SessionID)
	lane := strings.TrimSpace(req.Lane)
	if lane == "" {
		lane = "main"
	}
	return req, sess, lane, true
}

type wsClientMsg struct {
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	Lane      string `json:"lane"`
}

func (g *Gateway) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		g.observer.Error("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg wsClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = conn.WriteJSON(chatResp{Error: "invalid json"})
			continue
		}
		msg.SessionID = strings.TrimSpace(msg.SessionID)
		msg.Input = strings.TrimSpace(msg.Input)
		if msg.SessionID == "" || msg.Input == "" {
			_ = conn.WriteJSON(chatResp{Error: "session_id and input required"})
			continue
		}
		sess, ok := g.sessions.Get(msg.SessionID)
		if !ok {
			_ = conn.WriteJSON(chatResp{Error: "unknown session"})
			continue
		}
		g.sessions.Touch(msg.SessionID)
		lane := strings.TrimSpace(msg.Lane)
		if lane == "" {
			lane = "main"
		}
		ctx, cancel := context.WithTimeout(r.Context(), g.serverCfg.WriteTimeout)
		out, err := g.lanes.Submit(ctx, lane, func(c context.Context) (string, error) {
			if scoped, ok := g.supervisor.(ScopedSupervisor); ok {
				return scoped.HandleScopedRequest(c, sess, msg.Input)
			}
			return g.supervisor.HandleRequest(c, sess.ID, msg.Input)
		})
		cancel()
		if err != nil {
			_ = conn.WriteJSON(chatResp{Error: err.Error()})
			continue
		}
		// Stream large replies as multiple text frames (UTF-8 safe rune chunks).
		const maxRunes = 2048
		runes := []rune(out)
		for i := 0; i < len(runes); i += maxRunes {
			j := i + maxRunes
			if j > len(runes) {
				j = len(runes)
			}
			if werr := conn.WriteMessage(websocket.TextMessage, []byte(string(runes[i:j]))); werr != nil {
				return
			}
		}
	}
}
