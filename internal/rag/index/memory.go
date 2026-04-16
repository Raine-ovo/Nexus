package index

import (
	"context"
	"fmt"
	"math"
	"sync"
)

// Compile-time assertion: MemoryVectorStore implements VectorStore.
var _ VectorStore = (*MemoryVectorStore)(nil)

// vectorEntry holds one indexed vector and its metadata.
type vectorEntry struct {
	id       string
	vector   []float64
	metadata map[string]interface{}
}

// MemoryVectorStore is a thread-safe in-memory vector store using brute-force cosine similarity.
// Suitable for development and corpora well below ~100k vectors.
type MemoryVectorStore struct {
	mu             sync.RWMutex
	entries        []vectorEntry
	idToIdx        map[string]int
	dim            int
	allowResizeDim bool
}

// NewMemoryVectorStore creates an empty store. If dim > 0, all vectors must match dim.
// If dim is 0, the dimension is inferred from the first Add call.
func NewMemoryVectorStore(dim int) *MemoryVectorStore {
	return &MemoryVectorStore{
		entries:        nil,
		idToIdx:        make(map[string]int),
		dim:            dim,
		allowResizeDim: dim == 0,
	}
}

// Add inserts or replaces a vector by id.
func (m *MemoryVectorStore) Add(ctx context.Context, id string, vector []float64, metadata map[string]interface{}) error {
	if err := ctx.Err(); err != nil {
		return WrapContextError(ctx, err)
	}
	if err := ValidateVectorDims(id, vector, 0); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dim == 0 && m.allowResizeDim {
		m.dim = len(vector)
	}
	if len(vector) != m.dim {
		return fmt.Errorf("%w: vector dim %d != store dim %d for id %q", ErrDimension, len(vector), m.dim, id)
	}

	metaCopy := cloneMetadata(metadata)

	if idx, ok := m.idToIdx[id]; ok {
		m.entries[idx].vector = append([]float64(nil), vector...)
		m.entries[idx].metadata = metaCopy
		return nil
	}

	m.idToIdx[id] = len(m.entries)
	m.entries = append(m.entries, vectorEntry{
		id:       id,
		vector:   append([]float64(nil), vector...),
		metadata: metaCopy,
	})
	return nil
}

// Search returns the topK nearest neighbors by cosine similarity (higher is better).
func (m *MemoryVectorStore) Search(ctx context.Context, queryVector []float64, topK int) ([]VectorResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, WrapContextError(ctx, err)
	}
	if topK <= 0 {
		return nil, fmt.Errorf("%w", ErrInvalidTopK)
	}
	if len(queryVector) != m.dim && !(m.dim == 0 && len(m.entries) == 0) {
		return nil, fmt.Errorf("%w: query dim %d != store dim %d", ErrDimension, len(queryVector), m.dim)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.entries) == 0 {
		return nil, nil
	}

	type scored struct {
		res VectorResult
	}
	results := make([]scored, 0, len(m.entries))
	for _, e := range m.entries {
		s := cosineSimilarity(queryVector, e.vector)
		md := cloneMetadata(e.metadata)
		results = append(results, scored{
			res: VectorResult{ID: e.id, Score: s, Metadata: md},
		})
	}

	// partial selection sort for topK
	n := len(results)
	if topK < n {
		for i := 0; i < topK; i++ {
			best := i
			for j := i + 1; j < n; j++ {
				if results[j].res.Score > results[best].res.Score {
					best = j
				}
			}
			results[i], results[best] = results[best], results[i]
		}
		results = results[:topK]
	}

	out := make([]VectorResult, len(results))
	for i := range results {
		out[i] = results[i].res
	}
	return out, nil
}

// Delete removes a vector by id. No-op if missing.
func (m *MemoryVectorStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return WrapContextError(ctx, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, ok := m.idToIdx[id]
	if !ok {
		return nil
	}
	last := len(m.entries) - 1
	if idx != last {
		m.entries[idx] = m.entries[last]
		m.idToIdx[m.entries[idx].id] = idx
	}
	m.entries = m.entries[:last]
	delete(m.idToIdx, id)
	return nil
}

// Count returns the number of stored vectors.
func (m *MemoryVectorStore) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func cloneMetadata(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
