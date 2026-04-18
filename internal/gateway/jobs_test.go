package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/observability"
	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/internal/tool/mcp"
	"github.com/rainea/nexus/pkg/types"
)

type stubSupervisor struct {
	output string
	err    error
}

func (s stubSupervisor) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	return s.output, s.err
}

type scopedStubSupervisor struct {
	output             string
	lastSessionID      string
	lastScope          string
	lastWorkstream     string
	lastInput          string
	handleRequestCalls int
}

func (s *scopedStubSupervisor) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	s.handleRequestCalls++
	return s.output, nil
}

func (s *scopedStubSupervisor) HandleScopedRequest(ctx context.Context, session *Session, input string) (string, error) {
	if session != nil {
		s.lastSessionID = session.ID
		s.lastScope = session.Scope
		s.lastWorkstream = session.Workstream
	}
	s.lastInput = input
	return s.output, nil
}

func (s *scopedStubSupervisor) DebugScopes() []ScopeDebugInfo {
	return []ScopeDebugInfo{{
		Scope:      "nexus/team",
		Workstream: "TeamRegistry design",
		Summary:    "Continue the scoped team routing design",
		Recent:     []string{"Design TeamRegistry", "继续昨天那个方案"},
	}}
}

type stubDebugObserver struct {
	noopObserver
	metrics    map[string]interface{}
	runMetrics map[string]map[string]interface{}
	traces     []observability.TraceSummary
	spans      map[string][]*observability.Span
}

func (s stubDebugObserver) MetricsSnapshot() map[string]interface{} {
	return s.metrics
}

func (s stubDebugObserver) MetricsSnapshotForRun(runLabel string) map[string]interface{} {
	if m := s.runMetrics[runLabel]; m != nil {
		return m
	}
	return map[string]interface{}{"counters": map[string]int64{}, "histograms": map[string]interface{}{}}
}

func (s stubDebugObserver) ListTraces(limit int) []observability.TraceSummary {
	if limit > 0 && len(s.traces) > limit {
		return s.traces[:limit]
	}
	return s.traces
}

func (s stubDebugObserver) Trace(traceID string) []*observability.Span {
	return s.spans[traceID]
}

