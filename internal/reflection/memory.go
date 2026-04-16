package reflection

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type reflectionFile struct {
	Reflections []Reflection `yaml:"reflections"`
}

// ReflectionMemory persists episodic reflections as YAML, partitioned by level.
// Design mirrors memory.SemanticStore for consistency but adds similarity-based
// retrieval (keyword overlap) to surface relevant past failures during prospection.
type ReflectionMemory struct {
	entries    []Reflection
	filePath   string
	maxEntries int
	mu         sync.RWMutex
}

// NewReflectionMemory loads from file if present; otherwise starts empty.
func NewReflectionMemory(filePath string, maxEntries int) (*ReflectionMemory, error) {
	if maxEntries < 1 {
		maxEntries = 200
	}
	m := &ReflectionMemory{
		filePath:   filePath,
		maxEntries: maxEntries,
		entries:    make([]Reflection, 0),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// Store persists a reflection, enforcing maxEntries by evicting oldest micro-level first.
func (m *ReflectionMemory) Store(r Reflection) error {
	if r.ID == "" {
		return fmt.Errorf("reflection: id is required")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, r)
	m.evictLocked()
	return m.saveLocked()
}

// SearchRelevant returns reflections most relevant to the query by keyword overlap,
// optionally filtered by level. Results are sorted by relevance score descending.
func (m *ReflectionMemory) SearchRelevant(query string, maxResults int, level ReflectionLevel) []Reflection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if maxResults <= 0 {
		maxResults = 5
	}
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	type scored struct {
		ref   Reflection
		score float64
	}
	var candidates []scored

	for _, e := range m.entries {
		if level != "" && e.Level != level {
			continue
		}
		docTokens := tokenize(e.Insight + " " + e.ErrorPattern + " " + e.Suggestion + " " + e.TaskType)
		overlap := jaccardSimilarity(queryTokens, docTokens)
		if overlap > 0 {
			candidates = append(candidates, scored{ref: e, score: overlap})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	n := maxResults
	if n > len(candidates) {
		n = len(candidates)
	}
	out := make([]Reflection, n)
	for i := 0; i < n; i++ {
		out[i] = candidates[i].ref
	}
	return out
}

// ByLevel returns all reflections at the given level, newest first.
func (m *ReflectionMemory) ByLevel(level ReflectionLevel) []Reflection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Reflection
	for _, e := range m.entries {
		if e.Level == level {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Count returns total number of stored reflections.
func (m *ReflectionMemory) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// ToPromptSection formats reflections for system prompt injection.
func (m *ReflectionMemory) ToPromptSection(maxReflections int) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.entries) == 0 {
		return ""
	}

	sorted := make([]Reflection, len(m.entries))
	copy(sorted, m.entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})

	n := maxReflections
	if n <= 0 || n > len(sorted) {
		n = len(sorted)
	}
	if n > 20 {
		n = 20
	}

	var b strings.Builder
	b.WriteString("## Past reflections (episodic memory)\n")
	for i := 0; i < n; i++ {
		r := sorted[i]
		b.WriteString(fmt.Sprintf("- [%s/%s] %s → %s\n", r.Level, r.TaskType, r.ErrorPattern, r.Suggestion))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// --- persistence ---

func (m *ReflectionMemory) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f reflectionFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("reflection yaml: %w", err)
	}
	m.entries = append([]Reflection(nil), f.Reflections...)
	m.evictLocked()
	return nil
}

func (m *ReflectionMemory) saveLocked() error {
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("reflection mkdir: %w", err)
	}
	f := reflectionFile{Reflections: m.entries}
	data, err := yaml.Marshal(&f)
	if err != nil {
		return fmt.Errorf("reflection marshal: %w", err)
	}
	tmp := m.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("reflection write tmp: %w", err)
	}
	if err := os.Rename(tmp, m.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("reflection rename: %w", err)
	}
	return nil
}

// evictLocked drops oldest micro entries first, then meso, preserving macro longest.
func (m *ReflectionMemory) evictLocked() {
	if len(m.entries) <= m.maxEntries {
		return
	}
	sort.SliceStable(m.entries, func(i, j int) bool {
		return evictionPriority(m.entries[i]) < evictionPriority(m.entries[j])
	})
	excess := len(m.entries) - m.maxEntries
	m.entries = m.entries[excess:]
}

// evictionPriority: lower = evicted sooner. Macro insights have highest retention.
func evictionPriority(r Reflection) float64 {
	var levelWeight float64
	switch r.Level {
	case LevelMicro:
		levelWeight = 0
	case LevelMeso:
		levelWeight = 1000
	case LevelMacro:
		levelWeight = 2000
	default:
		levelWeight = 0
	}
	return levelWeight + float64(r.CreatedAt.Unix())
}

// --- text utilities ---

func tokenize(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:!?\"'()[]{}") // strip punctuation
		if len(w) > 1 {
			tokens[w] = struct{}{}
		}
	}
	return tokens
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return math.Round(float64(intersection)/float64(union)*1000) / 1000
}
