package core

import "context"

// StreamDeltaFunc receives incremental content deltas during a streaming
// generation. Returning a non-nil error aborts the generation.
type StreamDeltaFunc func(delta string) error

type streamSinkKey struct{}

// WithStreamSink attaches a streaming delta sink to ctx. When present, a
// ChatModel that supports streaming emits each token delta to the sink while
// still returning the fully assembled response.
func WithStreamSink(ctx context.Context, sink StreamDeltaFunc) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, streamSinkKey{}, sink)
}

// StreamSinkFromContext returns the attached sink, or nil.
func StreamSinkFromContext(ctx context.Context) StreamDeltaFunc {
	if ctx == nil {
		return nil
	}
	if s, ok := ctx.Value(streamSinkKey{}).(StreamDeltaFunc); ok {
		return s
	}
	return nil
}
