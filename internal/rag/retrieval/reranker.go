package retrieval

import (
	"context"
	"math"
	"sort"
	"strings"
)

// Reranker reorders retrieved chunks by relevance to the query.
type Reranker interface {
	Rerank(ctx context.Context, query string, chunks []ScoredChunk, topK int) []ScoredChunk
}

// CrossEncoderReranker scores chunks with a lightweight lexical overlap model by default.
// For production, set ScoreFn to an LLM or hosted cross-encoder that returns a relevance score in [0,1].
// When ScoreFn returns an error, the reranker falls back to the lexical model for that chunk.
type CrossEncoderReranker struct {
	// Alpha weights the lexical similarity term; (1-Alpha) weights the prior retrieval score (log1p).
	Alpha float64
	// Beta blends optional LLM ScoreFn output when set: final = Beta*llm + (1-Beta)*lexicalBlend.
	Beta float64
	// ScoreFn is an optional async-friendly hook (e.g., OpenAI scores API or local cross-encoder).
	ScoreFn func(ctx context.Context, query, document string) (float64, error)
}

// NewCrossEncoderReranker returns a deterministic pseudo cross-encoder (lexical only).
func NewCrossEncoderReranker() *CrossEncoderReranker {
	return &CrossEncoderReranker{Alpha: 1.0, Beta: 0.7}
}

// Rerank assigns higher scores to chunks whose token multiset overlaps the query, optionally augmented by ScoreFn.
func (c *CrossEncoderReranker) Rerank(ctx context.Context, query string, chunks []ScoredChunk, topK int) []ScoredChunk {
	if len(chunks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = len(chunks)
	}
	alpha := c.Alpha
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	beta := c.Beta
	if beta < 0 {
		beta = 0
	}
	if beta > 1 {
		beta = 1
	}
	qBag := bagOfTokens(query)
	out := make([]ScoredChunk, len(chunks))
	copy(out, chunks)
	for i := range out {
		dBag := bagOfTokens(out[i].Content)
		sim := cosineBag(qBag, dBag)
		prior := out[i].Score
		if prior == 0 {
			prior = 1e-6
		}
		lexBlend := alpha*sim + (1-alpha)*math.Log1p(prior)
		final := lexBlend
		if c.ScoreFn != nil {
			llmScore, err := c.ScoreFn(ctx, query, out[i].Content)
			if err == nil {
				if llmScore < 0 {
					llmScore = 0
				}
				if llmScore > 1 {
					llmScore = 1
				}
				final = beta*llmScore + (1-beta)*lexBlend
				if out[i].Metadata == nil {
					out[i].Metadata = map[string]interface{}{}
				}
				out[i].Metadata["llm_relevance"] = llmScore
			}
			// On ScoreFn error, keep lexicalBlend as final score.
		}
		out[i].Score = final
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]interface{}{}
		}
		out[i].Metadata["cross_encoder_sim"] = sim
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func bagOfTokens(s string) map[string]float64 {
	toks := tokenize(s)
	m := make(map[string]float64)
	for _, t := range toks {
		m[t]++
	}
	return m
}

func cosineBag(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, na, nb float64
	for t, va := range a {
		if vb, ok := b[t]; ok {
			dot += va * vb
		}
		na += va * va
	}
	for _, vb := range b {
		nb += vb * vb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// KeywordBoostReranker adds a bonus when query tokens appear in the chunk (case-insensitive).
type KeywordBoostReranker struct {
	BoostPerHit float64
}

// NewKeywordBoostReranker uses a default boost of 0.15 per distinct query token hit.
func NewKeywordBoostReranker() *KeywordBoostReranker {
	return &KeywordBoostReranker{BoostPerHit: 0.15}
}

// Rerank increases score for each distinct query keyword present in chunk text.
func (k *KeywordBoostReranker) Rerank(ctx context.Context, query string, chunks []ScoredChunk, topK int) []ScoredChunk {
	_ = ctx
	if len(chunks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = len(chunks)
	}
	qToks := tokenize(query)
	seen := make(map[string]struct{})
	var uniq []string
	for _, t := range qToks {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		uniq = append(uniq, t)
	}
	out := make([]ScoredChunk, len(chunks))
	copy(out, chunks)
	for i := range out {
		text := strings.ToLower(out[i].Content)
		hits := 0
		for _, t := range uniq {
			if t == "" {
				continue
			}
			if strings.Contains(text, t) {
				hits++
			}
		}
		out[i].Score += float64(hits) * k.BoostPerHit
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]interface{}{}
		}
		out[i].Metadata["keyword_hits"] = hits
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// CompositeReranker chains multiple rerankers in order.
type CompositeReranker struct {
	Steps []Reranker
}

// NewCompositeReranker wraps ordered rerankers.
func NewCompositeReranker(steps ...Reranker) *CompositeReranker {
	return &CompositeReranker{Steps: steps}
}

// Rerank applies each reranker sequentially; each step receives topK from the previous.
func (cr *CompositeReranker) Rerank(ctx context.Context, query string, chunks []ScoredChunk, topK int) []ScoredChunk {
	cur := chunks
	for _, step := range cr.Steps {
		if step == nil {
			continue
		}
		cur = step.Rerank(ctx, query, cur, topK)
	}
	return cur
}
