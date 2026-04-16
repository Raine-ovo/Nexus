package retrieval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/rainea/nexus/internal/rag/chunker"
	"github.com/rainea/nexus/internal/rag/index"
)

const rrfK = 60

// ScoredChunk is a retrieved segment with a relevance score and optional channel label.
type ScoredChunk struct {
	ChunkID  string
	Content  string
	DocID    string
	Score    float64
	Channel  string
	Metadata map[string]interface{}
}

// KeywordStore abstracts keyword-based retrieval backends (in-memory, Elasticsearch, etc.).
type KeywordStore interface {
	Add(ctx context.Context, id, text string) error
	Remove(ctx context.Context, id string) error
	Search(ctx context.Context, query string, topK int) ([]ScoredChunk, error)
}

// KeywordIndex is a simple in-memory inverted index for lexical retrieval (TF-IDF scoring).
type KeywordIndex struct {
	mu       sync.RWMutex
	postings map[string]map[string]float64 // term -> chunkID -> term frequency (raw count)
	docs     map[string]keywordDoc
	docCount int
}

type keywordDoc struct {
	text string
	tf   map[string]float64
}

// NewKeywordIndex creates an empty keyword index.
func NewKeywordIndex() *KeywordIndex {
	return &KeywordIndex{
		postings: make(map[string]map[string]float64),
		docs:     make(map[string]keywordDoc),
	}
}

// Add indexes one chunk's text for keyword search.
func (k *KeywordIndex) Add(_ context.Context, id, text string) error {
	if id == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	toks := tokenize(text)
	if len(toks) == 0 {
		return nil
	}
	tf := termFreq(toks)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.docs[id] = keywordDoc{text: text, tf: tf}
	k.docCount = len(k.docs)
	for term, freq := range tf {
		if k.postings[term] == nil {
			k.postings[term] = make(map[string]float64)
		}
		k.postings[term][id] = freq
	}
	return nil
}

// Remove deletes a chunk from the keyword index.
func (k *KeywordIndex) Remove(_ context.Context, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	doc, ok := k.docs[id]
	if !ok {
		return nil
	}
	for term := range doc.tf {
		if m, ok := k.postings[term]; ok {
			delete(m, id)
			if len(m) == 0 {
				delete(k.postings, term)
			}
		}
	}
	delete(k.docs, id)
	k.docCount = len(k.docs)
	return nil
}

// Search returns top keyword hits using a lightweight TF-IDF style score.
func (k *KeywordIndex) Search(_ context.Context, query string, topK int) ([]ScoredChunk, error) {
	if topK <= 0 {
		return nil, nil
	}
	qToks := tokenize(query)
	if len(qToks) == 0 {
		return nil, nil
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.docCount == 0 {
		return nil, nil
	}
	scores := make(map[string]float64)
	for _, term := range qToks {
		df := float64(len(k.postings[term]))
		if df == 0 {
			continue
		}
		idf := math.Log(1.0 + float64(k.docCount)/df)
		for id, tf := range k.postings[term] {
			scores[id] += tf * idf
		}
	}
	type pair struct {
		id    string
		score float64
	}
	var list []pair
	for id, s := range scores {
		list = append(list, pair{id: id, score: s})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score == list[j].score {
			return list[i].id < list[j].id
		}
		return list[i].score > list[j].score
	})
	if len(list) > topK {
		list = list[:topK]
	}
	out := make([]ScoredChunk, 0, len(list))
	for _, p := range list {
		doc := k.docs[p.id]
		out = append(out, ScoredChunk{
			ChunkID:  p.id,
			Content:  doc.text,
			Score:    p.score,
			Channel:  "keyword",
			Metadata: map[string]interface{}{"channel": "keyword"},
		})
	}
	return out, nil
}

// MultiChannelEngine runs vector and keyword retrieval in parallel and merges with RRF.
type MultiChannelEngine struct {
	vectorStore  index.VectorStore
	keywordStore KeywordStore
	embedder     Embedder
}

// Embedder produces query vectors (same contract as rag.Embedder).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	Dimensions() int
}

// NewMultiChannelEngine wires stores and the embedder used for query encoding.
// vs accepts any index.VectorStore; ks accepts any KeywordStore (memory, Elasticsearch, etc.).
func NewMultiChannelEngine(vs index.VectorStore, ks KeywordStore, emb Embedder) *MultiChannelEngine {
	return &MultiChannelEngine{
		vectorStore:  vs,
		keywordStore: ks,
		embedder:     emb,
	}
}

