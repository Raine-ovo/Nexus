package observability

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Context propagation: trace and span identifiers are attached with private context keys.
// Read them with TraceIDFromContext and SpanIDFromContext; StartSpan writes both on the returned context.
type ctxKey int

const (
	keyTraceID ctxKey = iota
	keySpanID
)

// Tracer provides distributed tracing for agent operations.
// Implements a simple span-based tracing system with parent-child relationships.
type Tracer struct {
	spans map[string]*Span
	mu    sync.RWMutex
}

// Span is one unit of work in a trace.
type Span struct {
	TraceID   string
	SpanID    string
	ParentID  string
	Operation string
	StartTime time.Time
	EndTime   time.Time
	Tags      map[string]string
	Events    []SpanEvent
	Status    string // "ok", "error"
}

// SpanEvent is a point-in-time annotation on a span.
type SpanEvent struct {
	Name      string
	Timestamp time.Time
	Attrs     map[string]string
}

// NewTracer creates an empty in-memory tracer.
func NewTracer() *Tracer {
	return &Tracer{
		spans: make(map[string]*Span),
	}
}

// StartSpan creates a child span (or root if no parent span id is in ctx).
// Context carries traceID and the active spanID for nesting.
func (t *Tracer) StartSpan(ctx context.Context, operation string) (context.Context, *Span) {
	if t == nil {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	traceID, _ := ctx.Value(keyTraceID).(string)
	if traceID == "" {
		traceID = uuid.NewString()
	}
	parentID, _ := ctx.Value(keySpanID).(string)
	spanID := uuid.NewString()

	span := &Span{
		TraceID:   traceID,
		SpanID:    spanID,
		ParentID:  parentID,
		Operation: operation,
		StartTime: time.Now().UTC(),
		Tags:      make(map[string]string),
		Status:    "ok",
	}

	t.mu.Lock()
	t.spans[spanID] = span
	t.mu.Unlock()

	next := context.WithValue(context.WithValue(ctx, keyTraceID, traceID), keySpanID, spanID)
	return next, span
}

// EndSpan marks the span finished. Nil span is a no-op.
func (t *Tracer) EndSpan(span *Span) {
	if t == nil || span == nil {
		return
	}
	span.EndTime = time.Now().UTC()
	if span.Status == "" {
		span.Status = "ok"
	}
}

// AddEvent appends a timed event to the span.
func (t *Tracer) AddEvent(span *Span, name string, attrs map[string]string) {
	if span == nil || name == "" {
		return
	}
	cp := cloneStringMap(attrs)
	span.Events = append(span.Events, SpanEvent{
		Name:      name,
		Timestamp: time.Now().UTC(),
		Attrs:     cp,
	})
}

// SetSpanError marks the span as failed.
func (t *Tracer) SetSpanError(span *Span, msg string) {
	if span == nil {
		return
	}
	span.Status = "error"
	if span.Tags == nil {
		span.Tags = make(map[string]string)
	}
	if msg != "" {
		span.Tags["error"] = msg
	}
}

// GetTrace returns all spans for traceID sorted by start time.
func (t *Tracer) GetTrace(traceID string) []*Span {
	if t == nil || traceID == "" {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []*Span
	for _, s := range t.spans {
		if s != nil && s.TraceID == traceID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.Before(out[j].StartTime)
	})
	return out
}

// TraceIDFromContext returns the trace id propagated on ctx, if any.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(keyTraceID).(string)
	return id
}

// SpanIDFromContext returns the innermost span id on ctx, if any.
func SpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(keySpanID).(string)
	return id
}

func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// FormatSpan returns a short debug representation.
func FormatSpan(s *Span) string {
	if s == nil {
		return "<nil span>"
	}
	return fmt.Sprintf("trace=%s span=%s op=%s status=%s", s.TraceID, s.SpanID, s.Operation, s.Status)
}
