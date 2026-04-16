package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey string

const requestIDKey ctxKey = "nexus_request_id"

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
