package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAIEmbedder implements Embedder against an OpenAI-compatible /embeddings
// endpoint, enabling real semantic embeddings for production RAG.
type OpenAIEmbedder struct {
	apiKey  string
	baseURL string
	model   string
	dim     int
	httpDo  func(*http.Request) (*http.Response, error)
}

// NewOpenAIEmbedder builds an embedder. dim must match the configured vector
// store dimension; 0 falls back to 1536.
func NewOpenAIEmbedder(apiKey, baseURL, model string, dim int) *OpenAIEmbedder {
	if dim <= 0 {
		dim = 1536
	}
	return &OpenAIEmbedder{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimSpace(baseURL),
		model:   strings.TrimSpace(model),
		dim:     dim,
		httpDo:  http.DefaultClient.Do,
	}
}

// Dimensions returns the configured vector size.
func (e *OpenAIEmbedder) Dimensions() int { return e.dim }

// SetHTTPDo replaces the HTTP caller (useful in tests). Nil restores the default.
func (e *OpenAIEmbedder) SetHTTPDo(do func(*http.Request) (*http.Response, error)) {
	if do == nil {
		e.httpDo = http.DefaultClient.Do
		return
	}
	e.httpDo = do
}

// Embed sends text to the embeddings endpoint and returns the first embedding.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if e == nil {
		return nil, fmt.Errorf("rag: nil OpenAIEmbedder")
	}
	if e.apiKey == "" {
		return nil, fmt.Errorf("rag: embeddings api_key is empty")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("rag: cannot embed empty text")
	}
	model := e.model
	if model == "" {
		model = "text-embedding-3-small"
	}

	payload, err := json.Marshal(map[string]interface{}{
		"model": model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("rag: marshal embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsURL(e.baseURL), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("rag: build embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	doer := e.httpDo
	if doer == nil {
		doer = http.DefaultClient.Do
	}
	resp, err := doer(req)
	if err != nil {
		return nil, fmt.Errorf("rag: embedding request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("rag: read embedding response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rag: embedding status %d: %s", resp.StatusCode, truncateBytes(respBody, 512))
	}

	var envelope struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("rag: decode embedding response: %w", err)
	}
	if len(envelope.Data) == 0 || len(envelope.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("rag: empty embedding response")
	}
	return envelope.Data[0].Embedding, nil
}

func embeddingsURL(base string) string {
	b := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if b == "" {
		return "https://api.openai.com/v1/embeddings"
	}
	if strings.HasSuffix(b, "/embeddings") {
		return b
	}
	if strings.HasSuffix(b, "/v1") || strings.HasSuffix(b, "/v4") {
		return b + "/embeddings"
	}
	return b + "/v1/embeddings"
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
