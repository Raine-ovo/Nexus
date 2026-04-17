package observability

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/rainea/nexus/configs"
)

// Observer is the main facade used throughout the application for logging and observability.
// It implements the Observer interface expected by orchestrator and gateway.
type Observer struct {
	cfg      configs.ObservabilityConfig
	tracer   *Tracer
	metrics  *MetricsCollector
	callback *CallbackHandler
	logLevel string
	std      *log.Logger
	mu       sync.RWMutex
}

// New constructs the application observer from config.
func New(cfg configs.ObservabilityConfig) *Observer {
	tr := NewTracer()
	mc := NewMetricsCollector()
	cb := NewCallbackHandler(tr, mc)
	lvl := strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	if lvl == "" {
		lvl = "info"
	}
	return &Observer{
		cfg:      cfg,
		tracer:   tr,
		metrics:  mc,
		callback: cb,
		logLevel: lvl,
		std:      log.New(log.Writer(), "nexus ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Tracer returns the distributed tracer (may be unused when tracing is disabled).
func (o *Observer) Tracer() *Tracer {
	if o == nil {
		return nil
	}
	return o.tracer
}

// Metrics returns the metrics collector.
func (o *Observer) Metrics() *MetricsCollector {
	if o == nil {
		return nil
	}
	return o.metrics
}

// Callback returns the lifecycle callback handler.
func (o *Observer) Callback() *CallbackHandler {
	if o == nil {
		return nil
	}
	return o.callback
}

// MetricsSnapshot exposes the current metrics snapshot for debug endpoints.
func (o *Observer) MetricsSnapshot() map[string]interface{} {
	if o == nil || !o.cfg.MetricsEnabled {
		return nil
	}
	return o.metrics.Snapshot()
}

// MetricsSnapshotForRun exposes metrics aggregated for one sandbox run label.
func (o *Observer) MetricsSnapshotForRun(runLabel string) map[string]interface{} {
	if o == nil || !o.cfg.MetricsEnabled {
		return nil
	}
	if strings.TrimSpace(runLabel) == "" {
		return o.metrics.Snapshot()
	}
	if o.callback == nil {
		return nil
	}
	return o.callback.SnapshotForRun(runLabel)
}

// Trace returns all spans for a trace identifier.
func (o *Observer) Trace(traceID string) []*Span {
	if o == nil || !o.cfg.TraceEnabled {
		return nil
	}
	return o.tracer.GetTrace(traceID)
}

// ListTraces returns recent trace summaries.
func (o *Observer) ListTraces(limit int) []TraceSummary {
	if o == nil || !o.cfg.TraceEnabled {
		return nil
	}
	return o.tracer.ListTraces(limit)
}

// Info logs at info level.
func (o *Observer) Info(msg string, keysAndValues ...interface{}) {
	o.logAt(1, "info", msg, keysAndValues...)
}

// Warn logs at warn level.
func (o *Observer) Warn(msg string, keysAndValues ...interface{}) {
	o.logAt(1, "warn", msg, keysAndValues...)
}

// Debug logs at debug level.
func (o *Observer) Debug(msg string, keysAndValues ...interface{}) {
	o.logAt(1, "debug", msg, keysAndValues...)
}

// Error logs at error level.
func (o *Observer) Error(msg string, keysAndValues ...interface{}) {
	o.logAt(1, "error", msg, keysAndValues...)
}

func (o *Observer) logAt(depth int, level, msg string, keysAndValues ...interface{}) {
	if o == nil {
		return
	}
	if !o.shouldLog(level) {
		return
	}
	o.mu.RLock()
	std := o.std
	o.mu.RUnlock()
	if std == nil {
		return
	}
	line := fmt.Sprintf("%s %s", strings.ToUpper(level), msg)
	if len(keysAndValues) > 0 {
		line += " " + formatKV(keysAndValues)
	}
	_ = std.Output(depth+2, line)
}

func (o *Observer) shouldLog(level string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	order := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	cur, ok := order[strings.ToLower(o.logLevel)]
	if !ok {
		cur = 1
	}
	req, ok := order[strings.ToLower(level)]
	if !ok {
		req = 1
	}
	return req >= cur
}

func formatKV(keysAndValues []interface{}) string {
	var b strings.Builder
	for i := 0; i < len(keysAndValues); i += 2 {
		key := fmt.Sprint(keysAndValues[i])
		val := ""
		if i+1 < len(keysAndValues) {
			val = fmt.Sprint(keysAndValues[i+1])
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(val)
	}
	return b.String()
}
