package memory

import (
	"fmt"

	"github.com/rainea/nexus/pkg/types"
)

// CompactionStrategy defines how to compact memory when token budget is exceeded.
type CompactionStrategy interface {
	ShouldCompact(tokenCount, threshold int) bool
	Compact(conv *ConversationMemory) error
}

// WindowCompaction keeps only the last N messages (older messages are discarded).
type WindowCompaction struct {
	Keep int
}

// ShouldCompact returns true when tokenCount meets or exceeds threshold.
func (w *WindowCompaction) ShouldCompact(tokenCount, threshold int) bool {
	return tokenCount >= threshold
}

// Compact truncates the message list to the last Keep messages.
func (w *WindowCompaction) Compact(conv *ConversationMemory) error {
	if conv == nil {
		return fmt.Errorf("compaction: conversation is nil")
	}
	keepN := w.Keep
	if keepN < 1 {
		keepN = 1
	}
	conv.mu.Lock()
	defer conv.mu.Unlock()

	if len(conv.messages) <= keepN {
		return nil
	}
	trimmed := make([]types.Message, keepN)
	copy(trimmed, conv.messages[len(conv.messages)-keepN:])
	conv.messages = trimmed
	return nil
}

// SummaryCompaction summarizes older messages into the summary list, retaining the sliding window.
type SummaryCompaction struct {
	Summarize func([]types.Message) string
}

// ShouldCompact returns true when tokenCount meets or exceeds threshold.
func (s *SummaryCompaction) ShouldCompact(tokenCount, threshold int) bool {
	return tokenCount >= threshold
}

// Compact delegates to ConversationMemory.Compact using the configured summarizer.
func (s *SummaryCompaction) Compact(conv *ConversationMemory) error {
	if conv == nil {
		return fmt.Errorf("compaction: conversation is nil")
	}
	if s.Summarize == nil {
		return fmt.Errorf("summary compaction: Summarize is nil")
	}
	conv.Compact(s.Summarize)
	return nil
}

// AggressiveCompaction keeps only the last three messages; existing summaries are preserved.
type AggressiveCompaction struct{}

// ShouldCompact returns true when tokenCount meets or exceeds threshold.
func (a *AggressiveCompaction) ShouldCompact(tokenCount, threshold int) bool {
	return tokenCount >= threshold
}

// Compact drops all but the last three messages.
func (a *AggressiveCompaction) Compact(conv *ConversationMemory) error {
	if conv == nil {
		return fmt.Errorf("compaction: conversation is nil")
	}
	const keep = 3
	conv.mu.Lock()
	defer conv.mu.Unlock()

	if len(conv.messages) <= keep {
		return nil
	}
	trimmed := make([]types.Message, keep)
	copy(trimmed, conv.messages[len(conv.messages)-keep:])
	conv.messages = trimmed
	return nil
}
