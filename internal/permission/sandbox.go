package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathSandbox validates that file operations stay within workspace boundaries.
type PathSandbox struct {
	root              string
	dangerousPatterns []string
}

// NewPathSandbox constructs a sandbox rooted at root with extra deny globs.
func NewPathSandbox(root string, patterns []string) *PathSandbox {
	if root == "" {
		root = "."
	}
	return &PathSandbox{
		root:              root,
		dangerousPatterns: append([]string(nil), patterns...),
	}
}

// ValidatePath ensures path resolves inside the workspace root.
func (s *PathSandbox) ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("permission: empty path")
	}
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("permission: path contains NUL")
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return fmt.Errorf("permission: resolve root: %w", err)
	}
	clean := filepath.Clean(path)
	var target string
	if filepath.IsAbs(clean) {
		target = clean
	} else {
		target = filepath.Join(rootAbs, clean)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("permission: resolve path: %w", err)
	}
	sep := string(os.PathSeparator)
	rootPrefix := rootAbs
	if !strings.HasSuffix(rootPrefix, sep) {
		rootPrefix += sep
	}
	if targetAbs != rootAbs && !strings.HasPrefix(targetAbs+sep, rootPrefix) {
		return fmt.Errorf("permission: path escapes workspace: %s", path)
	}
	base := filepath.Base(targetAbs)
	for _, pat := range s.dangerousPatterns {
		if pat == "" {
			continue
		}
		ok, err := filepath.Match(pat, base)
		if err != nil {
			return fmt.Errorf("permission: bad pattern %q: %w", pat, err)
		}
		if ok {
			return fmt.Errorf("permission: path matches dangerous pattern %q", pat)
		}
		ok, err = filepath.Match(pat, path)
		if err != nil {
			return fmt.Errorf("permission: bad pattern %q: %w", pat, err)
		}
		if ok {
			return fmt.Errorf("permission: path matches dangerous pattern %q", pat)
		}
	}
	return nil
}

// ValidateCommand performs light validation on shell-like commands.
func (s *PathSandbox) ValidateCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("permission: empty command")
	}
	lower := strings.ToLower(cmd)
	deny := []string{
		"rm -rf /",
		"mkfs",
		"dd if=",
		":(){", // fork bomb prefix
	}
	for _, d := range deny {
		if strings.Contains(lower, d) {
			return fmt.Errorf("permission: command contains disallowed fragment %q", d)
		}
	}
	return nil
}
