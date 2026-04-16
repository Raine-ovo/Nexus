package utils

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafePath resolves and validates that the given path stays within the workspace root.
// Returns the resolved absolute path or an error if path traversal is detected.
func SafePath(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}

	absTarget, err := filepath.Abs(filepath.Join(root, target))
	if err != nil {
		return "", fmt.Errorf("resolve target %s: %w", target, err)
	}

	if !strings.HasPrefix(absTarget, absRoot+string(filepath.Separator)) && absTarget != absRoot {
		return "", fmt.Errorf("path traversal detected: %s escapes workspace %s", target, root)
	}

	return absTarget, nil
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
