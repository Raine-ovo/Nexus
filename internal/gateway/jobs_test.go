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
)

type stubSupervisor struct {
	output string
	err    error
}

func (s stubSupervisor) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	return s.output, s.err
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
