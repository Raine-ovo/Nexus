package memory

import (
	"fmt"
	"strings"

	"github.com/rainea/nexus/configs"
)

// Manager unifies conversation memory and semantic memory.
type Manager struct {
	cfg          configs.MemoryConfig
	conversation *ConversationMemory
	semantic     *SemanticStore
}

// NewManager constructs memory subsystems from config.
func NewManager(cfg configs.MemoryConfig) (*Manager, error) {
	sem, err := NewSemanticStore(cfg.SemanticFile, cfg.MaxSemanticEntries)
	if err != nil {
		return nil, fmt.Errorf("semantic store: %w", err)
	}
	return &Manager{
		cfg:          cfg,
		conversation: NewConversationMemory(cfg.ConversationWindow),
		semantic:     sem,
	}, nil
}

// GetConversation returns the sliding-window conversation buffer.
func (m *Manager) GetConversation() *ConversationMemory {
	return m.conversation
}

// GetSemantic returns the YAML-backed semantic store.
func (m *Manager) GetSemantic() *SemanticStore {
	return m.semantic
}

// Flush persists all durable memory (semantic store).
func (m *Manager) Flush() error {
	if m.semantic == nil {
		return nil
	}
	return m.semantic.Save()
}

// BuildPromptSection formats memory for system prompt injection.
func (m *Manager) BuildPromptSection() string {
	var parts []string

	if m.conversation != nil {
		sums := m.conversation.Summaries()
		if len(sums) > 0 {
			var b strings.Builder
			b.WriteString("## Earlier conversation (summarized)\n")
			for i, s := range sums {
				b.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(s)))
			}
			parts = append(parts, strings.TrimSuffix(b.String(), "\n"))
		}
	}

	if m.semantic != nil {
		if sec := m.semantic.ToPromptSection(); sec != "" {
			parts = append(parts, sec)
		}
	}

	return strings.Join(parts, "\n\n")
}
