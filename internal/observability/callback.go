package observability

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// CallbackHandler implements Eino-style callbacks for agent lifecycle events.
// It provides hooks that can be attached to agent operations for tracing, metrics, and logging.
type CallbackHandler struct {
	tracer     *Tracer
	metrics    *MetricsCollector
	runMetrics map[string]*MetricsCollector
	logger     *Logger
	mu         sync.RWMutex
}

// CallbackEvent describes a single lifecycle notification.
type CallbackEvent struct {
	Type      string // "on_start", "on_end", "on_error", "on_tool_start", "on_tool_end", "on_llm_start", "on_llm_end"
	NodeName  string
	RunID     string
	RunLabel  string
	ParentID  string
	Data      map[string]interface{}
	Timestamp time.Time
	Duration  time.Duration
}

// Logger is a thin leveled wrapper used by CallbackHandler (and tests).
type Logger struct {
	std *log.Logger
}

// NewLogger builds a callback logger writing to the standard logger.
func NewLogger(prefix string) *Logger {
	p := strings.TrimSpace(prefix)
	if p != "" && !strings.HasSuffix(p, " ") {
		p += " "
	}
	return &Logger{std: log.New(log.Writer(), p, log.LstdFlags|log.Lmicroseconds)}
}

// Print writes a structured line for a callback event.
func (l *Logger) Print(event CallbackEvent) {
	if l == nil || l.std == nil {
		return
	}
	l.std.Printf("[%s] node=%s run=%s parent=%s dur=%s data=%v",
		event.Type, event.NodeName, event.RunLabel+"/"+event.RunID, event.ParentID, event.Duration, event.Data)
}

// NewCallbackHandler wires tracing and metrics; a default logger is attached.
func NewCallbackHandler(tracer *Tracer, metrics *MetricsCollector) *CallbackHandler {
	return &CallbackHandler{
		tracer:     tracer,
		metrics:    metrics,
		runMetrics: make(map[string]*MetricsCollector),
		logger:     NewLogger("nexus/callback"),
	}
}

// OnStart records span/metric activity for a run start.
func (h *CallbackHandler) OnStart(event CallbackEvent) {
	h.handleLifecycle("on_start", event, lifecycleStart)
}

// OnEnd records completion and latency when Duration is set.
func (h *CallbackHandler) OnEnd(event CallbackEvent) {
	h.handleLifecycle("on_end", event, lifecycleEnd)
}

// OnError increments error counters and logs the failure.
func (h *CallbackHandler) OnError(event CallbackEvent) {
	h.handleLifecycle("on_error", event, lifecycleError)
}

// OnToolStart is invoked before a tool call executes.
func (h *CallbackHandler) OnToolStart(event CallbackEvent) {
	h.handleLifecycle("on_tool_start", event, lifecycleToolStart)
}

// OnToolEnd is invoked after a tool call finishes.
func (h *CallbackHandler) OnToolEnd(event CallbackEvent) {
	h.handleLifecycle("on_tool_end", event, lifecycleToolEnd)
}

// OnLLMStart is invoked before an LLM request is sent.
func (h *CallbackHandler) OnLLMStart(event CallbackEvent) {
	h.handleLifecycle("on_llm_start", event, lifecycleLLMStart)
}

// OnLLMEnd is invoked after an LLM response is received.
func (h *CallbackHandler) OnLLMEnd(event CallbackEvent) {
	h.handleLifecycle("on_llm_end", event, lifecycleLLMEnd)
}

type lifecycleKind int

const (
	lifecycleStart lifecycleKind = iota
	lifecycleEnd
	lifecycleError
	lifecycleToolStart
	lifecycleToolEnd
	lifecycleLLMStart
	lifecycleLLMEnd
)

func (h *CallbackHandler) handleLifecycle(defaultType string, event CallbackEvent, kind lifecycleKind) {
	if h == nil {
		return
	}
	ev := event
	if ev.Type == "" {
		ev.Type = defaultType
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	if h.metrics != nil {
		applyLifecycleMetrics(h.metrics, ev, kind)
	}
	if strings.TrimSpace(ev.RunLabel) != "" {
		applyLifecycleMetrics(h.collectorForRun(ev.RunLabel), ev, kind)
	}

	if h.logger != nil {
		h.logger.Print(ev)
	}
}

func (h *CallbackHandler) collectorForRun(runLabel string) *MetricsCollector {
	runLabel = strings.TrimSpace(runLabel)
	if h == nil || runLabel == "" {
		return nil
	}
	h.mu.RLock()
	existing := h.runMetrics[runLabel]
	h.mu.RUnlock()
	if existing != nil {
		return existing
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runMetrics[runLabel] == nil {
		h.runMetrics[runLabel] = NewMetricsCollector()
	}
	return h.runMetrics[runLabel]
}

func (h *CallbackHandler) SnapshotForRun(runLabel string) map[string]interface{} {
	runLabel = strings.TrimSpace(runLabel)
	if h == nil || runLabel == "" {
		return nil
	}
	h.mu.RLock()
	mc := h.runMetrics[runLabel]
	h.mu.RUnlock()
	if mc == nil {
		return map[string]interface{}{
			"counters":   map[string]int64{},
			"histograms": map[string]interface{}{},
		}
	}
	return mc.Snapshot()
}

func applyLifecycleMetrics(mc *MetricsCollector, ev CallbackEvent, kind lifecycleKind) {
	if mc == nil {
		return
	}
	mc.IncrCounter("callbacks_"+ev.Type, 1)
	switch kind {
	case lifecycleStart:
		mc.IncrCounter("runs_active", 1)
	case lifecycleEnd:
		mc.IncrCounter("runs_active", -1)
		if ev.Duration > 0 {
			mc.ObserveHistogram("callback_duration_seconds", ev.Duration.Seconds())
		}
	case lifecycleError:
		mc.IncrCounter("errors_total", 1)
	case lifecycleToolStart:
		mc.IncrCounter("tools_active", 1)
	case lifecycleToolEnd:
		mc.IncrCounter("tools_active", -1)
		if ev.Duration > 0 {
			mc.ObserveHistogram("tool_duration_seconds", ev.Duration.Seconds())
		}
	case lifecycleLLMStart:
		mc.IncrCounter("llm_calls_active", 1)
	case lifecycleLLMEnd:
		mc.IncrCounter("llm_calls_active", -1)
		if ev.Duration > 0 {
			mc.ObserveHistogram("llm_duration_seconds", ev.Duration.Seconds())
		}
	}
}

// Logf writes a formatted message through the callback logger.
func (h *CallbackHandler) Logf(format string, args ...interface{}) {
	if h == nil || h.logger == nil || h.logger.std == nil {
		return
	}
	h.logger.std.Output(2, fmt.Sprintf(format, args...))
}
