package observability

import (
	"sync"
)

var defaultHistogramBuckets = []float64{
	0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// MetricsCollector collects and exposes operational metrics.
type MetricsCollector struct {
	counters   map[string]*int64
	histograms map[string]*Histogram
	mu         sync.RWMutex
}

// Histogram stores aggregate bucketed observations.
type Histogram struct {
	Count   int64
	Sum     float64
	Buckets []float64 // pre-defined bucket boundaries (upper bounds)
	Counts  []int64   // count per bucket
}

// NewMetricsCollector returns an empty collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		counters:   make(map[string]*int64),
		histograms: make(map[string]*Histogram),
	}
}

// IncrCounter adds delta to a named counter (delta may be negative).
func (c *MetricsCollector) IncrCounter(name string, delta int64) {
	if c == nil || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ptr, ok := c.counters[name]
	if !ok {
		v := int64(0)
		ptr = &v
		c.counters[name] = ptr
	}
	*ptr += delta
}

// ObserveHistogram records a sample into the named histogram, creating it if needed.
func (c *MetricsCollector) ObserveHistogram(name string, value float64) {
	if c == nil || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.histograms[name]
	if !ok {
		h = newHistogram(defaultHistogramBuckets)
		c.histograms[name] = h
	}
	h.observe(value)
}

// GetCounter returns the current counter value, or 0 if unset.
func (c *MetricsCollector) GetCounter(name string) int64 {
	if c == nil || name == "" {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	ptr := c.counters[name]
	if ptr == nil {
		return 0
	}
	return *ptr
}

// GetHistogram returns a shallow copy of the histogram for name, or nil.
func (c *MetricsCollector) GetHistogram(name string) *Histogram {
	if c == nil || name == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	h := c.histograms[name]
	if h == nil {
		return nil
	}
	return h.clone()
}

// Snapshot returns counters and histogram summaries suitable for debug endpoints.
func (c *MetricsCollector) Snapshot() map[string]interface{} {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]interface{})
	counters := make(map[string]int64, len(c.counters))
	for n, ptr := range c.counters {
		if ptr != nil {
			counters[n] = *ptr
		}
	}
	out["counters"] = counters

	hists := make(map[string]interface{}, len(c.histograms))
	for n, h := range c.histograms {
		if h != nil {
			hists[n] = h.clone()
		}
	}
	out["histograms"] = hists
	return out
}

func newHistogram(buckets []float64) *Histogram {
	b := make([]float64, len(buckets))
	copy(b, buckets)
	return &Histogram{
		Buckets: b,
		Counts:  make([]int64, len(b)),
	}
}

func (h *Histogram) observe(value float64) {
	h.Count++
	h.Sum += value
	for i, upper := range h.Buckets {
		if value <= upper {
			h.Counts[i]++
			return
		}
	}
	if n := len(h.Counts); n > 0 {
		h.Counts[n-1]++
	}
}

func (h *Histogram) clone() *Histogram {
	if h == nil {
		return nil
	}
	cp := &Histogram{
		Count: h.Count,
		Sum:   h.Sum,
	}
	if len(h.Buckets) > 0 {
		cp.Buckets = make([]float64, len(h.Buckets))
		copy(cp.Buckets, h.Buckets)
	}
	if len(h.Counts) > 0 {
		cp.Counts = make([]int64, len(h.Counts))
		copy(cp.Counts, h.Counts)
	}
	return cp
}
