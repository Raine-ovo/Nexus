package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey string

const requestIDKey ctxKey = "nexus_request_id"

type ScopeDecision struct {
	Scope      string
	Workstream string
	Decision   string
	Reason     string
	Score      int
	Threshold  int
	Candidates []ScopeDecisionCandidate
}

type ScopeDecisionCandidate struct {
	Scope      string `json:"scope"`
	Workstream string `json:"workstream,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Score      int    `json:"score"`
}

// TraceMiddleware injects a request ID and propagates it through context.
type TraceMiddleware struct{}

// NewTrace returns a trace middleware instance.
func NewTrace() *TraceMiddleware {
	return &TraceMiddleware{}
}

// Wrap wraps the next handler with request ID generation and response header.
func (t *TraceMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		w.Header().Set("X-Request-ID", rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request id if present.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// WithScopeDecision attaches scope routing context to the request context.
func WithScopeDecision(ctx context.Context, decision ScopeDecision) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey("nexus_scope_decision"), decision)
}

// ScopeDecisionFromContext returns scope routing context if present.
func ScopeDecisionFromContext(ctx context.Context) ScopeDecision {
	if ctx == nil {
		return ScopeDecision{}
	}
	v, _ := ctx.Value(ctxKey("nexus_scope_decision")).(ScopeDecision)
	return v
}
