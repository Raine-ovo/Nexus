package observability

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// TraceSummary is a compact trace-level view for debug listing endpoints.
type TraceSummary struct {
	TraceID    string    `json:"trace_id"`
	RootSpanID string    `json:"root_span_id"`
	Operation  string    `json:"operation"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Status     string    `json:"status"`
	SpanCount  int       `json:"span_count"`
	RequestID  string    `json:"request_id,omitempty"`
	RunLabel   string    `json:"run_label,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Workstream string    `json:"workstream,omitempty"`
	Decision   string    `json:"scope_decision,omitempty"`
	Score      int       `json:"scope_score,omitempty"`
	Threshold  int       `json:"scope_threshold,omitempty"`
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

// ListTraces returns recent trace summaries sorted by root span start time descending.
func (t *Tracer) ListTraces(limit int) []TraceSummary {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	type aggregate struct {
		root  *Span
		count int
	}
	agg := make(map[string]*aggregate)
	for _, span := range t.spans {
		if span == nil || span.TraceID == "" {
			continue
		}
		item := agg[span.TraceID]
		if item == nil {
			item = &aggregate{}
			agg[span.TraceID] = item
		}
		item.count++
		if item.root == nil || span.ParentID == "" && span.StartTime.Before(item.root.StartTime) {
			item.root = span
		}
	}

	out := make([]TraceSummary, 0, len(agg))
	for traceID, item := range agg {
		if item == nil || item.root == nil {
			continue
		}
		out = append(out, TraceSummary{
			TraceID:    traceID,
			RootSpanID: item.root.SpanID,
			Operation:  item.root.Operation,
			StartTime:  item.root.StartTime,
			EndTime:    item.root.EndTime,
			Status:     item.root.Status,
			SpanCount:  item.count,
			RequestID:  item.root.Tags["request_id"],
			RunLabel:   item.root.Tags["sandbox_run"],
			Scope:      strings.TrimSpace(item.root.Tags["scope"]),
			Workstream: strings.TrimSpace(item.root.Tags["workstream"]),
			Decision:   strings.TrimSpace(item.root.Tags["scope_decision"]),
			Score:      parseIntTag(item.root.Tags["scope_score"]),
			Threshold:  parseIntTag(item.root.Tags["scope_threshold"]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func parseIntTag(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
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
