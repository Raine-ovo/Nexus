package intelligence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	content := "---\nname: go-review\ndescription: review go code\ninvocation: on demand\n---\n# Body\nactual body"
	meta, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("parseFrontmatter: %v", err)
	}
	if meta.Name != "go-review" || meta.Description != "review go code" {
		t.Fatalf("meta = %+v", meta)
	}
	if !strings.Contains(body, "actual body") {
		t.Fatalf("body = %q", body)
	}
}

func TestParseFrontmatterNoDelimiter(t *testing.T) {
	meta, body, err := parseFrontmatter("just body text")
	if err != nil {
		t.Fatalf("parseFrontmatter: %v", err)
	}
	if meta.Name != "" {
		t.Fatalf("expected empty meta, got %+v", meta)
	}
	if body != "just body text" {
		t.Fatalf("body = %q", body)
	}
}

func TestSkillManagerScanAndLoad(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "go-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: go-review\ndescription: review go code\n---\nbody text"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewSkillManager(dir)
	if err := m.ScanSkills(); err != nil {
		t.Fatal(err)
	}
	skills := m.ListSkills()
	if len(skills) != 1 || skills[0].Name != "go-review" {
		t.Fatalf("skills = %+v", skills)
	}

	body, err := m.LoadSkill("go-review")
	if err != nil {
		t.Fatal(err)
	}
	if body != "body text" {
		t.Fatalf("body = %q", body)
	}

	if !strings.Contains(m.GetIndexPrompt(), "go-review") {
		t.Fatalf("index prompt missing skill: %q", m.GetIndexPrompt())
	}
}

func TestSkillManagerMissingDir(t *testing.T) {
	m := NewSkillManager(filepath.Join(t.TempDir(), "does-not-exist"))
	if err := m.ScanSkills(); err != nil {
		t.Fatalf("ScanSkills on missing dir should not error: %v", err)
	}
	if len(m.ListSkills()) != 0 {
		t.Fatalf("expected empty index, got %+v", m.ListSkills())
	}
}
