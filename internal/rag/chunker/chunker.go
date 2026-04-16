package chunker

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Chunk is a piece of a document with position info.
type Chunk struct {
	ID          string
	Content     string
	DocID       string
	StartOffset int
	EndOffset   int
	Metadata    map[string]interface{}
}

// RecursiveChunker splits text using a hierarchy of separators (largest first).
type RecursiveChunker struct {
	Separators []string
	ChunkSize  int
	Overlap    int
}

// NewRecursiveChunker returns a chunker with sensible defaults.
func NewRecursiveChunker(chunkSize, overlap int) *RecursiveChunker {
	if chunkSize <= 0 {
		chunkSize = 512
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}
	return &RecursiveChunker{
		Separators: []string{"\n\n", "\n", ". ", " ", ""},
		ChunkSize:  chunkSize,
		Overlap:    overlap,
	}
}

// ChunkDocument splits doc content into chunks tagged with docID.
func (r *RecursiveChunker) ChunkDocument(docID, text string, baseMeta map[string]interface{}) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	segments := r.splitToMaxSize(text, 0)
	var chunks []Chunk
	cursor := 0
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		for _, w := range windowWithOverlap(seg, r.ChunkSize, r.Overlap) {
			w = strings.TrimSpace(w)
			if w == "" {
				continue
			}
			start := strings.Index(text[cursor:], w)
			if start < 0 {
				start = strings.Index(text, w)
			}
			if start < 0 {
				start = cursor
			} else if start < cursor {
				// overlap window: allow non-monotonic match
			} else {
				cursor = start
			}
			end := start + len(w)
			if end > cursor {
				cursor = end
			}
			md := cloneMeta(baseMeta)
			chunks = append(chunks, Chunk{
				ID:          uuid.NewString(),
				Content:     w,
				DocID:       docID,
				StartOffset: start,
				EndOffset:   end,
				Metadata:    md,
			})
		}
	}
	return chunks
}

func (r *RecursiveChunker) splitToMaxSize(s string, sepIdx int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) <= r.ChunkSize {
		return []string{s}
	}
	if sepIdx >= len(r.Separators) {
		return windowStrings(s, r.ChunkSize, r.Overlap)
	}
	sep := r.Separators[sepIdx]
	if sep == "" {
		return windowStrings(s, r.ChunkSize, r.Overlap)
	}
	parts := strings.Split(s, sep)
	var out []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		out = append(out, buf.String())
		buf.Reset()
	}
	for i, p := range parts {
		if i > 0 && buf.Len() > 0 {
			buf.WriteString(sep)
		}
		if len(p) > r.ChunkSize {
			flush()
			out = append(out, r.splitToMaxSize(p, sepIdx+1)...)
			continue
		}
		if buf.Len()+len(p) > r.ChunkSize && buf.Len() > 0 {
			flush()
		}
		buf.WriteString(p)
	}
	flush()
	return out
}

func windowStrings(s string, size, overlap int) []string {
	var res []string
	step := size - overlap
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(s); i += step {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		res = append(res, s[i:end])
		if end == len(s) {
			break
		}
	}
	return res
}

func windowWithOverlap(s string, size, overlap int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) <= size {
		return []string{s}
	}
	return windowStrings(s, size, overlap)
}

// FixedSizeChunker splits by character count with overlap.
type FixedSizeChunker struct {
	Size    int
	Overlap int
}

// NewFixedSizeChunker validates size and overlap.
func NewFixedSizeChunker(size, overlap int) (*FixedSizeChunker, error) {
	if size <= 0 {
		return nil, fmt.Errorf("chunker: size must be positive")
	}
	if overlap < 0 || overlap >= size {
		return nil, fmt.Errorf("chunker: overlap must be in [0, size)")
	}
	return &FixedSizeChunker{Size: size, Overlap: overlap}, nil
}

// Chunk produces chunks from text with docID in metadata.
func (f *FixedSizeChunker) Chunk(docID, text string, baseMeta map[string]interface{}) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []Chunk
	step := f.Size - f.Overlap
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(text); i += step {
		end := i + f.Size
		if end > len(text) {
			end = len(text)
		}
		sub := strings.TrimSpace(text[i:end])
		if sub == "" {
			continue
		}
		md := cloneMeta(baseMeta)
		chunks = append(chunks, Chunk{
			ID:          uuid.NewString(),
			Content:     sub,
			DocID:       docID,
			StartOffset: i,
			EndOffset:   end,
			Metadata:    md,
		})
		if end == len(text) {
			break
		}
	}
	return chunks
}

func cloneMeta(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
