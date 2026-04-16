package loader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rainea/nexus/pkg/utils"
)

// Document represents a loaded document with metadata.
type Document struct {
	ID       string
	Content  string
	Source   string
	Metadata map[string]interface{}
}

// MaxFileSizeBytes caps single-file reads to avoid loading huge binaries into memory (0 = unlimited).
var MaxFileSizeBytes int64 = 16 << 20 // 16 MiB

var supportedExts = map[string]struct{}{
	".md":   {},
	".txt":  {},
	".go":   {},
	".py":   {},
	".java": {},
	".json": {},
	".yaml": {},
	".yml":  {},
}

// ErrUnsupportedExt is returned when FileLoader encounters a non-supported extension.
var ErrUnsupportedExt = errors.New("loader: unsupported file extension")

// ErrFileTooLarge is returned when a file exceeds MaxFileSizeBytes.
var ErrFileTooLarge = errors.New("loader: file exceeds size limit")

// DirectoryLoader walks a directory tree and loads supported file types.
type DirectoryLoader struct {
	Root       string
	Extensions map[string]struct{} // if nil, uses default supportedExts
}

// NewDirectoryLoader validates root with optional workspace safety.
func NewDirectoryLoader(root string) (*DirectoryLoader, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("loader: resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("loader: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("loader: root is not a directory: %s", abs)
	}
	return &DirectoryLoader{Root: abs}, nil
}

// NewDirectoryLoaderSafe resolves path under workspace root using utils.SafePath.
func NewDirectoryLoaderSafe(workspaceRoot, relDir string) (*DirectoryLoader, error) {
	abs, err := utils.SafePath(workspaceRoot, relDir)
	if err != nil {
		return nil, err
	}
	return NewDirectoryLoader(abs)
}

// ListSupportedExtensions returns sorted extensions including the leading dot (e.g. ".go", ".md").
func ListSupportedExtensions() []string {
	out := make([]string, 0, len(supportedExts))
	for e := range supportedExts {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// Load walks the directory and returns all documents.
func (d *DirectoryLoader) Load() ([]Document, error) {
	exts := d.Extensions
	if exts == nil {
		exts = supportedExts
	}
	var docs []Document
	err := filepath.WalkDir(d.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") && path != d.Root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := exts[ext]; !ok {
			return nil
		}
		doc, err := FileLoader(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// FileLoader loads a single file based on its extension.
func FileLoader(path string) (Document, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := supportedExts[ext]; !ok {
		return Document{}, fmt.Errorf("%w %q for %s", ErrUnsupportedExt, ext, path)
	}
	switch ext {
	case ".md":
		return LoadMarkdownFile(path)
	default:
		return loadPlainFile(path)
	}
}

func loadPlainFile(path string) (Document, error) {
	if MaxFileSizeBytes > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return Document{}, fmt.Errorf("loader: stat: %w", err)
		}
		if info.Size() > MaxFileSizeBytes {
			return Document{}, fmt.Errorf("%w: %s (%d bytes)", ErrFileTooLarge, path, info.Size())
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read file: %w", err)
	}
	abs, _ := filepath.Abs(path)
	ext := strings.ToLower(filepath.Ext(path))
	format := strings.TrimPrefix(ext, ".")
	if format == "" {
		format = "text"
	}
	return Document{
		ID:      abs,
		Content: string(b),
		Source:  abs,
		Metadata: map[string]interface{}{
			"file_path": abs,
			"format":    format,
		},
	}, nil
}

// IsSupportedExt reports whether the extension (with or without leading dot) is loadable.
func IsSupportedExt(ext string) bool {
	e := strings.ToLower(ext)
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	_, ok := supportedExts[e]
	return ok
}
