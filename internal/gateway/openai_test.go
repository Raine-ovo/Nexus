package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rainea/nexus/configs"
)

func newOpenAITestGateway(output string) *Gateway {
	g := New(configs.GatewayConfig{
		Lanes: map[string]configs.LaneConfig{
			"main": {MaxConcurrency: 1},
		},
	}, configs.ServerConfig{}, stubSupervisor{output: output}, nil)
	g.runCtx = context.Background()
	g.lanes.Start(context.Background())
	return g
}

func TestOpenAIChatCompletionsNonStreaming(t *testing.T) {
	g := newOpenAITestGateway("hello from nexus")
	defer g.lanes.Stop()
	mux := g.newPrimaryMux()

	body := `{"model":"nexus","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp openAIChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "chat.completion" || len(resp.Choices) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Choices[0].Message.Content != "hello from nexus" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
}

func TestOpenAIChatCompletionsStreaming(t *testing.T) {
	g := newOpenAITestGateway("stream me")
	defer g.lanes.Stop()
	mux := g.newPrimaryMux()

	body := `{"model":"nexus","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "chat.completion.chunk") {
		t.Fatalf("missing chunk object in stream: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("missing [DONE] terminator: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "stream me") {
		t.Fatalf("missing streamed content: %s", rec.Body.String())
	}
}

func TestOpenAIChatCompletionsMissingMessages(t *testing.T) {
	g := newOpenAITestGateway("ok")
	defer g.lanes.Stop()
	mux := g.newPrimaryMux()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nexus","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOpenAITranscript(t *testing.T) {
	msgs := []openAIChatMessage{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "what is Go?"},
		{Role: "assistant", Content: "a language"},
		{Role: "user", Content: "summarize"},
	}
	got := openAITranscript(msgs)
	if !strings.Contains(got, "[system] be concise") {
		t.Fatalf("missing system line: %q", got)
	}
	if !strings.Contains(got, "[assistant] a language") {
		t.Fatalf("missing assistant line: %q", got)
	}
	if !strings.Contains(got, "what is Go?") || !strings.Contains(got, "summarize") {
		t.Fatalf("missing user content: %q", got)
	}
}
