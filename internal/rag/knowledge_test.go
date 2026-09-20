package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestKnowledgeManagerAddDocument(t *testing.T) {
	m := NewKnowledgeManager(nil)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateKnowledgeBase(context.Background(), "kb1", root); err != nil {
		t.Fatal(err)
	}
	if err := m.AddDocument(context.Background(), "kb1", "doc.md", false); err != nil {
		t.Fatal(err)
	}

	docs, err := m.ListDocuments("kb1")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Status != StatusPending {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestKnowledgeManagerRejectsPathTraversal(t *testing.T) {
	m := NewKnowledgeManager(nil)
	root := t.TempDir()
	if _, err := m.CreateKnowledgeBase(context.Background(), "kb1", root); err != nil {
		t.Fatal(err)
	}

	if err := m.AddDocument(context.Background(), "kb1", "../escape.md", false); err == nil {
		t.Fatalf("expected traversal rejection, got nil")
	}
}

func TestKnowledgeManagerUnknownBase(t *testing.T) {
	m := NewKnowledgeManager(nil)
	if err := m.AddDocument(context.Background(), "nope", "x.md", false); err == nil {
		t.Fatalf("expected error for unknown base")
	}
	if _, err := m.CreateKnowledgeBase(context.Background(), "kb", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatalf("expected error for missing root dir")
	}
}
