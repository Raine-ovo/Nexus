package chunker

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	paragraphBreakSem = regexp.MustCompile(`\n\s*\n`)
	codeFenceExtract  = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\n(.*?)```")
)

// SemanticChunker splits on paragraph boundaries and keeps code blocks as atomic units.
type SemanticChunker struct {
	MaxParagraphRunes int
}

// NewSemanticChunker sets max runes before forcing a split inside long paragraphs.
func NewSemanticChunker(maxParagraphRunes int) *SemanticChunker {
	if maxParagraphRunes <= 0 {
		maxParagraphRunes = 800
	}
	return &SemanticChunker{MaxParagraphRunes: maxParagraphRunes}
}

// Chunk extracts fenced code blocks as atomic chunks, then paragraph-splits the remainder.
// Metadata is enriched with file path and the latest preceding markdown header.
func (s *SemanticChunker) Chunk(docID, text string, filePath string, baseMeta map[string]interface{}) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var chunks []Chunk
	remaining := text
	header := ""
	for {
		all := codeFenceExtract.FindStringSubmatchIndex(remaining)
		if all == nil {
			break
		}
		start, end := all[0], all[1]
		before := remaining[:start]
		block := remaining[start:end]
		remaining = remaining[end:]
		chunks = append(chunks, s.paragraphChunks(docID, before, filePath, &header, baseMeta)...)
		md := cloneMeta(baseMeta)
		md["file_path"] = filePath
		md["section_header"] = header
		md["is_code_block"] = true
		chunks = append(chunks, Chunk{
			ID:          uuid.NewString(),
			Content:     strings.TrimSpace(block),
			DocID:       docID,
			StartOffset: 0,
			EndOffset:   len(block),
			Metadata:    md,
		})
	}
	chunks = append(chunks, s.paragraphChunks(docID, remaining, filePath, &header, baseMeta)...)
	return chunks
}

func (s *SemanticChunker) paragraphChunks(docID, body, filePath string, header *string, baseMeta map[string]interface{}) []Chunk {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	parts := paragraphBreakSem.Split(body, -1)
	var out []Chunk
	pos := 0
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if h := lineHeader(part); h != "" {
			*header = h
		}
		for _, seg := range s.splitLong(part) {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			md := cloneMeta(baseMeta)
			md["file_path"] = filePath
			md["section_header"] = *header
			out = append(out, Chunk{
				ID:          uuid.NewString(),
				Content:     seg,
				DocID:       docID,
				StartOffset: pos,
				EndOffset:   pos + len(seg),
				Metadata:    md,
			})
			pos += len(seg) + 1
		}
	}
	return out
}

func lineHeader(s string) string {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func (s *SemanticChunker) splitLong(p string) []string {
	r := []rune(p)
	if len(r) <= s.MaxParagraphRunes {
		return []string{p}
	}
	var out []string
	for i := 0; i < len(r); i += s.MaxParagraphRunes {
		end := i + s.MaxParagraphRunes
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

// EnrichChunkContext prepends file path and section info for retrieval display.
func EnrichChunkContext(c Chunk) string {
	var path, header string
	if c.Metadata != nil {
		if v, ok := c.Metadata["file_path"].(string); ok {
			path = v
		}
		if v, ok := c.Metadata["section_header"].(string); ok {
			header = v
		}
	}
	var b strings.Builder
	if path != "" {
		b.WriteString("[")
		b.WriteString(path)
		b.WriteString("]")
	}
	if header != "" {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(header)
		b.WriteString(": ")
	}
	b.WriteString(c.Content)
	return b.String()
}
