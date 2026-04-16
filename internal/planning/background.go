package planning

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// BackgroundManager provides execution slots for background tasks.
// It separates "what to do" (TaskDAG) from "how to run" (slots).
type BackgroundManager struct {
	maxSlots    int
	activeSlots map[string]*BackgroundSlot
	mu          sync.Mutex
}

// BackgroundSlot tracks one running background execution.
type BackgroundSlot struct {
	TaskID    int
	AgentName string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Done      chan struct{}
	Result    string
	Err       error
}

// NewBackgroundManager creates a manager with at most maxSlots concurrent runs.
func NewBackgroundManager(maxSlots int) *BackgroundManager {
	if maxSlots < 1 {
		maxSlots = 1
	}
	return &BackgroundManager{
		maxSlots:    maxSlots,
		activeSlots: make(map[string]*BackgroundSlot),
	}
}

// Submit starts fn in a new slot when capacity allows. ctx is used only to derive a cancellable child context.
func (b *BackgroundManager) Submit(parentCtx context.Context, taskID int, agentName string, fn func(context.Context) (string, error)) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("background: fn is nil")
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	b.mu.Lock()
	if len(b.activeSlots) >= b.maxSlots {
		b.mu.Unlock()
		return "", fmt.Errorf("background: no free slots (%d active)", len(b.activeSlots))
	}

	slotID := uuid.NewString()
	ctx, cancel := context.WithCancel(parentCtx)
	slot := &BackgroundSlot{
		TaskID:    taskID,
		AgentName: agentName,
		StartedAt: time.Now().UTC(),
		Cancel:    cancel,
		Done:      make(chan struct{}),
	}
	b.activeSlots[slotID] = slot
	b.mu.Unlock()

	go func() {
		defer close(slot.Done)
		defer cancel()
		res, err := fn(ctx)
		b.mu.Lock()
		slot.Result = res
		slot.Err = err
		b.mu.Unlock()
	}()

	return slotID, nil
}

// Cancel stops a slot by its ID.
func (b *BackgroundManager) Cancel(slotID string) error {
	b.mu.Lock()
	slot := b.activeSlots[slotID]
	b.mu.Unlock()
	if slot == nil {
		return fmt.Errorf("background: unknown slot %q", slotID)
	}
	if slot.Cancel != nil {
		slot.Cancel()
	}
	return nil
}

// Status returns a snapshot of the slot; Err is only valid after the slot finishes.
func (b *BackgroundManager) Status(slotID string) (*BackgroundSlot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	slot := b.activeSlots[slotID]
	if slot == nil {
		return nil, fmt.Errorf("background: unknown slot %q", slotID)
	}
	cp := *slot
	return &cp, nil
}

// Wait blocks until the slot completes or ctx is done.
func (b *BackgroundManager) Wait(ctx context.Context, slotID string) (*BackgroundSlot, error) {
	for {
		b.mu.Lock()
		slot := b.activeSlots[slotID]
		b.mu.Unlock()
		if slot == nil {
			return nil, fmt.Errorf("background: unknown slot %q", slotID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-slot.Done:
			return b.Status(slotID)
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-slot.Done:
			return b.Status(slotID)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ActiveCount returns how many slots are allocated (including finished until removed).
func (b *BackgroundManager) ActiveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.activeSlots)
}

// Shutdown cancels all slots and removes them after they exit.
func (b *BackgroundManager) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	slots := make([]*BackgroundSlot, 0, len(b.activeSlots))
	for _, s := range b.activeSlots {
		slots = append(slots, s)
	}
	b.mu.Unlock()

	for _, s := range slots {
		if s.Cancel != nil {
			s.Cancel()
		}
	}

	deadline := time.After(30 * time.Second)
	for _, s := range slots {
		select {
		case <-s.Done:
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("background: shutdown timeout")
		}
	}

	b.mu.Lock()
	b.activeSlots = make(map[string]*BackgroundSlot)
	b.mu.Unlock()
	return nil
}

// Release removes a finished slot from the registry.
func (b *BackgroundManager) Release(slotID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.activeSlots[slotID]; ok {
		select {
		case <-s.Done:
			delete(b.activeSlots, slotID)
		default:
		}
	}
}
