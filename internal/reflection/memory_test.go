package reflection

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReflectionMemoryStoreSearch(t *testing.T) {
	m, err := NewReflectionMemory(filepath.Join(t.TempDir(), "ref.yaml"), 10)
	if err != nil {
		t.Fatal(err)
	}
	err = m.Store(Reflection{
		ID:           "1",
		Level:        LevelMicro,
		TaskType:     "code",
		ErrorPattern: "nil pointer dereference",
		Insight:      "missing nil check before deref",
		Suggestion:   "add a nil guard",
		CreatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	res := m.SearchRelevant("nil pointer", 5, "")
	if len(res) != 1 || res[0].ID != "1" {
		t.Fatalf("SearchRelevant = %+v", res)
	}

	if got := m.SearchRelevant("unrelated topic", 5, ""); len(got) != 0 {
		t.Fatalf("expected no matches, got %+v", got)
	}
}

func TestReflectionMemoryEvictionKeepsMacro(t *testing.T) {
	m, err := NewReflectionMemory(filepath.Join(t.TempDir(), "ref.yaml"), 2)
	if err != nil {
		t.Fatal(err)
	}
	must := func(r Reflection) {
		t.Helper()
		if err := m.Store(r); err != nil {
			t.Fatal(err)
		}
	}
	must(Reflection{ID: "micro", Level: LevelMicro, TaskType: "t", CreatedAt: time.Now().Add(-3 * time.Hour)})
	must(Reflection{ID: "meso", Level: LevelMeso, TaskType: "t", CreatedAt: time.Now().Add(-2 * time.Hour)})
	must(Reflection{ID: "macro", Level: LevelMacro, TaskType: "t", CreatedAt: time.Now().Add(-1 * time.Hour)})

	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}
	if got := m.ByLevel(LevelMacro); len(got) != 1 || got[0].ID != "macro" {
		t.Fatalf("macro should survive eviction, got %+v", got)
	}
	if got := m.ByLevel(LevelMicro); len(got) != 0 {
		t.Fatalf("micro should be evicted first, got %+v", got)
	}
}

func TestReflectionMemoryRequiresID(t *testing.T) {
	m, err := NewReflectionMemory(filepath.Join(t.TempDir(), "ref.yaml"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store(Reflection{Level: LevelMicro}); err == nil {
		t.Fatalf("expected error for missing ID")
	}
}
