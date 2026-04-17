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

func TestGateway_DebugEndpoints(t *testing.T) {
	obs := stubDebugObserver{
		metrics: map[string]interface{}{"counters": map[string]int64{"llm_calls_active": 0}},
		runMetrics: map[string]map[string]interface{}{
			"governance": {"counters": map[string]int64{"llm_calls_active": 2, "tools_active": 1}},
		},
		traces: []observability.TraceSummary{{
			TraceID:   "trace-1",
			Operation: "lead:request",
			SpanCount: 2,
			RunLabel:  "governance",
			RequestID: "req-1",
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
					"sandbox_run": "governance",
					"request_id":  "req-1",
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

	dashboardReq := httptest.NewRequest(http.MethodGet, "/debug/dashboard?run=governance", nil)
	dashboardRec := httptest.NewRecorder()
	g.handleDebugDashboard(dashboardRec, dashboardReq)
	if dashboardRec.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d body=%s", dashboardRec.Code, dashboardRec.Body.String())
	}
	if !strings.Contains(dashboardRec.Body.String(), "Nexus Debug Dashboard") || !strings.Contains(dashboardRec.Body.String(), "governance") {
		t.Fatalf("unexpected dashboard body: %s", dashboardRec.Body.String())
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
