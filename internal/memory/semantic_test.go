package memory

import (
	"path/filepath"
	"testing"
)

func TestSemanticStoreAddSearchDelete(t *testing.T) {
	s, err := NewSemanticStore(filepath.Join(t.TempDir(), "sem.yaml"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("project", "language", "Go"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("preference", "style", "concise"); err != nil {
		t.Fatal(err)
	}

	if got := len(s.Search("go", "")); got != 1 {
		t.Fatalf("Search(go) = %d, want 1", got)
	}
	if got := len(s.Search("", "project")); got != 1 {
		t.Fatalf("Search(project) = %d, want 1", got)
	}
	if got := len(s.Search("zzz", "")); got != 0 {
		t.Fatalf("Search(zzz) = %d, want 0", got)
	}

	if err := s.Delete("language"); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("after delete got %d entries, want 1", got)
	}
}

func TestSemanticStoreTrimToMaxEntries(t *testing.T) {
	s, err := NewSemanticStore(filepath.Join(t.TempDir(), "sem.yaml"), 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Add("project", string(rune('a'+i)), "v"); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(s.Entries()); got != 2 {
		t.Fatalf("after trim got %d entries, want 2", got)
	}
}

func TestSemanticStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sem.yaml")
	s, err := NewSemanticStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("project", "key", "value"); err != nil {
		t.Fatal(err)
	}

	s2, err := NewSemanticStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s2.Search("value", "")); got != 1 {
		t.Fatalf("reloaded search = %d, want 1", got)
	}
}
