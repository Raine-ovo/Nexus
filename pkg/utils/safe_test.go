package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafePathWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := SafePath(root, filepath.Join("sub", "f.txt"))
	if err != nil {
		t.Fatalf("SafePath: %v", err)
	}
	want := filepath.Join(root, "sub", "f.txt")
	if got != want {
		t.Fatalf("SafePath = %q, want %q", got, want)
	}
}

func TestSafePathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{"../etc/passwd", "..", "../../" + filepath.Base(root) + "-sibling"} {
		if _, err := SafePath(root, target); err == nil {
			t.Fatalf("SafePath(%q) expected error, got nil", target)
		}
	}
}

func TestSafePathSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Symlink creation may require privileges on Windows; skip gracefully.
		if err := os.Symlink(t.TempDir(), filepath.Join(t.TempDir(), "probe")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := SafePath(root, filepath.Join("escape", "secret.txt")); err == nil {
		t.Fatalf("SafePath through symlink escape expected error, got nil")
	}
}

func TestSafePathSymlinkWithinRootAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(t.TempDir(), "probe")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := SafePath(root, filepath.Join("alias", "new.txt"))
	if err != nil {
		t.Fatalf("SafePath within-root symlink: %v", err)
	}
	want := filepath.Join(realDir, "new.txt")
	if got != want {
		t.Fatalf("SafePath = %q, want %q", got, want)
	}
}

func TestPathWithin(t *testing.T) {
	cases := []struct {
		root, target string
		want         bool
	}{
		{"/a", "/a", true},
		{"/a", "/a/b", true},
		{"/a", "/a/b/c", true},
		{"/a", "/ab", false},
		{"/a", "/b", false},
		{"/a/b", "/a", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.root, c.target); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.root, c.target, got, c.want)
		}
	}
}
