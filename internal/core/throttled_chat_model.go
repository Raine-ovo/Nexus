package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rainea/nexus/pkg/types"
)

// NewThrottledChatModel wraps a ChatModel with a process-wide concurrency limiter.
// maxConcurrency <= 0 and minRequestInterval <= 0 disables throttling and returns the original model unchanged.
func NewThrottledChatModel(model ChatModel, maxConcurrency int, minRequestInterval time.Duration) ChatModel {
	if model == nil {
		return nil
	}
	if maxConcurrency <= 0 && minRequestInterval <= 0 {
		return model
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1 << 20
	}
	return &throttledChatModel{
		base:               model,
		sem:                make(chan struct{}, maxConcurrency),
		minRequestInterval: minRequestInterval,
	}
}

type throttledChatModel struct {
	base               ChatModel
	sem                chan struct{}
	minRequestInterval time.Duration
	mu                 sync.Mutex
	lastRequestAt      time.Time
}

func (m *throttledChatModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*ChatModelResponse, error) {
	if m == nil || m.base == nil {
		return nil, fmt.Errorf("core: nil throttled chat model")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.sem }()

	if err := m.waitForInterval(ctx); err != nil {
		return nil, err
	}
	return m.base.Generate(ctx, system, messages, tools)
}

func (m *throttledChatModel) waitForInterval(ctx context.Context) error {
	if m == nil || m.minRequestInterval <= 0 {
		return nil
	}
	for {
		m.mu.Lock()
		now := time.Now()
		wait := m.lastRequestAt.Add(m.minRequestInterval).Sub(now)
		if wait <= 0 {
			m.lastRequestAt = now
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