// Retrieve runs vector and keyword channels concurrently and fuses rankings with RRF.
func (m *MultiChannelEngine) Retrieve(ctx context.Context, query string, topK int) ([]ScoredChunk, error) {
	if topK <= 0 {
		return nil, fmt.Errorf("retrieval: topK must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.vectorStore == nil || m.keywordStore == nil || m.embedder == nil {
		return nil, fmt.Errorf("retrieval: incomplete engine configuration")
	}

	vecTop := topK * 2
	if vecTop < 10 {
		vecTop = 10
	}

	var wg sync.WaitGroup
	var vecHits []index.VectorResult
	var vecErr error
	var kwHits []ScoredChunk
	var kwErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		qv, err := m.embedder.Embed(ctx, query)
		if err != nil {
			vecErr = err
			return
		}
		vecHits, vecErr = m.vectorStore.Search(ctx, qv, vecTop)
	}()
	go func() {
		defer wg.Done()
		kwHits, kwErr = m.keywordStore.Search(ctx, query, vecTop)
	}()
	wg.Wait()
	if vecErr != nil {
		return nil, fmt.Errorf("retrieval: vector search: %w", vecErr)
	}
	if kwErr != nil {
		return nil, fmt.Errorf("retrieval: keyword search: %w", kwErr)
	}

	vecRanked := vectorResultsToScored(vecHits)
	for i := range vecRanked {
		vecRanked[i].Channel = "vector"
		if vecRanked[i].Metadata == nil {
			vecRanked[i].Metadata = map[string]interface{}{}
		}
		vecRanked[i].Metadata["channel"] = "vector"
	}

	return reciprocalRankFusion(vecRanked, kwHits, topK), nil
}

func vectorResultsToScored(vr []index.VectorResult) []ScoredChunk {
	out := make([]ScoredChunk, 0, len(vr))
	for _, r := range vr {
		content := ""
		docID := ""
		if r.Metadata != nil {
			if c, ok := r.Metadata["content"].(string); ok {
				content = c
			}
			if d, ok := r.Metadata["doc_id"].(string); ok {
				docID = d
			}
		}
		md := cloneMeta(r.Metadata)
		out = append(out, ScoredChunk{
			ChunkID:  r.ID,
			Content:  content,
			DocID:    docID,
			Score:    r.Score,
			Metadata: md,
		})
	}
	return out
}

// reciprocalRankFusion merges two ranked lists using RRF (k=rrfK).
func reciprocalRankFusion(vectorList, keywordList []ScoredChunk, topK int) []ScoredChunk {
	scores := make(map[string]float64)
	best := make(map[string]ScoredChunk)

	addRanks := func(list []ScoredChunk) {
		for rank, item := range list {
			id := item.ChunkID
			if id == "" {
				continue
			}
			scores[id] += 1.0 / (rrfK + float64(rank+1))
			prev := best[id]
			if prev.ChunkID == "" || item.Score > prev.Score {
				best[id] = item
			}
		}
	}
	addRanks(vectorList)
	addRanks(keywordList)

	type pair struct {
		id    string
		score float64
	}
	var plist []pair
	for id, s := range scores {
		plist = append(plist, pair{id: id, score: s})
	}
	sort.Slice(plist, func(i, j int) bool {
		if plist[i].score == plist[j].score {
			return plist[i].id < plist[j].id
		}
		return plist[i].score > plist[j].score
	})
	if len(plist) > topK {
		plist = plist[:topK]
	}

	out := make([]ScoredChunk, 0, len(plist))
	for _, p := range plist {
		ch := best[p.id]
		ch.Score = p.score
		if ch.Metadata == nil {
			ch.Metadata = map[string]interface{}{}
		}
		ch.Metadata["rrf_score"] = p.score
		out = append(out, ch)
	}
	return out
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var cur []rune
	var out []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, string(cur))
		cur = cur[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func termFreq(tokens []string) map[string]float64 {
	m := make(map[string]float64)
	for _, t := range tokens {
		m[t]++
	}
	return m
}

func cloneMeta(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ChunkToScored builds a ScoredChunk from a chunker.Chunk (for rerankers / tests).
func ChunkToScored(c chunker.Chunk, score float64, channel string) ScoredChunk {
	return ScoredChunk{
		ChunkID:  c.ID,
		Content:  c.Content,
		DocID:    c.DocID,
		Score:    score,
		Channel:  channel,
		Metadata: cloneMeta(c.Metadata),
	}
}
