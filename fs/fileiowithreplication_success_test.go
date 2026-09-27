package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers happy-path replicate actions: write, createStore, removeStore; and exists/read helpers.
func TestFileIOWithReplication_SuccessfulReplicate(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()

	// Replication enabled across two drives.
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	ms := NewManageStoreFolder(NewFileIO())
	fio := newFileIOWithReplication(rt, ms, true)

	// 1) createStore on active should be recorded and then replicated to passive.
	if err := fio.createStore(ctx, "storeA"); err != nil {
		t.Fatalf("createStore: %v", err)
	}

	// 2) write a file within the store and read it back from active to verify write/read.
	relFile := filepath.Join("storeA", "hello.txt")
	if err := fio.write(ctx, relFile, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !fio.exists(ctx, relFile) {
		t.Fatalf("exists should be true after write on active")
	}
	if ba, err := fio.read(ctx, relFile); err != nil || string(ba) != "hi" {
		t.Fatalf("read mismatch: %v %q", err, string(ba))
	}

	// 3) replicate actions to passive and verify passive copies.
	if err := fio.replicate(ctx); err != nil {
		t.Fatalf("replicate: %v", err)
	}

	// Validate in passive: folder and file should exist.
	passiveFolder := rt.formatPassiveFolderEntity("storeA")
	if fi, err := os.Stat(passiveFolder); err != nil || !fi.IsDir() {
		t.Fatalf("expected passive store folder, err=%v", err)
	}
	passiveFile := rt.formatPassiveFolderEntity(relFile)
	if _, err := os.Stat(passiveFile); err != nil {
		t.Fatalf("expected passive replicated file, err=%v", err)
	}

	// 4) removeStore should record and replicate deletion on passive as well.
	if err := fio.removeStore(ctx, "storeA"); err != nil {
		t.Fatalf("removeStore: %v", err)
	}
	if err := fio.replicate(ctx); err != nil {
		t.Fatalf("replicate (remove): %v", err)
	}
	if _, err := os.Stat(passiveFolder); !os.IsNotExist(err) {
		t.Fatalf("expected passive store folder removed, err=%v", err)
	}
}
