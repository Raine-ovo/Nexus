package observability

import (
	"context"
	"testing"
)

func TestTracerEvictsOldest(t *testing.T) {
	tr := NewTracerWithLimit(2)
	for _, op := range []string{"op1", "op2", "op3"} {
		_, span := tr.StartSpan(context.Background(), op)
		tr.EndSpan(span)
	}
	if got := len(tr.ListTraces(0)); got != 2 {
		t.Fatalf("traces = %d, want 2 after eviction", got)
	}
}

func TestTracerDefaultLimit(t *testing.T) {
	tr := NewTracer()
	if tr.maxSpans != defaultMaxSpans {
		t.Fatalf("maxSpans = %d, want %d", tr.maxSpans, defaultMaxSpans)
	}
}