func TestJobManager_RunLifecycle(t *testing.T) {
	jm := NewJobManager()
	job := jm.Create("s1", "main", "hello")
	if job.Status != JobPending {
		t.Fatalf("expected pending, got %s", job.Status)
	}
	done := make(chan struct{})
	jm.Run(context.Background(), job.ID, func(ctx context.Context) (string, error) {
		defer close(done)
		return "ok", nil
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not finish")
	}
	got, ok := jm.Get(job.ID)
	if !ok {
		t.Fatal("expected job to exist")
	}
	if got.Status != JobSucceeded || got.Output != "ok" {
		t.Fatalf("unexpected job result: %+v", got)
	}
}

func TestGateway_ChatJobEndpoints(t *testing.T) {
	g := New(configs.GatewayConfig{
		Lanes: map[string]configs.LaneConfig{
			"main": {MaxConcurrency: 1},
		},
	}, configs.ServerConfig{}, stubSupervisor{output: "done"}, nil)
	g.runCtx = context.Background()
	g.lanes.Start(context.Background())
	defer g.lanes.Stop()

	sess := g.sessions.Create("cli", "demo")

	req := httptest.NewRequest(http.MethodPost, "/api/chat/jobs", strings.NewReader(`{"session_id":"`+sess.ID+`","input":"hello","lane":"main"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleCreateChatJob(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	var createResp chatJobCreateResp
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatal(err)
	}
	if createResp.JobID == "" {
		t.Fatal("expected job id")
	}

	var job *ChatJob
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := g.jobs.Get(createResp.JobID); ok {
			job = got
			if job.Status == JobSucceeded {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job == nil || job.Status != JobSucceeded {
		t.Fatalf("expected succeeded job, got %+v", job)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/chat/jobs/"+createResp.JobID, nil)
	getReq.SetPathValue("id", createResp.JobID)
	getRec := httptest.NewRecorder()
	g.handleGetChatJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d body=%s", getRec.Code, getRec.Body.String())
	}

	var got ChatJob
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != JobSucceeded || got.Output != "done" {
		t.Fatalf("unexpected job payload: %+v", got)
	}
}

func TestGateway_CreateSessionAndChatPassScopeMetadata(t *testing.T) {
	sup := &scopedStubSupervisor{output: "done"}
	g := New(configs.GatewayConfig{
		Lanes: map[string]configs.LaneConfig{
			"main": {MaxConcurrency: 1},
		},
	}, configs.ServerConfig{}, sup, nil)
	g.lanes.Start(context.Background())
	defer g.lanes.Stop()

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"channel":"cli","user":"demo","scope":"nexus/team","workstream":"TeamRegistry design"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	g.handleCreateSession(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("unexpected create status: %d body=%s", createRec.Code, createRec.Body.String())
	}
	var sessResp createSessionResp
	if err := json.Unmarshal(createRec.Body.Bytes(), &sessResp); err != nil {
		t.Fatal(err)
	}
	if sessResp.Scope != "nexus/team" || sessResp.Workstream != "TeamRegistry design" {
		t.Fatalf("unexpected session metadata: %+v", sessResp)
	}

	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"session_id":"`+sessResp.SessionID+`","input":"continue the design","lane":"main"}`))
	chatReq.Header.Set("Content-Type", "application/json")
	chatRec := httptest.NewRecorder()
	g.handleChat(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("unexpected chat status: %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if sup.lastSessionID != sessResp.SessionID {
		t.Fatalf("expected session id %s, got %s", sessResp.SessionID, sup.lastSessionID)
	}
	if sup.lastScope != "nexus/team" {
		t.Fatalf("expected scope to be passed through, got %q", sup.lastScope)
	}
	if sup.lastWorkstream != "TeamRegistry design" {
		t.Fatalf("expected workstream to be passed through, got %q", sup.lastWorkstream)
	}
	if sup.handleRequestCalls != 0 {
		t.Fatalf("expected scoped handler to be used, HandleRequest fallback called %d times", sup.handleRequestCalls)
	}
}

func TestGateway_DebugScopesEndpoint(t *testing.T) {
	sup := &scopedStubSupervisor{output: "done"}
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, sup, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/debug/scopes", nil)
	rec := httptest.NewRecorder()
	g.handleDebugScopes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count  int              `json:"count"`
		Scopes []ScopeDebugInfo `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || len(payload.Scopes) != 1 {
		t.Fatalf("unexpected scopes payload: %+v", payload)
	}
	if payload.Scopes[0].Scope != "nexus/team" {
		t.Fatalf("unexpected scope: %+v", payload.Scopes[0])
	}
}

func TestGateway_DebugDashboardIncludesScopesCard(t *testing.T) {
	obs := stubDebugObserver{
		metrics: map[string]interface{}{"counters": map[string]int64{}},
	}
	sup := &scopedStubSupervisor{output: "done"}
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, sup, obs)
	req := httptest.NewRequest(http.MethodGet, "/debug/dashboard", nil)
	rec := httptest.NewRecorder()
	g.handleDebugDashboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Scopes") || !strings.Contains(body, "/api/debug/scopes") {
		t.Fatalf("expected scopes card and link in dashboard, got %s", body)
	}
}

func TestGateway_DebugEndpoints(t *testing.T) {
	obs := stubDebugObserver{
		metrics: map[string]interface{}{"counters": map[string]int64{"llm_calls_active": 0}},
		runMetrics: map[string]map[string]interface{}{
			"governance": {"counters": map[string]int64{"llm_calls_active": 2, "tools_active": 1}},
		},
		traces: []observability.TraceSummary{{
			TraceID:    "trace-1",
			Operation:  "lead:request",
			SpanCount:  2,
			RunLabel:   "governance",
			RequestID:  "req-1",
			Scope:      "nexus/team",
			Workstream: "TeamRegistry design",
			Decision:   "continuation_match",
			Score:      9,
			Threshold:  6,
		}, {
			TraceID:   "trace-2",
			Operation: "lead:request",
			SpanCount: 1,
			RunLabel:  "audit",
			RequestID: "req-2",
		}},
		spans: map[string][]*observability.Span{
			"trace-1": {{
				TraceID:   "trace-1",
				SpanID:    "span-1",
				Operation: "lead:request",
				Status:    "ok",
				Tags: map[string]string{
					"sandbox_run":           "governance",
					"request_id":            "req-1",
					"scope":                 "nexus/team",
					"workstream":            "TeamRegistry design",
					"scope_decision":        "continuation_match",
					"scope_reason":          "summary retrieval exceeded threshold",
					"scope_score":           "9",
					"scope_threshold":       "6",
					"scope_candidates_json": `[{"scope":"nexus/team","workstream":"TeamRegistry design","summary":"Continue scoped team routing","score":9},{"scope":"session:abc","workstream":"Other work","summary":"Lower confidence path","score":3}]`,
				},
			}},
		},
	}
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, stubSupervisor{output: "ok"}, obs)

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/debug/metrics?run=governance", nil)
	metricsRec := httptest.NewRecorder()
	g.handleDebugMetrics(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status: %d body=%s", metricsRec.Code, metricsRec.Body.String())
	}
	if !strings.Contains(metricsRec.Body.String(), "\"scope\":\"run\"") || !strings.Contains(metricsRec.Body.String(), "\"llm_calls_active\":2") {
		t.Fatalf("unexpected metrics body: %s", metricsRec.Body.String())
	}

	tracesReq := httptest.NewRequest(http.MethodGet, "/api/debug/traces?run=governance", nil)
	tracesRec := httptest.NewRecorder()
	g.handleDebugTraces(tracesRec, tracesReq)
	if tracesRec.Code != http.StatusOK {
		t.Fatalf("unexpected traces status: %d body=%s", tracesRec.Code, tracesRec.Body.String())
	}
	if !strings.Contains(tracesRec.Body.String(), "trace-1") || strings.Contains(tracesRec.Body.String(), "trace-2") {
		t.Fatalf("unexpected filtered traces body: %s", tracesRec.Body.String())
	}

	traceReq := httptest.NewRequest(http.MethodGet, "/api/debug/traces/trace-1?run=governance", nil)
	traceReq.SetPathValue("id", "trace-1")
	traceRec := httptest.NewRecorder()
	g.handleDebugTrace(traceRec, traceReq)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("unexpected trace status: %d body=%s", traceRec.Code, traceRec.Body.String())
	}
	if !strings.Contains(traceRec.Body.String(), "\"tree\"") || !strings.Contains(traceRec.Body.String(), "\"grouped\"") {
		t.Fatalf("unexpected trace detail body: %s", traceRec.Body.String())
	}
	if !strings.Contains(traceRec.Body.String(), "\"scope_summary\"") || !strings.Contains(traceRec.Body.String(), "continuation_match") {
		t.Fatalf("expected scope summary in trace detail body: %s", traceRec.Body.String())
	}
	if !strings.Contains(traceRec.Body.String(), "\"score\":9") || !strings.Contains(traceRec.Body.String(), "\"threshold\":6") || !strings.Contains(traceRec.Body.String(), "\"candidates\"") {
		t.Fatalf("expected scope matching quality in trace detail body: %s", traceRec.Body.String())
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/debug/dashboard?run=governance", nil)
	dashboardRec := httptest.NewRecorder()
	g.handleDebugDashboard(dashboardRec, dashboardReq)
	if dashboardRec.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d body=%s", dashboardRec.Code, dashboardRec.Body.String())
	}
	if !strings.Contains(dashboardRec.Body.String(), "Nexus Debug Dashboard") || !strings.Contains(dashboardRec.Body.String(), "governance") {
		t.Fatalf("unexpected dashboard body: %s", dashboardRec.Body.String())
	}
	if !strings.Contains(dashboardRec.Body.String(), "Scope Decision") {
		t.Fatalf("expected scope decision section in dashboard body: %s", dashboardRec.Body.String())
	}
	if !strings.Contains(dashboardRec.Body.String(), "item.scope_decision") || !strings.Contains(dashboardRec.Body.String(), "item.scope_score") || !strings.Contains(dashboardRec.Body.String(), "item.scope_threshold") {
		t.Fatalf("expected trace list scope decision rendering logic in dashboard: %s", dashboardRec.Body.String())
	}
	if !strings.Contains(dashboardRec.Body.String(), "Scope Candidates") || !strings.Contains(dashboardRec.Body.String(), "score-high") || !strings.Contains(dashboardRec.Body.String(), "score-medium") || !strings.Contains(dashboardRec.Body.String(), "score-low") {
		t.Fatalf("expected candidates table and score color classes in dashboard: %s", dashboardRec.Body.String())
	}
}

func TestGateway_PrimaryHandler_AuthAndRateLimit(t *testing.T) {
	g := New(configs.GatewayConfig{
		Auth: configs.GatewayAuthConfig{
			APIKeys: []string{"secret"},
		},
		RateLimit: configs.RateLimitConfig{
			Enabled: true,
			RPS:     1000,
			Burst:   1,
		},
	}, configs.ServerConfig{}, stubSupervisor{output: "ok"}, nil)

	handler := g.wrapPrimaryHandler(g.newPrimaryMux())

	req1 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req1.RemoteAddr = "127.0.0.1:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected public health route, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/debug/traces", nil)
	req2.RemoteAddr = "127.0.0.2:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusUnauthorized {
		t.Fatalf("expected public debug route to bypass auth, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"session_id":"s","input":"hello"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "127.0.0.3:3333"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("expected chat route to remain protected, got %d body=%s", rec3.Code, rec3.Body.String())
	}

	req4 := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"session_id":"s","input":"hello"}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("X-API-Key", "secret")
	req4.RemoteAddr = "127.0.0.4:4444"
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)
	if rec4.Code == http.StatusUnauthorized {
		t.Fatalf("expected chat route to allow valid api key, got %d body=%s", rec4.Code, rec4.Body.String())
	}

	req5 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req5.RemoteAddr = "127.0.0.5:5555"
	rec5 := httptest.NewRecorder()
	handler.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusOK {
		t.Fatalf("expected first health request ok, got %d body=%s", rec5.Code, rec5.Body.String())
	}

	req6 := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req6.RemoteAddr = "127.0.0.5:6666"
	rec6 := httptest.NewRecorder()
	handler.ServeHTTP(rec6, req6)
	if rec6.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limited response, got %d body=%s", rec6.Code, rec6.Body.String())
	}
}

func TestGateway_PrimaryMux_MountsMCP(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(&types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "echo_remote",
			Description: "echo",
			Parameters:  map[string]interface{}{"type": "object"},
		},
		Source: "builtin",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			return &types.ToolResult{Name: "echo_remote", Content: "ok"}, nil
		},
	})
	g := New(configs.GatewayConfig{}, configs.ServerConfig{}, stubSupervisor{output: "ok"}, nil)
	g.SetMCPHandler(mcp.NewServer(reg).Handler())

	mux := g.newPrimaryMux()
	req := httptest.NewRequest(http.MethodPost, "/mcp/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected mcp status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "echo_remote") {
		t.Fatalf("expected mcp tool listing, got %s", rec.Body.String())
	}
}
