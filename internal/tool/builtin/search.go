package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

const (
	maxGrepMatches   = 500
	maxGlobFiles     = 500
	maxGrepFileBytes = 512 * 1024
)

var skipDirNames = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	".idea":        {},
	".vscode":      {},
}

// RegisterSearchTools registers grep_search and glob_search under the workspace.
// grep_search walks the tree with filepath.Walk, skipping bulky directories and binary files.
func RegisterSearchTools(reg RegisterFunc, workspaceRoot string) error {
	tools := []*types.ToolMeta{
		{
			Definition: types.ToolDefinition{
				Name:        "grep_search",
				Description: "Search file contents with a regular expression under the workspace (Go regexp syntax). Skips large files and common dependency directories.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Go regexp pattern",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Subdirectory relative to workspace (default .)",
						},
					},
					"required": []string{"pattern"},
				},
			},
			Permission: types.PermRead,
			Source:     "builtin",
			Handler:    makeGrepHandler(workspaceRoot),
		},
		{
			Definition: types.ToolDefinition{
				Name:        "glob_search",
				Description: "Find files matching a glob pattern (e.g. *.go, **/*.md) under the workspace using filepath.Walk.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Glob pattern relative to search base",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Base directory relative to workspace (default .)",
						},
					},
					"required": []string{"pattern"},
				},
			},
			Permission: types.PermRead,
			Source:     "builtin",
			Handler:    makeGlobHandler(workspaceRoot),
		},
	}
	for _, t := range tools {
		if err := reg(t); err != nil {
			return err
		}
	}
	return nil
}

func makeGrepHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		pat := utils.GetString(args, "pattern")
		if pat == "" {
			return nil, fmt.Errorf("pattern is required")
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("invalid regexp: %w", err)
		}
		sub := utils.GetString(args, "path")
		if sub == "" {
			sub = "."
		}
		base, err := utils.SafePath(root, sub)
		if err != nil {
			return nil, err
		}

		var b strings.Builder
		count := 0
		err = filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if info.IsDir() {
				if _, skip := skipDirNames[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if count >= maxGrepMatches {
				return filepath.SkipAll
			}
			if info.Size() > maxGrepFileBytes {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			if bytesLooksBinary(data) {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			rel, _ := filepath.Rel(root, path)
			for i, line := range lines {
				if re.MatchString(line) {
					count++
					fmt.Fprintf(&b, "%s:%d:%s\n", rel, i+1, line)
					if count >= maxGrepMatches {
						b.WriteString(fmt.Sprintf("... truncated after %d matches\n", maxGrepMatches))
						return filepath.SkipAll
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		out := strings.TrimSpace(b.String())
		if out == "" {
			out = "(no matches)"
		}
		return &types.ToolResult{Name: "grep_search", Content: out}, nil
	}
}

func makeGlobHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		pattern := utils.GetString(args, "pattern")
		if pattern == "" {
			return nil, fmt.Errorf("pattern is required")
		}
		sub := utils.GetString(args, "path")
		if sub == "" {
			sub = "."
		}
		base, err := utils.SafePath(root, sub)
		if err != nil {
			return nil, err
		}

		var matches []string
		err = filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if info.IsDir() {
				if _, skip := skipDirNames[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return nil
			}
			if !matchGlob(rel, pattern) {
				return nil
			}
			outRel, _ := filepath.Rel(root, path)
			matches = append(matches, outRel)
			if len(matches) >= maxGlobFiles {
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m)
			b.WriteByte('\n')
		}
		out := strings.TrimSpace(b.String())
		if out == "" {
			out = "(no files)"
		} else if len(matches) >= maxGlobFiles {
			out += fmt.Sprintf("\n... truncated after %d files\n", maxGlobFiles)
		}
		return &types.ToolResult{Name: "glob_search", Content: out}, nil
	}
}

// matchGlob matches rel (slash-separated path under search base) against pattern.
// Supports filepath.Match patterns plus a single ** wildcard segment.
func matchGlob(rel, pattern string) bool {
	if ok, err := filepath.Match(pattern, rel); err == nil && ok {
		return true
	}
	if !strings.Contains(pattern, "**") {
		return false
	}
	left, right, ok := strings.Cut(pattern, "**")
	if !ok {
		return false
	}
	left = strings.TrimSpace(strings.TrimSuffix(left, "/"))
	right = strings.TrimSpace(strings.TrimPrefix(right, "/"))
	if left != "" {
		prefix := left + string(filepath.Separator)
		if rel != left && !strings.HasPrefix(rel, prefix) {
			return false
		}
		if strings.HasPrefix(rel, prefix) {
			rel = rel[len(prefix):]
		} else {
			rel = ""
		}
	}
	if right == "" {
		return true
	}
	for {
		if m, err := filepath.Match(right, rel); err == nil && m {
			return true
		}
		if rel == "" || rel == "." {
			break
		}
		rel = filepath.Dir(rel)
		if rel == "." {
			break
		}
	}
	return false
}
