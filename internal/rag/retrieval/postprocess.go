package retrieval

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"

	"github.com/rainea/nexus/internal/rag/chunker"
)

// PostProcessor mutates or filters a ranked chunk list.
type PostProcessor interface {
	Process(chunks []ScoredChunk) ([]ScoredChunk, error)
}

// PostChain runs processors in order; any error aborts the chain.
type PostChain struct {
	Steps []PostProcessor
}

// NewPostChain builds a sequential post-processing pipeline.
func NewPostChain(steps ...PostProcessor) *PostChain {
	return &PostChain{Steps: steps}
}

// Run executes all steps.
func (p *PostChain) Run(chunks []ScoredChunk) ([]ScoredChunk, error) {
	cur := chunks
	var err error
	for _, step := range p.Steps {
		if step == nil {
			continue
		}
		cur, err = step.Process(cur)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// DeduplicateProcessor removes near-duplicates using Jaccard similarity on token sets.
type DeduplicateProcessor struct {
	Threshold float64
}

// NewDeduplicateProcessor uses Jaccard >= 0.92 as near-duplicate.
func NewDeduplicateProcessor() *DeduplicateProcessor {
	return &DeduplicateProcessor{Threshold: 0.92}
}

// Process keeps higher-scoring chunk when two contents are highly similar.
func (d *DeduplicateProcessor) Process(chunks []ScoredChunk) ([]ScoredChunk, error) {
	if len(chunks) <= 1 {
		return chunks, nil
	}
	th := d.Threshold
	if th <= 0 || th > 1 {
		th = 0.92
	}
	kept := make([]ScoredChunk, 0, len(chunks))
	for _, c := range chunks {
		dup := false
		for _, k := range kept {
			if jaccardTokens(c.Content, k.Content) >= th {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, c)
		}
	}
	return kept, nil
}

func jaccardTokens(a, b string) float64 {
	sa := bagOfWords(a)
	sb := bagOfWords(b)
	if len(sa) == 0 && len(sb) == 0 {
		return 1
	}
	var inter int
	for t := range sa {
		if sb[t] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func bagOfWords(s string) map[string]bool {
	toks := tokenize(s)
	m := make(map[string]bool)
	for _, t := range toks {
		m[t] = true
	}
	return m
}

// ScoreNormalizer maps scores to [0,1] via min-max over the batch.
type ScoreNormalizer struct{}

// NewScoreNormalizer constructs a normalizer.
func NewScoreNormalizer() *ScoreNormalizer {
	return &ScoreNormalizer{}
}

// Process applies min-max normalization; constant scores become 0.5.
func (s *ScoreNormalizer) Process(chunks []ScoredChunk) ([]ScoredChunk, error) {
	if len(chunks) == 0 {
		return chunks, nil
	}
	minS, maxS := chunks[0].Score, chunks[0].Score
	for _, c := range chunks[1:] {
		if c.Score < minS {
			minS = c.Score
		}
		if c.Score > maxS {
			maxS = c.Score
		}
	}
	out := make([]ScoredChunk, len(chunks))
	copy(out, chunks)
	den := maxS - minS
	for i := range out {
		if den == 0 {
			out[i].Score = 0.5
		} else {
			out[i].Score = (out[i].Score - minS) / den
		}
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]interface{}{}
		}
		out[i].Metadata["norm_score"] = out[i].Score
	}
	return out, nil
}

// ContextEnricher prepends file path and section metadata to chunk text for downstream LLM context.
type ContextEnricher struct{}

// NewContextEnricher returns an enricher.
func NewContextEnricher() *ContextEnricher {
	return &ContextEnricher{}
}

// Process wraps Content with path/header lines when present in metadata.
func (c *ContextEnricher) Process(chunks []ScoredChunk) ([]ScoredChunk, error) {
	out := make([]ScoredChunk, len(chunks))
	copy(out, chunks)
	for i := range out {
		ch := chunker.Chunk{
			ID:       out[i].ChunkID,
			Content:  out[i].Content,
			DocID:    out[i].DocID,
			Metadata: out[i].Metadata,
		}
		out[i].Content = chunker.EnrichChunkContext(ch)
	}
	return out, nil
}

// FingerprintProcessor adds stable content hashes for logging and dedup keys.
type FingerprintProcessor struct{}

// NewFingerprintProcessor constructs the processor.
func NewFingerprintProcessor() *FingerprintProcessor {
	return &FingerprintProcessor{}
}

// Process annotates metadata with content_sha256.
func (f *FingerprintProcessor) Process(chunks []ScoredChunk) ([]ScoredChunk, error) {
	out := make([]ScoredChunk, len(chunks))
	copy(out, chunks)
	for i := range out {
		h := sha256.Sum256([]byte(out[i].Content))
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]interface{}{}
		}
		out[i].Metadata["content_sha256"] = fmt.Sprintf("%x", h[:])
	}
	return out, nil
}

// SortByScoreDesc ensures descending score order (stable).
func SortByScoreDesc(chunks []ScoredChunk) []ScoredChunk {
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Score == chunks[j].Score {
			return chunks[i].ChunkID < chunks[j].ChunkID
		}
		return chunks[i].Score > chunks[j].Score
	})
	return chunks
}

// MinMaxFloat is a small helper for external use.
func MinMaxFloat(vals []float64) (min, max float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	min, max = vals[0], vals[0]
	for _, v := range vals[1:] {
		min = math.Min(min, v)
		max = math.Max(max, v)
	}
	return min, max
}
