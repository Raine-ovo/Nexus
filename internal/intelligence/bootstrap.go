package intelligence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMaxFileChars  = 20000
	defaultMaxTotalChars = 150000
)

// BootstrapLoader loads ordered bootstrap files (SOUL.md, IDENTITY.md, TOOLS.md, MEMORY.md)
// from a workspace directory to construct the base system prompt.
type BootstrapLoader struct {
	workspaceDir  string
	maxFileChars  int
	maxTotalChars int
}

// DefaultBootstrapFiles lists the files to load in order.
var DefaultBootstrapFiles = []string{"SOUL.md", "IDENTITY.md", "TOOLS.md", "MEMORY.md"}

// NewBootstrapLoader returns a loader rooted at workspaceDir with default character limits.
func NewBootstrapLoader(workspaceDir string) *BootstrapLoader {
	return &BootstrapLoader{
		workspaceDir:  workspaceDir,
		maxFileChars:  defaultMaxFileChars,
		maxTotalChars: defaultMaxTotalChars,
	}
}

// SetMaxLimits updates per-file and total caps used by Load.
func (l *BootstrapLoader) SetMaxLimits(maxFileChars, maxTotalChars int) {
	if l == nil {
		return
	}
	if maxFileChars > 0 {
		l.maxFileChars = maxFileChars
	}
	if maxTotalChars > 0 {
		l.maxTotalChars = maxTotalChars
	}
}

// Load reads each default bootstrap file in order, truncates per file if needed,
// concatenates with section markers, and enforces the total character budget.
func (l *BootstrapLoader) Load() (string, error) {
	if l == nil {
		return "", fmt.Errorf("intelligence: nil BootstrapLoader")
	}
	if l.workspaceDir == "" {
		return "", fmt.Errorf("intelligence: workspaceDir is empty")
	}
	maxTotal := l.maxTotalChars
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalChars
	}

	var b strings.Builder
	total := 0

	for _, name := range DefaultBootstrapFiles {
		chunk, err := l.LoadFile(name)
		if err != nil {
			return "", err
		}
		header := fmt.Sprintf("\n<<< SECTION: %s >>>\n", strings.TrimSuffix(name, filepath.Ext(name)))
		part := header + chunk
		if total+len(part) > maxTotal {
			remain := maxTotal - total
			if remain <= 0 {
				break
			}
			part = part[:remain]
		}
		b.WriteString(part)
		total += len(part)
		if total >= maxTotal {
			break
		}
	}

	return b.String(), nil
}

// LoadFile reads a single file under the workspace directory.
// Missing files produce an empty string without error so workspaces can grow incrementally.
func (l *BootstrapLoader) LoadFile(filename string) (string, error) {
	if l == nil {
		return "", fmt.Errorf("intelligence: nil BootstrapLoader")
	}
	if filename == "" {
		return "", fmt.Errorf("intelligence: empty filename")
	}
	base := filepath.Clean(l.workspaceDir)
	path := filepath.Join(base, filepath.Clean(filename))
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("intelligence: path escapes workspace: %q", filename)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read bootstrap %s: %w", path, err)
	}

	s := string(data)
	maxFile := l.maxFileChars
	if maxFile <= 0 {
		maxFile = defaultMaxFileChars
	}
	if len(s) > maxFile {
		s = s[:maxFile]
	}
	return s, nil
}
