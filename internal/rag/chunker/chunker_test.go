package chunker

import "testing"

func TestFixedSizeChunker(t *testing.T) {
	f, err := NewFixedSizeChunker(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	chunks := f.Chunk("doc", "abcdefghij", nil)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	want := []string{"abcd", "defg", "ghij"}
	for i, c := range chunks {
		if c.Content != want[i] {
			t.Fatalf("chunk[%d] = %q, want %q", i, c.Content, want[i])
		}
		if c.DocID != "doc" {
			t.Fatalf("chunk[%d].DocID = %q", i, c.DocID)
		}
	}
}

func TestFixedSizeChunkerShortAndEmpty(t *testing.T) {
	f, _ := NewFixedSizeChunker(10, 1)
	if got := f.Chunk("doc", "short", nil); len(got) != 1 || got[0].Content != "short" {
		t.Fatalf("short text chunks = %+v", got)
	}
	if got := f.Chunk("doc", "   ", nil); got != nil {
		t.Fatalf("empty text should return nil, got %+v", got)
	}
}

func TestFixedSizeChunkerValidation(t *testing.T) {
	if _, err := NewFixedSizeChunker(0, 0); err == nil {
		t.Fatalf("expected error for non-positive size")
	}
	if _, err := NewFixedSizeChunker(4, 4); err == nil {
		t.Fatalf("expected error for overlap >= size")
	}
}

func TestRecursiveChunkerSmallText(t *testing.T) {
	r := NewRecursiveChunker(512, 64)
	chunks := r.ChunkDocument("doc", "one two three", nil)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if chunks[0].DocID != "doc" || chunks[0].Content != "one two three" {
		t.Fatalf("chunk = %+v", chunks[0])
	}
}

func TestRecursiveChunkerEmpty(t *testing.T) {
	r := NewRecursiveChunker(512, 64)
	if got := r.ChunkDocument("doc", "", nil); got != nil {
		t.Fatalf("empty text should return nil, got %+v", got)
	}
}
