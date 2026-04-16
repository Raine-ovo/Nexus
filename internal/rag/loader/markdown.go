package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	headerRe    = regexp.MustCompile(`(?m)^(#+)\s+(.+)$`)
	codeFenceRe = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\n(.*?)```")
)

// MarkdownParsed holds structured extraction from a markdown file.
type MarkdownParsed struct {
	Frontmatter map[string]interface{}
	Body        string
	Sections    []MarkdownSection
	CodeBlocks  []string
}

// MarkdownSection is content under one header.
type MarkdownSection struct {
	Level   int
	Title   string
	Content string
}

// LoadMarkdownFile reads a markdown file with optional YAML frontmatter.
func LoadMarkdownFile(path string) (Document, error) {
	if MaxFileSizeBytes > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return Document{}, fmt.Errorf("markdown: stat: %w", err)
		}
		if info.Size() > MaxFileSizeBytes {
			return Document{}, fmt.Errorf("%w: %s (%d bytes)", ErrFileTooLarge, path, info.Size())
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("markdown: read: %w", err)
	}
	abs, _ := filepath.Abs(path)
	parsed := ParseMarkdown(string(b))
	meta := map[string]interface{}{
		"file_path": abs,
		"format":    "markdown",
	}
	for k, v := range parsed.Frontmatter {
		meta["frontmatter_"+k] = v
	}
	return Document{
		ID:      abs,
		Content: parsed.Body,
		Source:  abs,
		Metadata: mergeMeta(meta, map[string]interface{}{
			"sections":    len(parsed.Sections),
			"code_blocks": len(parsed.CodeBlocks),
		}),
	}, nil
}

// ParseMarkdown extracts frontmatter, splits sections by headers, and collects fenced code blocks.
func ParseMarkdown(src string) MarkdownParsed {
	out := MarkdownParsed{
		Frontmatter: map[string]interface{}{},
	}
	body := strings.TrimSpace(src)
	body = extractFrontmatter(body, &out)
	out.Body = body
	out.CodeBlocks = extractCodeBlocks(body)
	out.Sections = splitByHeaders(body)
	return out
}

func extractFrontmatter(s string, out *MarkdownParsed) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := strings.TrimPrefix(s, "---")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return s
	}
	fm := strings.TrimSpace(rest[:idx])
	after := strings.TrimSpace(rest[idx+4:])
	if err := yaml.Unmarshal([]byte(fm), &out.Frontmatter); err != nil {
		out.Frontmatter = map[string]interface{}{"_parse_error": err.Error()}
	}
	return after
}

func extractCodeBlocks(s string) []string {
	matches := codeFenceRe.FindAllStringSubmatch(s, -1)
	var blocks []string
	for _, m := range matches {
		if len(m) > 1 {
			blocks = append(blocks, strings.TrimSpace(m[1]))
		}
	}
	return blocks
}

// splitByHeaders breaks markdown into sections; content before first header is one section with empty title.
func splitByHeaders(s string) []MarkdownSection {
	idxs := headerRe.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return []MarkdownSection{{Level: 0, Title: "", Content: strings.TrimSpace(s)}}
	}
	var sections []MarkdownSection
	first := strings.TrimSpace(s[:idxs[0][0]])
	if first != "" {
		sections = append(sections, MarkdownSection{Level: 0, Title: "", Content: first})
	}
	for i, loc := range idxs {
		line := s[loc[0]:loc[1]]
		level, title := parseHeaderLine(line)
		var end int
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		} else {
			end = len(s)
		}
		block := strings.TrimSpace(s[loc[1]:end])
		sections = append(sections, MarkdownSection{
			Level:   level,
			Title:   title,
			Content: block,
		})
	}
	return sections
}

func parseHeaderLine(line string) (level int, title string) {
	line = strings.TrimSpace(line)
	i := 0
	for i < len(line) && line[i] == '#' {
		level++
		i++
	}
	if i < len(line) && line[i] == ' ' {
		i++
	}
	title = strings.TrimSpace(line[i:])
	return level, title
}

// DocumentFromMarkdownSections builds multiple lightweight documents from section splits (for fine-grained indexing).
func DocumentFromMarkdownSections(basePath string, parsed MarkdownParsed) []Document {
	abs := basePath
	var docs []Document
	for i, sec := range parsed.Sections {
		if strings.TrimSpace(sec.Content) == "" {
			continue
		}
		id := fmt.Sprintf("%s#%d", abs, i)
		meta := map[string]interface{}{
			"file_path": abs,
			"format":    "markdown_section",
			"section":   sec.Title,
			"level":     sec.Level,
		}
		docs = append(docs, Document{
			ID:       id,
			Content:  sec.Content,
			Source:   abs,
			Metadata: meta,
		})
	}
	if len(docs) == 0 && strings.TrimSpace(parsed.Body) != "" {
		d, _ := LoadMarkdownFile(basePath)
		return []Document{d}
	}
	return docs
}

func mergeMeta(a, b map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
