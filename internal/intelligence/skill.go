package intelligence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	defaultMaxSkills     = 256
	defaultMaxPromptChars = 80000
)

// SkillManager implements two-phase skill loading.
// Phase 1: Scan all skills/*/SKILL.md, parse YAML frontmatter only -> build index
// Phase 2: On demand, load the full skill body
type SkillManager struct {
	skillsDir      string
	index          []SkillMeta
	loaded         map[string]string // name -> full body (after frontmatter)
	maxSkills      int
	maxPromptChars int
	mu             sync.RWMutex
}

// SkillMeta holds parsed frontmatter for one skill.
type SkillMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Invocation  string `yaml:"invocation"`
	FilePath    string `yaml:"-"`
}

// NewSkillManager constructs a manager for skills under skillsDir (e.g. workspace/skills).
func NewSkillManager(skillsDir string) *SkillManager {
	return &SkillManager{
		skillsDir:      skillsDir,
		loaded:         make(map[string]string),
		maxSkills:      defaultMaxSkills,
		maxPromptChars: defaultMaxPromptChars,
	}
}

// SetScanLimits tunes how many skills appear in the index and the max size of formatted index text.
// Pass maxSkills < 0 to leave the current maxSkills unchanged; 0 resets to defaultMaxSkills.
func (m *SkillManager) SetScanLimits(maxSkills, maxPromptChars int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case maxSkills > 0:
		m.maxSkills = maxSkills
	case maxSkills == 0:
		m.maxSkills = defaultMaxSkills
	}
	if maxPromptChars > 0 {
		m.maxPromptChars = maxPromptChars
	}
}

// ScanSkills walks skillsDir/*/SKILL.md and rebuilds the index from YAML frontmatter only.
func (m *SkillManager) ScanSkills() error {
	if m == nil {
		return fmt.Errorf("intelligence: nil SkillManager")
	}
	if m.skillsDir == "" {
		return fmt.Errorf("intelligence: skillsDir is empty")
	}

	entries, err := os.ReadDir(m.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			m.mu.Lock()
			m.index = nil
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("read skills dir %s: %w", m.skillsDir, err)
	}

	m.mu.RLock()
	limit := m.maxSkills
	m.mu.RUnlock()
	if limit <= 0 {
		limit = defaultMaxSkills
	}

	next := make([]SkillMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(m.skillsDir, e.Name(), "SKILL.md")
		meta, err := m.readSkillMeta(skillPath, e.Name())
		if err != nil {
			return err
		}
		if meta.FilePath == "" {
			continue // no SKILL.md in this directory
		}
		if meta.Name == "" {
			meta.Name = e.Name()
		}
		meta.FilePath = skillPath
		next = append(next, meta)
		if len(next) >= limit {
			break
		}
	}

	m.mu.Lock()
	m.index = next
	m.loaded = make(map[string]string)
	m.mu.Unlock()
	return nil
}

func (m *SkillManager) readSkillMeta(path, dirName string) (SkillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillMeta{}, nil
		}
		return SkillMeta{}, fmt.Errorf("read %s: %w", path, err)
	}
	meta, _, err := parseFrontmatter(string(data))
	if err != nil {
		return SkillMeta{}, fmt.Errorf("parse frontmatter %s: %w", path, err)
	}
	if meta.Name == "" {
		meta.Name = dirName
	}
	meta.FilePath = path
	return meta, nil
}

// GetIndexPrompt returns a formatted skill index suitable for a system prompt.
func (m *SkillManager) GetIndexPrompt() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var b strings.Builder
	b.WriteString("<<< SKILL INDEX >>>\n")
	for _, s := range m.index {
		line := fmt.Sprintf("- name: %q\n  description: %s\n", s.Name, strings.TrimSpace(s.Description))
		if s.Invocation != "" {
			line += fmt.Sprintf("  invocation: %s\n", strings.TrimSpace(s.Invocation))
		}
		b.WriteString(line)
	}
	out := b.String()
	if m.maxPromptChars > 0 && len(out) > m.maxPromptChars {
		out = out[:m.maxPromptChars]
	}
	return out
}

// LoadSkill returns the markdown body (after frontmatter) for the named skill.
func (m *SkillManager) LoadSkill(name string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("intelligence: nil SkillManager")
	}
	if name == "" {
		return "", fmt.Errorf("intelligence: empty skill name")
	}

	m.mu.RLock()
	cached, ok := m.loaded[name]
	path := ""
	for _, meta := range m.index {
		if meta.Name == name {
			path = meta.FilePath
			break
		}
	}
	m.mu.RUnlock()
	if ok {
		return cached, nil
	}

	if path == "" {
		// Allow load by rescanning if index stale
		if err := m.ScanSkills(); err != nil {
			return "", err
		}
		m.mu.RLock()
		for _, meta := range m.index {
			if meta.Name == name {
				path = meta.FilePath
				break
			}
		}
		cached, ok = m.loaded[name]
		m.mu.RUnlock()
		if ok {
			return cached, nil
		}
		if path == "" {
			return "", fmt.Errorf("intelligence: unknown skill %q", name)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill %s: %w", path, err)
	}
	_, body, err := parseFrontmatter(string(data))
	if err != nil {
		return "", fmt.Errorf("parse skill %s: %w", path, err)
	}
	body = strings.TrimSpace(body)
	if m.maxPromptChars > 0 && len(body) > m.maxPromptChars {
		body = body[:m.maxPromptChars]
	}

	m.mu.Lock()
	m.loaded[name] = body
	m.mu.Unlock()
	return body, nil
}

// ListSkills returns a shallow copy of the current index.
func (m *SkillManager) ListSkills() []SkillMeta {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SkillMeta, len(m.index))
	copy(out, m.index)
	return out
}

// ListSkillSummaries returns [name, description] pairs for all indexed skills.
// Satisfies the builtin.SkillLister interface for tool registration.
func (m *SkillManager) ListSkillSummaries() [][2]string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([][2]string, 0, len(m.index))
	for _, s := range m.index {
		out = append(out, [2]string{s.Name, s.Description})
	}
	return out
}

// parseFrontmatter extracts YAML between --- delimiters at the start of the file.
// Returns metadata, remaining body, and an error if YAML is invalid.
func parseFrontmatter(content string) (SkillMeta, string, error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return SkillMeta{}, strings.TrimSpace(content), nil
	}

	var yamlLines []string
	i := 1
	for i < len(lines) {
		line := lines[i]
		i++
		if strings.TrimSpace(line) == "---" {
			break
		}
		yamlLines = append(yamlLines, line)
	}
	body := strings.Join(lines[i:], "\n")

	var meta SkillMeta
	if len(yamlLines) > 0 {
		if err := yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &meta); err != nil {
			return SkillMeta{}, "", fmt.Errorf("yaml frontmatter: %w", err)
		}
	}
	return meta, body, nil
}
