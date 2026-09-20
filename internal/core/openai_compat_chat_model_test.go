package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rainea/nexus/configs"
)

func TestOpenAICompatibleChatModel_Retries429ThenSucceeds(t *testing.T) {
	model := NewOpenAICompatibleChatModel(configs.ModelConfig{
		APIKey:    "test-key",
		BaseURL:   "https://example.invalid/v4/",
		ModelName: "glm-5.1",
	})

	attempts := 0
	model.SetHTTPDo(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]
			}`)),
		}, nil
	})

	resp, err := model.Generate(context.Background(), "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestOpenAICompatibleChatModel_StopsAfterRetryBudget(t *testing.T) {
	model := NewOpenAICompatibleChatModel(configs.ModelConfig{
		APIKey:    "test-key",
		BaseURL:   "https://example.invalid/v4/",
		ModelName: "glm-5.1",
	})

	attempts := 0
	model.SetHTTPDo(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		}, nil
	})

	_, err := model.Generate(context.Background(), "system", nil, nil)
	if err == nil {
		t.Fatal("expected error after exhausting retry budget")
	}
	if attempts != maxChatCompletionAttempts {
		t.Fatalf("expected %d attempts, got %d", maxChatCompletionAttempts, attempts)
	}
}

func TestOpenAICompatibleChatModel_StreamingContent(t *testing.T) {
	model := NewOpenAICompatibleChatModel(configs.ModelConfig{
		APIKey:    "test-key",
		BaseURL:   "https://example.invalid/v4/",
		ModelName: "glm-5.1",
	})
	model.SetHTTPDo(func(req *http.Request) (*http.Response, error) {
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["stream"] != true {
			t.Errorf("expected stream:true, got %v", body["stream"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})

	var streamed strings.Builder
	ctx := WithStreamSink(context.Background(), func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	resp, err := model.Generate(ctx, "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" {
		t.Fatalf("content = %q, want Hello", resp.Content)
	}
	if streamed.String() != "Hello" {
		t.Fatalf("streamed = %q, want Hello", streamed.String())
	}
}

func TestOpenAICompatibleChatModel_StreamingToolCalls(t *testing.T) {
	model := NewOpenAICompatibleChatModel(configs.ModelConfig{
		APIKey:    "test-key",
		BaseURL:   "https://example.invalid/v4/",
		ModelName: "glm-5.1",
	})
	model.SetHTTPDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})

	ctx := WithStreamSink(context.Background(), func(delta string) error { return nil })
	resp, err := model.Generate(ctx, "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "get_weather" {
		t.Fatalf("call = %+v", call)
	}
	if call.Arguments["city"] != "Paris" {
		t.Fatalf("args = %+v", call.Arguments)
	}
}
