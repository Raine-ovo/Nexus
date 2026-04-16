package index

import (
	"context"
	"errors"
	"fmt"
)

// Common errors returned by VectorStore implementations.
var (
	ErrEmptyID       = errors.New("index: empty vector id")
	ErrEmptyVector   = errors.New("index: empty embedding vector")
	ErrDimension     = errors.New("index: vector dimension mismatch")
	ErrInvalidTopK   = errors.New("index: topK must be positive")
	ErrContextCancel = errors.New("index: context canceled")
)

// VectorStore is the abstraction for dense retrieval backends (in-memory, disk, or remote).
// Implementations must be safe for concurrent use unless documented otherwise.
//
// Contract:
//   - Add replaces an existing id in full (metadata and vector).
//   - Search ranks by semantic similarity; higher Score means closer to the query vector.
//   - Delete is idempotent when the id is missing.
//   - Count is a cheap cardinality hint for metrics and fusion heuristics.
type VectorStore interface {
	Add(ctx context.Context, id string, vector []float64, metadata map[string]interface{}) error
	Search(ctx context.Context, queryVector []float64, topK int) ([]VectorResult, error)
	Delete(ctx context.Context, id string) error
	Count() int
}

// VectorResult is one hit from a vector similarity search.
type VectorResult struct {
	ID       string
	Score    float64
	Metadata map[string]interface{}
}

// ValidateVectorDims returns an error if len(v) does not equal expectedDim (when expectedDim > 0).
func ValidateVectorDims(id string, v []float64, expectedDim int) error {
	if id == "" {
		return fmt.Errorf("%w", ErrEmptyID)
	}
	if len(v) == 0 {
		return fmt.Errorf("%w for id %q", ErrEmptyVector, id)
	}
	if expectedDim > 0 && len(v) != expectedDim {
		return fmt.Errorf("%w: got %d want %d for id %q", ErrDimension, len(v), expectedDim, id)
	}
	return nil
}

// WrapContextError maps context.Canceled / DeadlineExceeded to ErrContextCancel when appropriate.
func WrapContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrContextCancel, err)
	}
	return err
}
