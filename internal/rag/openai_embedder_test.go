package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedder(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"embedding": []float64{0.1, 0.2, 0.3}}},
		})
	}))
	defer ts.Close()

	e := NewOpenAIEmbedder("test-key", ts.URL, "text-embedding-3-small", 3)
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Fatalf("vec = %v", vec)
	}
	if e.Dimensions() != 3 {
		t.Fatalf("dimensions = %d, want 3", e.Dimensions())
	}
}

func TestOpenAIEmbedderMissingKey(t *testing.T) {
	e := NewOpenAIEmbedder("", "http://example.com", "m", 3)
	if _, err := e.Embed(context.Background(), "hi"); err == nil {
		t.Fatalf("expected error for empty api key")
	}
}

func TestOpenAIEmbedderEmptyText(t *testing.T) {
	e := NewOpenAIEmbedder("k", "http://example.com", "m", 3)
	if _, err := e.Embed(context.Background(), "  "); err == nil {
		t.Fatalf("expected error for empty text")
	}
}

func TestEmbeddingsURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "https://api.openai.com/v1/embeddings"},
		{"https://api.openai.com/v1/embeddings", "https://api.openai.com/v1/embeddings"},
		{"https://open.bigmodel.cn/api/paas/v4/", "https://open.bigmodel.cn/api/paas/v4/embeddings"},
		{"https://example.com/v1", "https://example.com/v1/embeddings"},
		{"https://example.com", "https://example.com/v1/embeddings"},
	}
	for _, c := range cases {
		if got := embeddingsURL(c.in); got != c.want {
			t.Errorf("embeddingsURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
