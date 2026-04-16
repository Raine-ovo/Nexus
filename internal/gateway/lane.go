package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/rainea/nexus/configs"
)

// ErrStaleLane is returned when a lane was reset while the task was queued.
var ErrStaleLane = errors.New("gateway: lane reset, task discarded")

// LaneManager provides named queues with independent concurrency limits.
type LaneManager struct {
	lanes map[string]*Lane
	mu    sync.RWMutex
}

// Lane is a named execution lane with bounded concurrency.
type Lane struct {
	Name           string
	MaxConcurrency int
	generation     atomic.Uint64
	queue          chan *LaneTask
	cancel         context.CancelFunc
	done           chan struct{}
}

// LaneTask is one unit of work in a lane.
type LaneTask struct {
	ID         string
	Generation uint64
	Execute    func(ctx context.Context) (string, error)
	ResultCh   chan LaneResult
}

// LaneResult is the outcome of a lane task.
type LaneResult struct {
	Output string
	Err    error
}

// NewLaneManager builds lanes from gateway config defaults.
func NewLaneManager(cfg configs.GatewayConfig) *LaneManager {
	m := &LaneManager{lanes: make(map[string]*Lane)}
	for name, lc := range cfg.Lanes {
		max := lc.MaxConcurrency
		if max < 1 {
			max = 1
		}
		m.lanes[name] = &Lane{
			Name:           name,
			MaxConcurrency: max,
			queue:          make(chan *LaneTask, 256),
		}
	}
	return m
}

// Start launches queue processors for every lane. Call once.
func (m *LaneManager) Start(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, lane := range m.lanes {
		lane := lane
		laneCtx, cancel := context.WithCancel(ctx)
		lane.cancel = cancel
		lane.done = make(chan struct{})
		go func() {
			defer close(lane.done)
			lane.processQueue(laneCtx)
		}()
	}
}

// Stop cancels lane processors and waits for them to exit.
func (m *LaneManager) Stop() {
	m.mu.RLock()
	lanes := make([]*Lane, 0, len(m.lanes))
	for _, l := range m.lanes {
		lanes = append(lanes, l)
	}
	m.mu.RUnlock()
	for _, l := range lanes {
		if l.cancel != nil {
			l.cancel()
		}
		if l.done != nil {
			<-l.done
		}
	}
}

// Submit runs fn in the named lane and blocks until completion or ctx cancellation.
func (m *LaneManager) Submit(ctx context.Context, laneName string, fn func(ctx context.Context) (string, error)) (string, error) {
	m.mu.RLock()
	lane, ok := m.lanes[laneName]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("gateway: unknown lane %q", laneName)
	}

	gen := lane.generation.Load()
	resultCh := make(chan LaneResult, 1)
	task := &LaneTask{
		ID:         uuid.NewString(),
		Generation: gen,
		Execute:    fn,
		ResultCh:   resultCh,
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case lane.queue <- task:
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-resultCh:
		return res.Output, res.Err
	}
}

// Reset increments generation and drops queued tasks with ErrStaleLane.
func (m *LaneManager) Reset(laneName string) {
	m.mu.RLock()
	lane, ok := m.lanes[laneName]
	m.mu.RUnlock()
	if !ok {
		return
	}
	lane.generation.Add(1)
	for {
		select {
		case t := <-lane.queue:
			if t == nil {
				return
			}
			select {
			case t.ResultCh <- LaneResult{Err: ErrStaleLane}:
			default:
			}
		default:
			return
		}
	}
}

func (l *Lane) processQueue(ctx context.Context) {
	sem := make(chan struct{}, l.MaxConcurrency)
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-l.queue:
			if !ok {
				return
			}
			if task == nil {
				continue
			}
			curGen := l.generation.Load()
			if task.Generation != curGen {
				select {
				case task.ResultCh <- LaneResult{Err: ErrStaleLane}:
				default:
				}
				continue
			}
			sem <- struct{}{}
			go func(t *LaneTask) {
				defer func() { <-sem }()
				if l.generation.Load() != t.Generation {
					select {
					case t.ResultCh <- LaneResult{Err: ErrStaleLane}:
					default:
					}
					return
				}
				out, err := t.Execute(ctx)
				select {
				case t.ResultCh <- LaneResult{Output: out, Err: err}:
				default:
				}
			}(task)
		}
	}
}
