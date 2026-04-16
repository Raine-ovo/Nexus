package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/rainea/nexus/pkg/types"
)

// RecoveryPolicy configures Layer-3 transport-style retries.
type RecoveryPolicy struct {
	MaxAttempts int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	// JitterFraction in [0,1] scales random backoff (e.g. 0.2 => +/-20%).
	JitterFraction float64
	// MaxRetryBudget caps total wall time spent sleeping between attempts (0 = unlimited).
	MaxRetryBudget time.Duration
}

// DefaultRecoveryPolicy returns sensible defaults for LLM HTTP/RPC flakiness.
func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{
		MaxAttempts:      4,
		InitialDelay:     300 * time.Millisecond,
		MaxDelay:         8 * time.Second,
		JitterFraction:   0.25,
		MaxRetryBudget:   45 * time.Second,
	}
}

// RecoveryManager coordinates the three recovery layers described in Nexus design:
//
// Layer 1 — tool errors are surfaced as structured tool_result content so the model can self-correct.
// Layer 2 — context/token overflow triggers compaction via ContextGuard (invoked by the loop).
// Layer 3 — transient failures retried with exponential backoff, jitter, and a bounded budget.
type RecoveryManager struct {
	policy RecoveryPolicy
	obs    Observer
}

// NewRecoveryManager builds a manager. obs may be nil.
func NewRecoveryManager(policy RecoveryPolicy, obs Observer) *RecoveryManager {
	if policy.MaxAttempts <= 0 {
		policy = DefaultRecoveryPolicy()
	}
	if policy.InitialDelay <= 0 {
		policy.InitialDelay = 300 * time.Millisecond
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = 8 * time.Second
	}
	if policy.JitterFraction <= 0 {
		policy.JitterFraction = 0.2
	}
	return &RecoveryManager{policy: policy, obs: obs}
}

// WrapToolError implements Layer 1: never drops the error on the floor; the assistant sees it.
func WrapToolError(toolName, toolID string, err error) *types.ToolResult {
	if err == nil {
		return nil
	}
	msg := err.Error()
	content := fmt.Sprintf("error executing tool %q (id=%s): %s", toolName, toolID, msg)
	return &types.ToolResult{
		ToolID:  toolID,
		Name:    toolName,
		Content: content,
		IsError: true,
	}
}

// IsContextOverflowError implements Layer 2 heuristics: string matching on common provider errors.
// The loop uses this to trigger ContextGuard without hard-coding vendor SDK types.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "context length") || strings.Contains(s, "maximum context") {
		return true
	}
	if strings.Contains(s, "token") && (strings.Contains(s, "limit") || strings.Contains(s, "exceeded")) {
		return true
	}
	if strings.Contains(s, "too many tokens") {
		return true
	}
	return false
}

// CallWithRetry executes op with exponential backoff + jitter until success or attempts exhausted.
// ctx cancellation is always honored. Non-retryable errors fail fast.
//
// When state is non-nil, successful transitions mark PhaseRecovering while waiting between attempts,
// then return to PhaseRunning after a successful op (if recovery was entered).
func (r *RecoveryManager) CallWithRetry(ctx context.Context, state *LoopState, op func() error) error {
	if r == nil {
		return op()
	}

	var lastErr error
	delay := r.policy.InitialDelay
	var budgetUsed time.Duration

	for attempt := 1; attempt <= r.policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = op()
		if lastErr == nil {
			if state != nil && state.PhaseSafe() == PhaseRecovering {
				_ = state.TransitionTo(PhaseRunning, "recovered")
			}
			return nil
		}

		if !isRetryable(lastErr) {
			return lastErr
		}

		if attempt == r.policy.MaxAttempts {
			break
		}

		if r.obs != nil {
			r.obs.Warn("core/recovery: retrying after transient error",
				"attempt", attempt,
				"max", r.policy.MaxAttempts,
				"err", lastErr.Error(),
			)
		}

		jittered := applyJitter(delay, r.policy.JitterFraction)
		if r.policy.MaxRetryBudget > 0 {
			remaining := r.policy.MaxRetryBudget - budgetUsed
			if remaining <= 0 {
				if r.obs != nil {
					r.obs.Warn("core/recovery: retry budget exhausted", "budget", r.policy.MaxRetryBudget.String())
				}
				break
			}
			if jittered > remaining {
				jittered = remaining
			}
		}

		if state != nil && state.PhaseSafe() == PhaseRunning {
			_ = state.TransitionTo(PhaseRecovering, "transient_retry")
		}

		select {
		case <-ctx.Done():
			if state != nil && state.PhaseSafe() == PhaseRecovering {
				_ = state.TransitionTo(PhaseRunning, "retry_cancelled")
			}
			return ctx.Err()
		case <-time.After(jittered):
		}

		budgetUsed += jittered

		next := time.Duration(float64(delay) * 2)
		if next > r.policy.MaxDelay {
			next = r.policy.MaxDelay
		}
		delay = next
	}

	if state != nil && state.PhaseSafe() == PhaseRecovering {
		_ = state.TransitionTo(PhaseRunning, "retry_exhausted")
	}

	if lastErr == nil {
		return errors.New("core/recovery: exhausted retries")
	}
	return fmt.Errorf("core/recovery: exhausted retries: %w", lastErr)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "timeout") || strings.Contains(s, "timed out") {
		return true
	}
	if strings.Contains(s, "connection reset") || strings.Contains(s, "eof") {
		return true
	}
	if strings.Contains(s, "429") || strings.Contains(s, "rate limit") {
		return true
	}
	if strings.Contains(s, "503") || strings.Contains(s, "502") || strings.Contains(s, "504") {
		return true
	}
	if strings.Contains(s, "unavailable") {
		return true
	}
	return false
}

func applyJitter(d time.Duration, frac float64) time.Duration {
	if frac <= 0 || d <= 0 {
		return d
	}
	f := 1 + (rand.Float64()*2-1)*frac
	if f < 0.1 {
		f = 0.1
	}
	return time.Duration(float64(d) * f)
}
