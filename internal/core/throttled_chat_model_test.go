package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rainea/nexus/pkg/types"
)

type countingModel struct {
	active    int32
	maxActive int32
	delay     time.Duration
}

func (m *countingModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*ChatModelResponse, error) {
	cur := atomic.AddInt32(&m.active, 1)
	for {
		prev := atomic.LoadInt32(&m.maxActive)
		if cur <= prev || atomic.CompareAndSwapInt32(&m.maxActive, prev, cur) {
			break
		}
	}
	defer atomic.AddInt32(&m.active, -1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
	}
	return &ChatModelResponse{Content: "ok"}, nil
}

func TestNewThrottledChatModel_LimitsConcurrency(t *testing.T) {
	base := &countingModel{delay: 60 * time.Millisecond}
	model := NewThrottledChatModel(base, 1, 0)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := model.Generate(context.Background(), "system", nil, nil); err != nil {
				t.Errorf("generate: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&base.maxActive); got != 1 {
		t.Fatalf("expected max active = 1, got %d", got)
	}
}

func TestNewThrottledChatModel_DisabledReturnsBase(t *testing.T) {
	base := &countingModel{}
	if got := NewThrottledChatModel(base, 0, 0); got != base {
		t.Fatalf("expected disabled throttling to return original model")
	}
}

func TestNewThrottledChatModel_RespectsMinRequestInterval(t *testing.T) {
	base := &countingModel{}
	model := NewThrottledChatModel(base, 1, 80*time.Millisecond)

	start := time.Now()
	if _, err := model.Generate(context.Background(), "system", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := model.Generate(context.Background(), "system", nil, nil); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 75*time.Millisecond {
		t.Fatalf("expected throttling interval to delay second request, elapsed=%s", elapsed)
	}
}
