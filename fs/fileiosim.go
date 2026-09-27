package fs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

type simFileInfo struct{}

func (simFileInfo) Name() string       { return "sim" }
func (simFileInfo) Size() int64        { return 0 }
func (simFileInfo) Mode() os.FileMode  { return 0 }
func (simFileInfo) ModTime() time.Time { return time.Now() }
func (simFileInfo) IsDir() bool        { return false }
func (simFileInfo) Sys() any           { return nil }

type fileIOSimulator struct {
	lookup map[string][]byte
	locker sync.Mutex
	// The error flags are manipulated by tests concurrently; use atomics to avoid races.
	errorOnSuffixNumber  atomic.Int32
	errorOnSuffixNumber2 atomic.Int32
	resetFlag            atomic.Bool
}

func newFileIOSim() *fileIOSimulator {
	sim := &fileIOSimulator{
		lookup: make(map[string][]byte),
		locker: sync.Mutex{},
	}
	sim.errorOnSuffixNumber.Store(-1)
	sim.errorOnSuffixNumber2.Store(-1)
	return sim
}

// ToFilePath is part of FileIO so we can allow implementations to drive
// generation of full path filename.
func (sim *fileIOSimulator) ToFilePath(basePath string, id sop.UUID) string {
	return ""
}

func (sim *fileIOSimulator) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	n := sim.errorOnSuffixNumber.Load()
	if n >= 0 && strings.HasSuffix(name, fmt.Sprintf("_%d", n)) {
		return fmt.Errorf("induced error on file suffix %d", n)
	}
	sim.locker.Lock()
	defer sim.locker.Unlock()
	sim.lookup[name] = data
	return nil
}
func (sim *fileIOSimulator) ReadFile(ctx context.Context, name string) ([]byte, error) {
	n := sim.errorOnSuffixNumber.Load()
	if n >= 0 && strings.HasSuffix(name, fmt.Sprintf("_%d", n)) {
		return nil, fmt.Errorf("induced error on file suffix %d", n)
	}
	n2 := sim.errorOnSuffixNumber2.Load()
	if n2 >= 0 && strings.HasSuffix(name, fmt.Sprintf("_%d", n2)) {
		if sim.resetFlag.Load() {
			sim.errorOnSuffixNumber.Store(-1)
			sim.errorOnSuffixNumber2.Store(-1)
		}
		return nil, fmt.Errorf("induced error on file suffix %d", n2)
	}
	sim.locker.Lock()
	defer sim.locker.Unlock()

	if _, ok := sim.lookup[name]; !ok {
		return nil, fmt.Errorf("file %s not found", name)
	}
	ba := sim.lookup[name]
	return ba, nil
}
func (sim *fileIOSimulator) Remove(ctx context.Context, name string) error {
	n := sim.errorOnSuffixNumber.Load()
	if n >= 0 && strings.HasSuffix(name, fmt.Sprintf("_%d", n)) {
		return fmt.Errorf("induced error on file suffix %d", n)
	}
	sim.locker.Lock()
	defer sim.locker.Unlock()
	delete(sim.lookup, name)
	return nil
}
func (sim *fileIOSimulator) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	return simFileInfo{}, nil
}
func (sim *fileIOSimulator) Exists(ctx context.Context, path string) bool {
	return true
}

// Directory API.
func (sim *fileIOSimulator) RemoveAll(ctx context.Context, path string) error {
	return nil
}
func (sim *fileIOSimulator) MkdirAll(ctx context.Context, path string, perm os.FileMode) error {
	return nil
}

func (sim *fileIOSimulator) ReadDir(ctx context.Context, sourceDir string) ([]os.DirEntry, error) {
	return nil, nil
}

// Test helpers for atomically setting error flags to avoid data races in tests.
func (sim *fileIOSimulator) setErrorOnSuffixNumber(v int) {
	sim.errorOnSuffixNumber.Store(int32(v))
}

func (sim *fileIOSimulator) setErrorOnSuffixNumber2(v int) {
	sim.errorOnSuffixNumber2.Store(int32(v))
}

func (sim *fileIOSimulator) setResetFlag(v bool) {
	sim.resetFlag.Store(v)
}
