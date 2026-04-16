package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ESConfig holds Elasticsearch connection and index settings.
type ESConfig struct {
	Addresses  []string `yaml:"addresses"`
	IndexName  string   `yaml:"index_name"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	Shards     int      `yaml:"shards"`
	Replicas   int      `yaml:"replicas"`
	BM25K1     float64  `yaml:"bm25_k1"`
	BM25B      float64  `yaml:"bm25_b"`
	AutoCreate bool     `yaml:"auto_create"`
}

// ESKeywordStore implements KeywordStore using Elasticsearch with BM25 scoring.
type ESKeywordStore struct {
	cfg    ESConfig
	client *http.Client
	base   string
}

// NewESKeywordStore creates an ES-backed keyword store and optionally ensures the index exists.
func NewESKeywordStore(cfg ESConfig) (*ESKeywordStore, error) {
	if len(cfg.Addresses) == 0 {
		cfg.Addresses = []string{"http://localhost:9200"}
	}
	if cfg.IndexName == "" {
		cfg.IndexName = "nexus_chunks"
	}
	if cfg.Shards <= 0 {
		cfg.Shards = 1
	}
	if cfg.Replicas < 0 {
		cfg.Replicas = 0
	}
	if cfg.BM25K1 <= 0 {
		cfg.BM25K1 = 1.2
	}
	if cfg.BM25B <= 0 {
		cfg.BM25B = 0.75
	}

	base := strings.TrimRight(cfg.Addresses[0], "/")
	s := &ESKeywordStore{
		cfg:  cfg,
		base: base,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	if cfg.AutoCreate {
		if err := s.ensureIndex(context.Background()); err != nil {
			return nil, fmt.Errorf("es: ensure index: %w", err)
		}
	}
	return s, nil
}

func (s *ESKeywordStore) ensureIndex(ctx context.Context) error {
	url := fmt.Sprintf("%s/%s", s.base, s.cfg.IndexName)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("check index existence: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body := map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   s.cfg.Shards,
			"number_of_replicas": s.cfg.Replicas,
			"index": map[string]interface{}{
				"similarity": map[string]interface{}{
					"custom_bm25": map[string]interface{}{
						"type": "BM25",
						"k1":   s.cfg.BM25K1,
						"b":    s.cfg.BM25B,
					},
				},
			},
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"chunk_id": map[string]interface{}{
					"type": "keyword",
				},
				"content": map[string]interface{}{
					"type":       "text",
					"analyzer":   "standard",
					"similarity": "custom_bm25",
				},
			},
		},
	}

	return s.doPut(ctx, url, body)
}

// Add indexes a chunk document into Elasticsearch.
func (s *ESKeywordStore) Add(ctx context.Context, id, text string) error {
	if id == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	url := fmt.Sprintf("%s/%s/_doc/%s", s.base, s.cfg.IndexName, id)
	doc := map[string]interface{}{
		"chunk_id": id,
		"content":  text,
	}
	return s.doPut(ctx, url, doc)
}

// Remove deletes a chunk document from Elasticsearch.
func (s *ESKeywordStore) Remove(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/%s/_doc/%s", s.base, s.cfg.IndexName, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	s.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("es delete %s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("es delete %s: status %d: %s", id, resp.StatusCode, body)
	}
	return nil
}

// Search performs a BM25-scored match query and returns the top-K scored chunks.
func (s *ESKeywordStore) Search(ctx context.Context, query string, topK int) ([]ScoredChunk, error) {
	if topK <= 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}

	url := fmt.Sprintf("%s/%s/_search", s.base, s.cfg.IndexName)
	body := map[string]interface{}{
		"size": topK,
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"content": map[string]interface{}{
					"query":    query,
					"operator": "or",
				},
			},
		},
		"_source": []string{"chunk_id", "content"},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	s.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("es search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("es search: status %d: %s", resp.StatusCode, b)
	}

	var result esSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("es search: decode: %w", err)
	}

	out := make([]ScoredChunk, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		out = append(out, ScoredChunk{
			ChunkID:  hit.Source.ChunkID,
			Content:  hit.Source.Content,
			Score:    hit.Score,
			Channel:  "keyword",
			Metadata: map[string]interface{}{"channel": "keyword", "es_score": hit.Score},
		})
	}
	return out, nil
}

func (s *ESKeywordStore) doPut(ctx context.Context, url string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	s.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("es PUT %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("es PUT %s: status %d: %s", url, resp.StatusCode, b)
	}
	return nil
}

func (s *ESKeywordStore) setAuth(req *http.Request) {
	if s.cfg.Username != "" {
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
}

type esSearchResponse struct {
	Hits struct {
		Hits []esHit `json:"hits"`
	} `json:"hits"`
}

type esHit struct {
	Score  float64  `json:"_score"`
	Source esSource `json:"_source"`
}

type esSource struct {
	ChunkID string `json:"chunk_id"`
	Content string `json:"content"`
}
