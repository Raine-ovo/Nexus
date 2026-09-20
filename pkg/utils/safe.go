package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafePath resolves and validates that the given path stays within the workspace root.
// Both the root and the target have symlinks resolved, so a symlink placed inside the
// workspace cannot escape to an external path. The resolved absolute path is returned,
// or an error if path traversal or a symlink escape is detected.
func SafePath(root, target string) (string, error) {
	if strings.Contains(target, "\x00") {
		return "", fmt.Errorf("resolve target: path contains NUL")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}
	resolvedRoot, err := resolveSymlinks(absRoot)
	if err != nil {
		// Root may not exist yet (fresh workspace); fall back to the cleaned absolute path.
		resolvedRoot = absRoot
	}

	var candidate string
	if filepath.IsAbs(target) {
		candidate = filepath.Clean(target)
	} else {
		candidate = filepath.Join(absRoot, target)
	}

	resolvedTarget, err := resolveSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve target %s: %w", target, err)
	}

	if !pathWithin(resolvedRoot, resolvedTarget) {
		return "", fmt.Errorf("path traversal detected: %s escapes workspace %s", target, root)
	}

	return resolvedTarget, nil
}

// resolveSymlinks resolves symlinks in the deepest existing ancestor of path,
// preserving any non-existent trailing components verbatim. This lets writes to
// not-yet-created files still be validated against the real (symlink-resolved)
// parent directory.
func resolveSymlinks(path string) (string, error) {
	clean := filepath.Clean(path)
	existing := clean
	var suffix []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return resolved, nil
	}
	return filepath.Join(append([]string{resolved}, suffix...)...), nil
}

func pathWithin(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(target, root)
}

// IsDangerousCommand checks if a shell command matches any dangerous patterns.
func IsDangerousCommand(cmd string, patterns []string) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true, fmt.Sprintf("command matches dangerous pattern: %q", p)
		}
	}
	return false, ""
}

// TruncateString truncates a string to maxLen runes, appending a suffix if truncated.
func TruncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	suffix := "\n... [output truncated]"
	if maxLen > len(suffix) {
		return string(runes[:maxLen-len([]rune(suffix))]) + suffix
	}
	return string(runes[:maxLen])
}

// SanitizeToolName ensures a tool name contains only safe characters.
func SanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Coalesce returns the first non-empty string.
func Coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
