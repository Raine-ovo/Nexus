package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Semantic memory categories.
const (
	CategoryProject    = "project"
	CategoryPreference = "preference"
	CategoryFeedback   = "feedback"
	CategoryReference  = "reference"
)

// SemanticEntry is one long-term memory fact stored as YAML.
type SemanticEntry struct {
	Category  string    `yaml:"category" json:"category"`
	Key       string    `yaml:"key" json:"key"`
	Value     string    `yaml:"value" json:"value"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
}

type semanticFile struct {
	Entries []SemanticEntry `yaml:"entries"`
}

// SemanticStore is long-term memory persisted as YAML.
// Categories: project, preference, feedback, reference.
type SemanticStore struct {
	entries    []SemanticEntry
	filePath   string
	maxEntries int
	mu         sync.RWMutex
}

// NewSemanticStore loads from file if present; otherwise starts empty.
func NewSemanticStore(filePath string, maxEntries int) (*SemanticStore, error) {
	if maxEntries < 1 {
		maxEntries = 1
	}
	s := &SemanticStore{
		filePath:   filePath,
		maxEntries: maxEntries,
		entries:    make([]SemanticEntry, 0),
	}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Add inserts an entry, enforcing maxEntries by dropping oldest entries.
func (s *SemanticStore) Add(category, key, value string) error {
	if key == "" {
		return fmt.Errorf("semantic: key is required")
	}
	category = strings.TrimSpace(strings.ToLower(category))
	if category == "" {
		return fmt.Errorf("semantic: category is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.entries = append(s.entries, SemanticEntry{
		Category:  category,
		Key:       key,
		Value:     value,
		CreatedAt: now,
	})
	s.trimLocked()
	return s.saveLocked()
}

// Search returns entries matching query (substring, case-insensitive) in key or value.
// If category is non-empty, only entries in that category are considered.
func (s *SemanticStore) Search(query, category string) []SemanticEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))
	cat := strings.TrimSpace(strings.ToLower(category))
	var out []SemanticEntry
	for _, e := range s.entries {
		if cat != "" && !strings.EqualFold(e.Category, cat) {
			continue
		}
		if q == "" {
			out = append(out, e)
			continue
		}
		if strings.Contains(strings.ToLower(e.Key), q) ||
			strings.Contains(strings.ToLower(e.Value), q) ||
			strings.Contains(strings.ToLower(e.Category), q) {
			out = append(out, e)
		}
	}
	return out
}

// Delete removes all entries with the given key.
func (s *SemanticStore) Delete(key string) error {
	if key == "" {
		return fmt.Errorf("semantic: key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := s.entries[:0]
	for _, e := range s.entries {
		if e.Key != key {
			filtered = append(filtered, e)
		}
	}
	s.entries = filtered
	return s.saveLocked()
}

// Load reads the YAML file from disk.
func (s *SemanticStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f semanticFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("semantic yaml: %w", err)
	}
	s.entries = append([]SemanticEntry(nil), f.Entries...)
	s.trimLocked()
	return s.saveLocked()
}

// Save writes entries to YAML atomically.
func (s *SemanticStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *SemanticStore) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("semantic mkdir: %w", err)
	}
	f := semanticFile{Entries: s.entries}
	data, err := yaml.Marshal(&f)
	if err != nil {
		return fmt.Errorf("semantic marshal: %w", err)
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("semantic write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("semantic rename: %w", err)
	}
	return nil
}

func (s *SemanticStore) trimLocked() {
	if len(s.entries) <= s.maxEntries {
		return
	}
	sort.Slice(s.entries, func(i, j int) bool {
		return s.entries[i].CreatedAt.Before(s.entries[j].CreatedAt)
	})
	excess := len(s.entries) - s.maxEntries
	s.entries = s.entries[excess:]
}

// ToPromptSection formats semantic memory for system prompt injection.
func (s *SemanticStore) ToPromptSection() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Long-term memory (semantic)\n")
	for _, e := range s.entries {
		b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", e.Category, e.Key, e.Value))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Entries returns a shallow copy of all entries (sorted by CreatedAt for stability).
func (s *SemanticStore) Entries() []SemanticEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]SemanticEntry, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// FilePath returns the configured persistence path.
func (s *SemanticStore) FilePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filePath
}
