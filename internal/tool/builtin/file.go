// Package builtin registers path-safe file and workspace tools used by the Nexus agent runtime.
package builtin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

const (
	maxReadFileBytes = 8 << 20 // 8 MiB cap for full-file reads
)

// RegisterFunc registers a tool without importing the parent tool package (avoids cycles).
type RegisterFunc func(meta *types.ToolMeta) error

// RegisterFileTools registers read_file, write_file, edit_file, and list_dir.
func RegisterFileTools(reg RegisterFunc, workspaceRoot string) error {
	tools := []*types.ToolMeta{
		{
			Definition: types.ToolDefinition{
				Name:        "read_file",
				Description: "Read a UTF-8 text file within the workspace. Optional 1-based start_line and end_line (inclusive). Large files without line range are capped.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path relative to workspace root",
						},
						"start_line": map[string]interface{}{
							"type":        "integer",
							"description": "First line to include (1-based); omit for start of file",
						},
						"end_line": map[string]interface{}{
							"type":        "integer",
							"description": "Last line to include (1-based); omit for end of file",
						},
					},
					"required": []string{"path"},
				},
			},
			Permission: types.PermRead,
			Source:     "builtin",
			Handler:    makeReadFileHandler(workspaceRoot),
		},
		{
			Definition: types.ToolDefinition{
				Name:        "write_file",
				Description: "Create or overwrite a file under the workspace root. Parent directories are created as needed.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path relative to workspace root",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Full file contents",
						},
					},
					"required": []string{"path", "content"},
				},
			},
			Permission: types.PermWrite,
			Source:     "builtin",
			Handler:    makeWriteFileHandler(workspaceRoot),
		},
		{
			Definition: types.ToolDefinition{
				Name:        "edit_file",
				Description: "Replace the first occurrence of old_string with new_string in a workspace file. old_string must be unique in the file.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path relative to workspace root",
						},
						"old_string": map[string]interface{}{
							"type":        "string",
							"description": "Exact substring to replace once",
						},
						"new_string": map[string]interface{}{
							"type":        "string",
							"description": "Replacement text",
						},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
			Permission: types.PermWrite,
			Source:     "builtin",
			Handler:    makeEditFileHandler(workspaceRoot),
		},
		{
			Definition: types.ToolDefinition{
				Name:        "list_dir",
				Description: "List files and subdirectories in a workspace directory (non-recursive). Append trailing separator for directories.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Directory relative to workspace root; use . for root",
						},
					},
					"required": []string{"path"},
				},
			},
			Permission: types.PermRead,
			Source:     "builtin",
			Handler:    makeListDirHandler(workspaceRoot),
		},
	}
	for _, t := range tools {
		if err := reg(t); err != nil {
			return err
		}
	}
	return nil
}

func makeReadFileHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		rel := utils.GetString(args, "path")
		if rel == "" {
			return nil, fmt.Errorf("path is required")
		}
		abs, err := utils.SafePath(root, rel)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			return nil, fmt.Errorf("path is a directory: %s", rel)
		}

		start := utils.GetInt(args, "start_line")
		end := utils.GetInt(args, "end_line")

		if start <= 0 && end <= 0 {
			if st.Size() > maxReadFileBytes {
				return nil, fmt.Errorf("file too large (%d bytes); specify start_line/end_line or use a smaller file", st.Size())
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(data) {
				return nil, fmt.Errorf("file does not appear to be valid UTF-8")
			}
			if bytesLooksBinary(data) {
				return &types.ToolResult{
					Name:    "read_file",
					Content: string(data) + "\n[warning: file may contain binary data]",
				}, nil
			}
			return &types.ToolResult{Name: "read_file", Content: string(data)}, nil
		}

		f, err := os.Open(abs)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var lines []string
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}

		s, e := start, end
		if s < 1 {
			s = 1
		}
		if e < 1 || e > len(lines) {
			e = len(lines)
		}
		if s > len(lines) {
			return &types.ToolResult{Name: "read_file", Content: ""}, nil
		}
		sel := lines[s-1 : e]
		var b strings.Builder
		for i, ln := range sel {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(ln)
		}
		return &types.ToolResult{Name: "read_file", Content: b.String()}, nil
	}
}

func makeWriteFileHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		rel := utils.GetString(args, "path")
		content := utils.GetString(args, "content")
		if rel == "" {
			return nil, fmt.Errorf("path is required")
		}
		abs, err := utils.SafePath(root, rel)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return nil, err
		}
		return &types.ToolResult{
			Name:    "write_file",
			Content: fmt.Sprintf("wrote %d bytes to %s", len(content), rel),
		}, nil
	}
}

func makeEditFileHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		rel := utils.GetString(args, "path")
		oldS := utils.GetString(args, "old_string")
		newS := utils.GetString(args, "new_string")
		if rel == "" || oldS == "" {
			return nil, fmt.Errorf("path and old_string are required")
		}
		abs, err := utils.SafePath(root, rel)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		s := string(data)
		if !strings.Contains(s, oldS) {
			return nil, fmt.Errorf("old_string not found in file")
		}
		if strings.Count(s, oldS) != 1 {
			return nil, fmt.Errorf("old_string must match exactly once (found %d)", strings.Count(s, oldS))
		}
		out := strings.Replace(s, oldS, newS, 1)
		if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
			return nil, err
		}
		return &types.ToolResult{Name: "edit_file", Content: "file updated"}, nil
	}
}

func makeListDirHandler(root string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		rel := utils.GetString(args, "path")
		if rel == "" {
			rel = "."
		}
		abs, err := utils.SafePath(root, rel)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		for _, ent := range entries {
			name := ent.Name()
			if ent.IsDir() {
				name += string(filepath.Separator)
			}
			b.WriteString(name)
			b.WriteByte('\n')
		}
		return &types.ToolResult{Name: "list_dir", Content: b.String()}, nil
	}
}

// bytesLooksBinary returns true if data appears to contain NUL bytes in the prefix.
func bytesLooksBinary(data []byte) bool {
	const sniff = 8000
	n := len(data)
	if n > sniff {
		n = sniff
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
