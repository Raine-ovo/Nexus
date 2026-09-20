package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rainea/nexus/pkg/utils"
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

// ValidatePath ensures path resolves inside the workspace root. Symlinks are
// resolved on both the root and the target so a symlink inside the workspace
// cannot escape to an external path.
func (s *PathSandbox) ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("permission: empty path")
	}
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("permission: path contains NUL")
	}
	resolved, err := utils.SafePath(s.root, path)
	if err != nil {
		return fmt.Errorf("permission: %w", err)
	}
	base := filepath.Base(resolved)
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
