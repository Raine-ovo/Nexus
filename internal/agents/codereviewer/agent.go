// Package codereviewer provides a BaseAgent specialization for structured code review.
package codereviewer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// CodeReviewAgent specializes in code review tasks using local tools plus registry file/grep utilities.
type CodeReviewAgent struct {
	*core.BaseAgent
}

// New builds a code review agent with review-specific tools registered on the BaseAgent.
func New(deps *core.AgentDependencies) *CodeReviewAgent {
	ws := "."
	if deps != nil && deps.WorkspaceRoot != "" {
		ws = deps.WorkspaceRoot
	}
	ba := core.NewBaseAgent(deps, core.DefaultChatModel,
		"code_reviewer",
		"Reviews code and diffs for security, correctness, performance, and readability using structured findings.",
		SystemPrompt,
		ws,
	)
	a := &CodeReviewAgent{BaseAgent: ba}
	_ = a.BaseAgent.RegisterTools(
		a.toolAnalyzeDiff(),
		a.toolReviewFile(ws),
		a.toolCheckPatterns(ws),
	)
	attachRegistryTools(ba, deps, "read_file", "grep_search", "glob_search", "list_dir", "load_skill", "list_skills")
	return a
}

// attachRegistryTools copies named tools from deps.ToolRegistry onto the agent so they appear
// in LLM tool schemas. Execution still resolves via the same ToolMeta handlers.
func attachRegistryTools(ba *core.BaseAgent, deps *core.AgentDependencies, names ...string) {
	if ba == nil || deps == nil || deps.ToolRegistry == nil {
		return
	}
	for _, name := range names {
		if m := deps.ToolRegistry.Get(name); m != nil {
			_ = ba.AddTool(m)
		}
	}
}

func toolResult(content string, err error) (*types.ToolResult, error) {
	if err != nil {
		return &types.ToolResult{Name: "", Content: err.Error(), IsError: true}, nil
	}
	return &types.ToolResult{Content: content, IsError: false}, nil
}

func strArg(args map[string]interface{}, key string) string {
	return utils.GetString(args, key)
}

func intArg(args map[string]interface{}, key string) int {
	return utils.GetInt(args, key)
}

// --- analyze_diff ---

type diffHunk struct {
	Header    string `json:"header"`
	StartOld  int    `json:"start_old,omitempty"`
	StartNew  int    `json:"start_new,omitempty"`
	LineCount int    `json:"line_count"`
}

type diffFile struct {
	Path  string     `json:"path"`
	Hunks []diffHunk `json:"hunks"`
}

type diffAnalysis struct {
	Files      []diffFile `json:"files"`
	RawSummary string     `json:"raw_summary"`
}

func parseUnifiedDiff(text string) diffAnalysis {
	var out diffAnalysis
	sc := bufio.NewScanner(strings.NewReader(text))
	var cur *diffFile
	var curHunk *diffHunk
	lineCount := 0

	flushHunk := func() {
		if curHunk != nil {
			curHunk.LineCount = lineCount
			if cur != nil {
				cur.Hunks = append(cur.Hunks, *curHunk)
			}
			curHunk = nil
			lineCount = 0
		}
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "diff --git ") {
			flushHunk()
			parts := strings.Fields(line)
			path := ""
			if len(parts) >= 4 {
				path = strings.TrimPrefix(parts[2], "a/")
			}
			cur = &diffFile{Path: path, Hunks: nil}
			out.Files = append(out.Files, *cur)
			cur = &out.Files[len(out.Files)-1]
			continue
		}
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			flushHunk()
			h := diffHunk{Header: line}
			// @@ -l,s +l,s @@
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				if oldRange := strings.TrimPrefix(fields[1], "-"); oldRange != "" {
					h.StartOld = parseHunkStart(oldRange)
				}
				if newRange := strings.TrimPrefix(fields[2], "+"); newRange != "" {
					h.StartNew = parseHunkStart(newRange)
				}
			}
			curHunk = &h
			lineCount = 0
			continue
		}
		if curHunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")) {
			lineCount++
		}
	}
	flushHunk()

	var b strings.Builder
	for _, f := range out.Files {
		b.WriteString(fmt.Sprintf("%s (%d hunks)\n", f.Path, len(f.Hunks)))
	}
	out.RawSummary = strings.TrimSpace(b.String())
	return out
}

func parseHunkStart(rangeSpec string) int {
	i := strings.IndexByte(rangeSpec, ',')
	chunk := rangeSpec
	if i >= 0 {
		chunk = rangeSpec[:i]
	}
	n, _ := strconv.Atoi(chunk)
	if n < 0 {
		return 0
	}
	return n
}

