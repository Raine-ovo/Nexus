package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// LoopPhase represents the high-level state of the ReAct loop.
// Coarse-grained phases let observability and guards reason about lifecycle without coupling
// to every internal branch. Names align with the Nexus agent state machine design.
type LoopPhase string

const (
	PhaseIdle          LoopPhase = "idle"
	PhaseRunning       LoopPhase = "running"
	PhaseToolExecution LoopPhase = "tool_execution"
	PhaseCompacting    LoopPhase = "compacting"
	PhaseRecovering    LoopPhase = "recovering"
	PhaseCompleted     LoopPhase = "completed"
	PhaseError         LoopPhase = "error"
)

// allowedTransitions encodes valid phase moves.
//
// Design: compacting and recovering are always entered from running (or from tool_execution
// transitioning back to running is separate). Idle may only start a run or fail early.
// Terminal phases completed/error accept no further transitions except Reset().
//
// Explicitly disallowed: idle -> compacting (must run first), idle -> tool_execution, etc.
var allowedTransitions = map[LoopPhase][]LoopPhase{
	PhaseIdle:          {PhaseRunning, PhaseError},
	PhaseRunning:       {PhaseToolExecution, PhaseCompacting, PhaseRecovering, PhaseCompleted, PhaseError},
	PhaseToolExecution: {PhaseRunning, PhaseRecovering, PhaseError},
	PhaseCompacting:    {PhaseRunning, PhaseError},
	PhaseRecovering:    {PhaseRunning, PhaseError},
	PhaseCompleted:     {},
	PhaseError:         {},
}

// TransitionAllowed reports whether a transition is valid without mutating state.
func TransitionAllowed(from, to LoopPhase) bool {
	allowed, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}
	return false
}

// LoopState is the authoritative in-memory state for a single RunLoop invocation.
// It is not persisted; callers that need durability should snapshot Messages externally.
type LoopState struct {
	mu sync.RWMutex

	Phase            LoopPhase
	Messages         []types.Message
	TurnCount        int
	CurrentIteration int
	TransitionReason string
	StartedAt        time.Time
	CompletedAt      time.Time

	// Token accounting (estimated via pkg/utils, not provider-reported usage).
	EstimatedPromptTokens   int
	EstimatedResponseTokens int
	LastCompactionAt        time.Time
	CompactionCount         int
}

// NewLoopState constructs an idle state machine ready for a new conversation turn.
func NewLoopState() *LoopState {
	return &LoopState{
		Phase:     PhaseIdle,
		Messages:  make([]types.Message, 0, 16),
		StartedAt: time.Now(),
	}
}

// Reset clears messages and counters while returning to idle. Used when embedding the same
// agent instance across unrelated sessions (optional caller behavior).
func (s *LoopState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = PhaseIdle
	s.Messages = s.Messages[:0]
	s.TurnCount = 0
	s.CurrentIteration = 0
	s.TransitionReason = ""
	s.EstimatedPromptTokens = 0
	s.EstimatedResponseTokens = 0
	s.CompactionCount = 0
	s.LastCompactionAt = time.Time{}
	s.StartedAt = time.Now()
	s.CompletedAt = time.Time{}
}

// PhaseSafe returns the current phase with read lock.
func (s *LoopState) PhaseSafe() LoopPhase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Phase
}

// TransitionTo moves the state machine to next if allowed; reason is stored for debugging.
func (s *LoopState) TransitionTo(next LoopPhase, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.Phase
	allowed, ok := allowedTransitions[cur]
	if !ok {
		return fmt.Errorf("core/state: unknown phase %q", cur)
	}
	for _, a := range allowed {
		if a == next {
			s.Phase = next
			s.TransitionReason = reason
			if next == PhaseCompleted || next == PhaseError {
				s.CompletedAt = time.Now()
			}
			return nil
		}
	}
	return fmt.Errorf("core/state: invalid transition %q -> %q (%s)", cur, next, reason)
}

// MessageCount returns the number of transcript messages.
func (s *LoopState) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages)
}

// AppendMessage appends a copy with fresh ID/timestamp if unset.
func (s *LoopState) AppendMessage(m types.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	s.Messages = append(s.Messages, m)
	s.recomputeTokensLocked()
}

// MessagesSnapshot returns a shallow copy of messages for read-only iteration outside the lock.
func (s *LoopState) MessagesSnapshot() []types.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.Message, len(s.Messages))
	copy(out, s.Messages)
	return out
}

// SetMessages replaces the entire transcript (used after compaction). Caller must supply
// coherent ordering (system first if used, then user/assistant/tool).
func (s *LoopState) SetMessages(msgs []types.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append([]types.Message(nil), msgs...)
	s.recomputeTokensLocked()
}

// BumpIteration increments the outer loop counter (one LLM call = one iteration).
func (s *LoopState) BumpIteration() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentIteration++
}

// BumpTurn increments logical user/assistant turns (optional metric).
func (s *LoopState) BumpTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TurnCount++
}

// EstimatedTotalTokens returns the sum of last estimated prompt+response accounting.
func (s *LoopState) EstimatedTotalTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.EstimatedPromptTokens + s.EstimatedResponseTokens
}

// recomputeTokensLocked estimates tokens from message contents and tool JSON payloads.
func (s *LoopState) recomputeTokensLocked() {
	var contents []string
	for _, m := range s.Messages {
		contents = append(contents, m.Content)
		for _, tc := range m.ToolCalls {
			contents = append(contents, tc.Name)
			contents = append(contents, fmt.Sprintf("%v", tc.Arguments))
		}
		if m.ToolID != "" {
			contents = append(contents, m.ToolID)
		}
	}
	total := utils.EstimateMessagesTokens(contents)
	// Split heuristic: treat bulk as prompt-side context for guard thresholds.
	s.EstimatedPromptTokens = total
	s.EstimatedResponseTokens = 0
}

// RecordResponseTokens adds tokens attributed to the latest model generation.
func (s *LoopState) RecordResponseTokens(tokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EstimatedResponseTokens += tokens
}

// MarkCompaction records that a compaction pass ran.
func (s *LoopState) MarkCompaction() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CompactionCount++
	s.LastCompactionAt = time.Now()
}

// LastTransitionReason returns the reason string from the most recent successful transition.
func (s *LoopState) LastTransitionReason() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TransitionReason
}

// RoleCounts returns how many messages exist per role in the current transcript.
func (s *LoopState) RoleCounts() map[types.Role]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[types.Role]int)
	for _, m := range s.Messages {
		out[m.Role]++
	}
	return out
}

// LastAssistantText returns the content of the most recent assistant message without tool calls, or "".
func (s *LoopState) LastAssistantText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		if m.Role == types.RoleAssistant && len(m.ToolCalls) == 0 && m.Content != "" {
			return m.Content
		}
	}
	return ""
}
