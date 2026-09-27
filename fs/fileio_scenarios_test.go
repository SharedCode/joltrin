package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// TestFileIOScenarios consolidates FileIO coverage: write (mkdir branch & direct),
// read, exists (true/false & permission denied), readdir success & error, mkdir/removeall,
// remove, retryIO helper behavior, and permanent error surface when parent path blocked.
func TestFileIOScenarios(t *testing.T) {
	// Speed up retries for this test.
	originalDuration := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	defer func() { sop.RetryStartDuration = originalDuration }()

	ctx := context.Background()
	fio := NewFileIO()
	base := t.TempDir()

	// 1. Write paths: mkdir-needed vs direct success.
	writeTargets := []struct {
		name      string
		rel       string
		preCreate bool
		content   string
	}{
		{name: "mkdir_needed", rel: filepath.Join("nested1", "a", "file1.txt"), content: "hello-mkdir"},
		{name: "direct_success", rel: filepath.Join("nested2", "file2.txt"), preCreate: true, content: "hello-direct"},
	}
	for _, wt := range writeTargets {
		t.Run(wt.name, func(t *testing.T) {
			target := filepath.Join(base, wt.rel)
			if wt.preCreate {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatalf("pre mkdir: %v", err)
				}
			}
			if err := fio.WriteFile(ctx, target, []byte(wt.content), 0o640); err != nil {
				t.Fatalf("WriteFile(%s): %v", wt.name, err)
			}
			if !fio.Exists(ctx, target) {
				t.Fatalf("Exists false after write: %s", wt.name)
			}
			rb, err := fio.ReadFile(ctx, target)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(rb) != wt.content {
				t.Fatalf("content mismatch got=%q want=%q", rb, wt.content)
			}
		})
	}

	// 2. ReadDir success (nested2) and error (missing path).
	if ents, err := fio.ReadDir(ctx, filepath.Join(base, "nested2")); err != nil || len(ents) != 1 {
		t.Fatalf("ReadDir success path unexpected err=%v len=%d", err, len(ents))
	}
	if _, err := fio.ReadDir(ctx, filepath.Join(base, "does-not-exist")); err == nil {
		t.Fatalf("expected ReadDir error on missing dir")
	}

	// 3. Exists negative case for missing file.
	if fio.Exists(ctx, filepath.Join(base, "nope", "missing.txt")) {
		t.Fatalf("Exists returned true for missing path")
	}

	// 4. Permission denied path still counts as exists.
	permFile := filepath.Join(base, "perm.txt")
	if err := os.WriteFile(permFile, []byte("x"), 0o000); err != nil {
		t.Fatalf("prep perm file: %v", err)
	}
	if !fio.Exists(ctx, permFile) {
		t.Fatalf("Exists false for permission denied file")
	}
	_ = os.Chmod(permFile, 0o644) // cleanup

	// 5. retryIO helper minimal behavior: success after one transient-like error.
	attempts := 0
	if err := retryIO(ctx, func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("temp")
		}
		return nil
	}, sop.FileIOError); err != nil {
		t.Fatalf("retryIO unexpected err: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}

	// 6. MkdirAll + RemoveAll round trip plus Remove single file.
	extraDir := filepath.Join(base, "x", "y")
	if err := fio.MkdirAll(ctx, extraDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f1 := filepath.Join(extraDir, "f1")
	if err := fio.WriteFile(ctx, f1, []byte("d1"), 0o644); err != nil {
		t.Fatalf("write f1: %v", err)
	}
	if err := fio.Remove(ctx, f1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := fio.RemoveAll(ctx, filepath.Join(base, "x")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if fio.Exists(ctx, f1) {
		t.Fatalf("file still exists after remove operations")
	}

	// 7. Permanent error surface: parent path blocked by file; WriteFile should return error.
	// Create a directory, then place a file where the parent directory should be, causing MkdirAll to fail.
	blockedRoot := filepath.Join(base, "block")
	if err := os.MkdirAll(blockedRoot, 0o755); err != nil {
		t.Fatalf("prep blocked root: %v", err)
	}
	parentDirPath := filepath.Join(blockedRoot, "child") // this will be a file, not directory
	if err := os.WriteFile(parentDirPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("prep block: %v", err)
	}
	blockedParentFile := filepath.Join(parentDirPath, "f.txt")
	if err := fio.WriteFile(ctx, blockedParentFile, []byte("x"), 0o644); err == nil {
		t.Fatalf("expected error for blocked parent path")
	}

	// 8. Additional Exists check on nested directory path.
	if !fio.Exists(ctx, filepath.Join(base, "nested1", "a")) {
		t.Fatalf("expected nested directory to exist")
	}

	// 9. Timeout context sanity for retryIO (no retries triggered) – ensures context honored.
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := retryIO(tctx, func(context.Context) error { return nil }, sop.FileIOError); err != nil {
		t.Fatalf("retryIO with timeout ctx: %v", err)
	}

	// 10. Directory permission denied path still counts as exists.
	permDir := filepath.Join(base, "permdir")
	if err := os.MkdirAll(permDir, 0o755); err != nil {
		t.Fatalf("mkdir permdir: %v", err)
	}
	_ = os.Chmod(permDir, 0o000)
	if !fio.Exists(ctx, permDir) {
		t.Fatalf("Exists false for permission denied directory")
	}
	_ = os.Chmod(permDir, 0o755)

	// 11. Small sleep ensures mod times differ for any ordering logic (defensive, though not used here).
	time.Sleep(5 * time.Millisecond)
}

// Additional FileIOSimulator coverage: read-not-found and resetFlag(false) persistence of induced errors.
func TestFileIOSim_ReadNotFoundAndResetFalse(t *testing.T) {
	sim := newFileIOSim()
	ctx := context.Background()
	// read missing file -> not found error
	if _, err := sim.ReadFile(ctx, "missing_99"); err == nil {
		t.Fatalf("expected not found error")
	}
	// set error flag, then disable reset so flags persist across induced errors
	sim.setErrorOnSuffixNumber2(5)
	sim.setResetFlag(false)
	if _, err := sim.ReadFile(ctx, "foo_5"); err == nil {
		t.Fatalf("expected induced error")
	}
	// second read should still error because resetFlag(false)
	if _, err := sim.ReadFile(ctx, "foo_5"); err == nil {
		t.Fatalf("expected induced error persist")
	}
}

// Covers defaultFileIO.WriteFile path where initial write fails (missing parent directory),
// MkdirAll succeeds, then retryIO performs the write.
func TestDefaultFileIO_WriteFile_CreatesParentAndRetries(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	target := filepath.Join(base, "nested", "deep", "file.txt")
	dio := NewFileIO()
	if err := dio.WriteFile(ctx, target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

// Covers branch where MkdirAll fails (parent path collision) and original write error is returned.
func TestDefaultFileIO_WriteFile_ParentCreateFails(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// Create a file that will act as a conflicting path component.
	parentFile := filepath.Join(base, "parent")
	if err := os.WriteFile(parentFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("prep parent file: %v", err)
	}
	target := filepath.Join(parentFile, "child", "file.txt") // parentFile is a file, so MkdirAll on its subpath fails
	dio := NewFileIO()
	if err := dio.WriteFile(ctx, target, []byte("data"), 0o644); err == nil {
		t.Fatalf("expected error due to parent create failure")
	}
}