func (a *CodeReviewAgent) toolAnalyzeDiff() *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "analyze_diff",
			Description: "Parse a unified diff string. Returns changed file paths, hunk headers, and approximate old/new line starts.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"diff_text": map[string]interface{}{
						"type":        "string",
						"description": "Full unified diff (git-style)",
					},
				},
				"required": []string{"diff_text"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/codereviewer",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			text := strArg(args, "diff_text")
			if strings.TrimSpace(text) == "" {
				return toolResult("", fmt.Errorf("diff_text is required"))
			}
			analysis := parseUnifiedDiff(text)
			b, err := json.MarshalIndent(analysis, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

// --- review_file ---

func (a *CodeReviewAgent) toolReviewFile(workspaceRoot string) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "review_file",
			Description: "Load a UTF-8 text file under the workspace and return metadata plus content (optionally sliced by line range) for human or model review.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path relative to workspace root",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "1-based start line (optional)",
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "1-based end line inclusive (optional)",
					},
					"max_bytes": map[string]interface{}{
						"type":        "integer",
						"description": "Max bytes when no line range (default 512000)",
					},
				},
				"required": []string{"path"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/codereviewer",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			rel := strArg(args, "path")
			if rel == "" {
				return toolResult("", fmt.Errorf("path is required"))
			}
			abs, err := utils.SafePath(workspaceRoot, rel)
			if err != nil {
				return toolResult("", err)
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return toolResult("", err)
			}
			start := intArg(args, "start_line")
			end := intArg(args, "end_line")
			maxBytes := intArg(args, "max_bytes")
			if maxBytes <= 0 {
				maxBytes = 512000
			}

			lines := strings.Split(string(data), "\n")
			total := len(lines)
			var body string
			if start > 0 || end > 0 {
				if start <= 0 {
					start = 1
				}
				if end <= 0 || end > total {
					end = total
				}
				if start > end {
					return toolResult("", fmt.Errorf("start_line after end_line"))
				}
				slice := lines[start-1 : end]
				body = strings.Join(slice, "\n")
			} else {
				if len(data) > maxBytes {
					body = string(data[:maxBytes]) + "\n... [truncated, use start_line/end_line]"
				} else {
					body = string(data)
				}
			}

			payload := map[string]interface{}{
				"path":         rel,
				"abs_path":     abs,
				"line_count":   total,
				"byte_length":  len(data),
				"content":      body,
				"review_hints": []string{"Check error handling", "Verify API contracts", "Scan for secrets and unsafe patterns"},
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}

// --- check_patterns ---

func (a *CodeReviewAgent) toolCheckPatterns(workspaceRoot string) *types.ToolMeta {
	return &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "check_patterns",
			Description: "Scan a file or all text files under a directory (bounded depth) for common anti-pattern substrings.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File or directory relative to workspace",
					},
					"max_files": map[string]interface{}{
						"type":        "integer",
						"description": "Max files to scan when path is a directory (default 200)",
					},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": "Max directory depth from path (default 4)",
					},
				},
				"required": []string{"path"},
			},
		},
		Permission: types.PermRead,
		Source:     "agent/codereviewer",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			_ = ctx
			rel := strArg(args, "path")
			if rel == "" {
				return toolResult("", fmt.Errorf("path is required"))
			}
			maxFiles := intArg(args, "max_files")
			if maxFiles <= 0 {
				maxFiles = 200
			}
			maxDepth := intArg(args, "max_depth")
			if maxDepth <= 0 {
				maxDepth = 4
			}

			abs, err := utils.SafePath(workspaceRoot, rel)
			if err != nil {
				return toolResult("", err)
			}

			patterns := []struct {
				ID      string `json:"id"`
				Needle  string `json:"needle"`
				Hint    string `json:"hint"`
				Severity string `json:"severity"`
			}{
				{"sql_concat", "fmt.Sprintf(", "Possible format-string SQL; use parameterized queries", "warning"},
				{"password_literal", "password=", "Hard-coded credential keyword", "warning"},
				{"eval", "eval(", "Dynamic code execution", "critical"},
				{"panic_ignore", "panic(nil)", "Suspicious panic usage", "info"},
				{"todo_security", "TODO: security", "Security-related TODO", "info"},
			}

			type hit struct {
				File     string `json:"file"`
				Line     int    `json:"line"`
				Pattern  string `json:"pattern_id"`
				Hint     string `json:"hint"`
				Severity string `json:"severity"`
				Snippet  string `json:"snippet"`
			}

			var hits []hit
			seenFiles := 0

			info, err := os.Stat(abs)
			if err != nil {
				return toolResult("", err)
			}

			scanFile := func(p string) error {
				data, err := os.ReadFile(p)
				if err != nil || len(data) > 2<<20 {
					return nil
				}
				relPath, _ := filepath.Rel(workspaceRoot, p)
				if relPath == "." || relPath == "" {
					relPath = p
				}
				ls := strings.Split(string(data), "\n")
				for i, ln := range ls {
					lower := strings.ToLower(ln)
					for _, pat := range patterns {
						if strings.Contains(lower, strings.ToLower(pat.Needle)) || strings.Contains(ln, pat.Needle) {
							hits = append(hits, hit{
								File:     relPath,
								Line:     i + 1,
								Pattern:  pat.ID,
								Hint:     pat.Hint,
								Severity: pat.Severity,
								Snippet:  strings.TrimSpace(utils.TruncateString(ln, 200)),
							})
						}
					}
				}
				return nil
			}

			if !info.IsDir() {
				seenFiles++
				if err := scanFile(abs); err != nil {
					return toolResult("", err)
				}
			} else {
				var walk func(string, int) error
				walk = func(dir string, depth int) error {
					if depth > maxDepth || seenFiles >= maxFiles {
						return nil
					}
					entries, err := os.ReadDir(dir)
					if err != nil {
						return err
					}
					for _, e := range entries {
						if e.Name() == ".git" && e.IsDir() {
							continue
						}
						full := filepath.Join(dir, e.Name())
						if e.IsDir() {
							_ = walk(full, depth+1)
							continue
						}
						if seenFiles >= maxFiles {
							return nil
						}
						ext := strings.ToLower(filepath.Ext(e.Name()))
						switch ext {
						case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rs", ".c", ".h", ".cpp", ".yaml", ".yml", ".json", ".md":
						default:
							if ext != "" && len(ext) <= 5 {
								continue
							}
						}
						seenFiles++
						_ = scanFile(full)
					}
					return nil
				}
				if err := walk(abs, 0); err != nil {
					return toolResult("", err)
				}
			}

			out := map[string]interface{}{
				"hits":        hits,
				"files_scanned": seenFiles,
				"truncated":   seenFiles >= maxFiles,
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return toolResult("", err)
			}
			return toolResult(string(b), nil)
		},
	}
}
