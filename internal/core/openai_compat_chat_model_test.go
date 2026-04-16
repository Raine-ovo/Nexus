package core

import (
	"context"
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
