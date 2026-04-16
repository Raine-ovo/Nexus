package memory

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// ConversationMemory implements sliding window + LLM summarization.
type ConversationMemory struct {
	messages   []types.Message
	summaries  []string
	windowSize int
	mu         sync.RWMutex
}

// NewConversationMemory creates a conversation buffer with the given window size.
func NewConversationMemory(windowSize int) *ConversationMemory {
	if windowSize < 1 {
		windowSize = 1
	}
	return &ConversationMemory{
		messages:   make([]types.Message, 0),
		summaries:  make([]string, 0),
		windowSize: windowSize,
	}
}

// Add appends a message, assigning ID and CreatedAt when unset.
func (c *ConversationMemory) Add(msg types.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	c.messages = append(c.messages, msg)
}

// GetRecent returns up to n most recent messages (newest last).
func (c *ConversationMemory) GetRecent(n int) []types.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if n <= 0 || len(c.messages) == 0 {
		return nil
	}
	if n >= len(c.messages) {
		out := make([]types.Message, len(c.messages))
		copy(out, c.messages)
		return out
	}
	start := len(c.messages) - n
	out := make([]types.Message, n)
	copy(out, c.messages[start:])
	return out
}

// GetAll returns a shallow copy of all messages in order.
func (c *ConversationMemory) GetAll() []types.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]types.Message, len(c.messages))
	copy(out, c.messages)
	return out
}

// Compact summarizes messages that fall outside the sliding window using summarizer.
// Summaries are appended; only the last windowSize messages are retained.
func (c *ConversationMemory) Compact(summarizer func([]types.Message) string) {
	if summarizer == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.messages) <= c.windowSize {
		return
	}

	old := c.messages[:len(c.messages)-c.windowSize]
	keep := make([]types.Message, c.windowSize)
	copy(keep, c.messages[len(c.messages)-c.windowSize:])

	summary := summarizer(old)
	if summary != "" {
		c.summaries = append(c.summaries, summary)
	}
	c.messages = keep
}

// Summaries returns a copy of accumulated summary strings.
func (c *ConversationMemory) Summaries() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]string, len(c.summaries))
	copy(out, c.summaries)
	return out
}

// EstimateTokens estimates total tokens for summaries and message contents.
func (c *ConversationMemory) EstimateTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := 0
	for _, s := range c.summaries {
		total += utils.EstimateTokens(s)
	}
	for _, m := range c.messages {
		total += utils.EstimateTokens(string(m.Role))
		total += utils.EstimateTokens(m.Content)
		total += utils.EstimateTokens(m.ToolID)
		for _, tc := range m.ToolCalls {
			total += utils.EstimateTokens(tc.Name)
			total += utils.EstimateTokens(tc.ID)
			if tc.Arguments != nil {
				if b, err := json.Marshal(tc.Arguments); err == nil {
					total += utils.EstimateTokens(string(b))
				}
			}
		}
	}
	return total
}

// Clear removes all messages and summaries.
func (c *ConversationMemory) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = c.messages[:0]
	c.summaries = c.summaries[:0]
}

// WindowSize returns the configured sliding window size.
func (c *ConversationMemory) WindowSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.windowSize
}
