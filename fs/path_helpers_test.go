package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestPathHelpers(t *testing.T) {
	id, _ := sop.ParseUUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	// Apply4LevelHierarchy yields a/b/c/d style layout using first 4 hex chars.
	p := Apply4LevelHierarchy(id)
	parts := strings.Split(p, string(os.PathSeparator))
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d: %q", len(parts), p)
	}

	base := t.TempDir()
	fp := DefaultToFilePath(base, id)
	if !strings.HasPrefix(fp, base+string(os.PathSeparator)) {
		t.Errorf("DefaultToFilePath should prefix base path, got %q base %q", fp, base)
	}

	// When base ends with separator, DefaultToFilePath should not add an extra one.
	base2 := base + string(os.PathSeparator)
	fp2 := DefaultToFilePath(base2, id)
	if strings.HasPrefix(fp2, base2+string(os.PathSeparator)) {
		t.Errorf("unexpected double separator in %q", fp2)
	}

	// Sanity: ensure file path ends with the 4-level hierarchy.
	if !strings.HasSuffix(fp, parts[0]+string(os.PathSeparator)+parts[1]+string(os.PathSeparator)+parts[2]+string(os.PathSeparator)+parts[3]) {
		t.Errorf("DefaultToFilePath suffix mismatch: %q vs %q", fp, p)
	}

	// Allow callers to override ToFilePath and then restore it.
	old := ToFilePath
	ToFilePath = func(basePath string, id sop.UUID) string { return filepath.Join(basePath, "custom", id.String()) }
	defer func() { ToFilePath = old }()
	got := ToFilePath(base, id)
	if !strings.Contains(got, "custom") {
		t.Errorf("override of ToFilePath not applied: %q", got)
	}
}
