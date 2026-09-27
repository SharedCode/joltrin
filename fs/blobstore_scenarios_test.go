package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

type dummyFileInfo struct{}

func (dummyFileInfo) Name() string       { return "dummy" }
func (dummyFileInfo) Size() int64        { return 0 }
func (dummyFileInfo) Mode() os.FileMode  { return 0 }
func (dummyFileInfo) ModTime() time.Time { return time.Now() }
func (dummyFileInfo) IsDir() bool        { return false }
func (dummyFileInfo) Sys() any           { return nil }

// Local unique helper (avoid name clash with existing tests); mirrors errFileIO behavior.
type scenarioErrFileIO struct {
	failMkdir, failWrite, failRead, failRemove bool
	existsAlways                               bool
}

func (e *scenarioErrFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	if e.failWrite {
		return errors.New("write failure")
	}
	_ = os.MkdirAll(filepath.Dir(name), 0o755)
	return os.WriteFile(name, data, perm)
}
func (e *scenarioErrFileIO) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if e.failRead {
		return nil, errors.New("read failure")
	}
	return os.ReadFile(name)
}
func (e *scenarioErrFileIO) Remove(ctx context.Context, name string) error {
	if e.failRemove {
		return errors.New("remove failure")
	}
	return os.Remove(name)
}
func (e *scenarioErrFileIO) Exists(ctx context.Context, path string) bool { return e.existsAlways }
func (e *scenarioErrFileIO) RemoveAll(ctx context.Context, path string) error {
	return os.RemoveAll(path)
}
func (e *scenarioErrFileIO) MkdirAll(ctx context.Context, path string, perm os.FileMode) error {
	if e.failMkdir {
		return errors.New("mkdir failure")
	}
	return os.MkdirAll(path, perm)
}
func (e *scenarioErrFileIO) ReadDir(ctx context.Context, sourceDir string) ([]os.DirEntry, error) {
	return nil, nil
}
func (e *scenarioErrFileIO) List(ctx context.Context, path string) ([]string, error) {
	return nil, nil
}
func (e *scenarioErrFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	if e.existsAlways {
		return dummyFileInfo{}, nil
	}
	return nil, os.ErrNotExist
}

// scenarioFailingRemoveFileIO mirrors failingRemoveFileIO from legacy tests with a unique name.
type scenarioFailingRemoveFileIO struct {
	FileIO
	fail bool
	mu   sync.Mutex
}

func (f *scenarioFailingRemoveFileIO) Remove(ctx context.Context, name string) error {
	f.mu.Lock()
	if f.fail {
		f.fail = false
		f.mu.Unlock()
		return errors.New("remove shard fail")
	}
	f.mu.Unlock()
	return f.FileIO.Remove(ctx, name)
}

// Failing multi shard writer to exceed parity quickly.
type scenarioFailingMultiShardFileIO struct {
	failWrites map[int]struct{}
	mu         sync.Mutex
	store      map[string][]byte
}

func newScenarioFailingMultiShardFileIO(fails ...int) *scenarioFailingMultiShardFileIO {
	m := make(map[int]struct{}, len(fails))
	for _, i := range fails {
		m[i] = struct{}{}
	}
	return &scenarioFailingMultiShardFileIO{failWrites: m, store: make(map[string][]byte)}
}
func (f *scenarioFailingMultiShardFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	parts := strings.Split(name, "_")
	if len(parts) > 0 {
		if idxStr := parts[len(parts)-1]; idxStr != "" {
			var idx int
			fmt.Sscanf(idxStr, "%d", &idx)
			if _, ok := f.failWrites[idx]; ok {
				return fmt.Errorf("induced write failure on shard %d", idx)
			}
		}
	}
	f.mu.Lock()
	f.store[name] = data
	f.mu.Unlock()
	return nil
}
func (f *scenarioFailingMultiShardFileIO) ReadFile(ctx context.Context, name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ba, ok := f.store[name]; ok {
		return ba, nil
	}
	return nil, fmt.Errorf("file %s not found", name)
}
func (f *scenarioFailingMultiShardFileIO) Remove(ctx context.Context, name string) error {
	f.mu.Lock()
	delete(f.store, name)
	f.mu.Unlock()
	return nil
}
func (f *scenarioFailingMultiShardFileIO) Exists(context.Context, string) bool     { return true }
func (f *scenarioFailingMultiShardFileIO) RemoveAll(context.Context, string) error { return nil }
func (f *scenarioFailingMultiShardFileIO) MkdirAll(context.Context, string, os.FileMode) error {
	return nil
}
func (f *scenarioFailingMultiShardFileIO) ReadDir(context.Context, string) ([]os.DirEntry, error) {
	return nil, nil
}
func (f *scenarioFailingMultiShardFileIO) List(context.Context, string) ([]string, error) {
	return nil, nil
}
func (f *scenarioFailingMultiShardFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	return dummyFileInfo{}, nil
}

// FileIO that fails specific shard indices for a given blob id (parity exceed / tolerated scenarios).
type scenarioFileIOWithShardFail struct {
	FileIO
	blobID       sop.UUID
	errorIndices map[int]struct{}
	mu           sync.Mutex
}

func (f *scenarioFileIOWithShardFail) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	if strings.Contains(name, f.blobID.String()+"_") {
		if u := strings.LastIndex(name, "_"); u > -1 {
			var idx int
			if _, err := fmt.Sscanf(name[u+1:], "%d", &idx); err == nil {
				f.mu.Lock()
				_, shouldFail := f.errorIndices[idx]
				if shouldFail {
					delete(f.errorIndices, idx)
					f.mu.Unlock()
					return fmt.Errorf("injected shard write failure index %d", idx)
				}
				f.mu.Unlock()
			}
		}
	}
	return f.FileIO.WriteFile(ctx, name, data, perm)
}

type scenarioErrFileIOAlwaysErr struct{ FileIO }

func (e scenarioErrFileIOAlwaysErr) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return nil, errors.New("boom")
}

// scenarioFailMkdirFileIO forces MkdirAll error (for EC Add branch) while allowing other ops.
type scenarioFailMkdirFileIO struct{ FileIO }

func (f scenarioFailMkdirFileIO) Exists(context.Context, string) bool { return false }
func (f scenarioFailMkdirFileIO) MkdirAll(context.Context, string, os.FileMode) error {
	return errors.New("mkdir all induced error")
}

// scenarioRepairWriteFailFileIO fails a WriteFile after a certain count (used to cover repair warning path).
type scenarioRepairWriteFailFileIO struct {
	FileIO
	mu        sync.Mutex
	writes    int
	failAfter int
}

func (f *scenarioRepairWriteFailFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.writes > f.failAfter {
		return errors.New("repair write failure")
	}
	return f.FileIO.WriteFile(ctx, name, data, perm)
}

func TestBlobStore_AllScenarios(t *testing.T) {
	ctx := context.Background()
	type scenario struct {
		name string
		run  func(t *testing.T)
	}
	scenarios := []scenario{
		{name: "BasicLifecycle", run: func(t *testing.T) {
			base := t.TempDir()
			bs := NewBlobStore("", nil, nil)
			id1, id2 := sop.NewUUID(), sop.NewUUID()
			payload := []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: base, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id1, Value: []byte("v1")}, {Key: id2, Value: []byte("v2")}}}}
			if err := bs.Add(ctx, payload); err != nil {
				t.Fatalf("Add: %v", err)
			}
			payload[0].Blobs[0].Value = []byte("v1b")
			if err := bs.Update(ctx, payload); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if b, err := bs.GetOne(ctx, base, id1); err != nil || string(b) != "v1b" {
				t.Fatalf("GetOne id1: %v %s", err, string(b))
			}
			if b2, err := bs.GetOne(ctx, base, id2); err != nil || string(b2) != "v2" {
				t.Fatalf("GetOne id2: %v %s", err, string(b2))
			}
			if err := bs.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: base, Blobs: []sop.UUID{id1}}}); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := bs.GetOne(ctx, base, id1); err == nil {
				t.Fatalf("expected error removed")
			}
			if b2, err := bs.GetOne(ctx, base, id2); err != nil || string(b2) != "v2" {
				t.Fatalf("GetOne id2 after id1 removal: %v %s", err, string(b2))
			}
		}},
		{name: "OverwriteAndRemoveMissing", run: func(t *testing.T) {
			base := t.TempDir()
			bs := NewBlobStore("", nil, nil)
			id := sop.NewUUID()
			pay := []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: base, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("one")}}}}
			if err := bs.Add(ctx, pay); err != nil {
				t.Fatalf("add1: %v", err)
			}
			pay[0].Blobs[0].Value = []byte("two")
			if err := bs.Add(ctx, pay); err != nil {
				t.Fatalf("overwrite: %v", err)
			}
			if g, _ := bs.GetOne(ctx, base, id); string(g) != "two" {
				t.Fatalf("overwrite mismatch")
			}
			missing := sop.NewUUID()
			if err := bs.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: base, Blobs: []sop.UUID{missing}}}); err != nil {
				t.Fatalf("remove missing: %v", err)
			}
		}},
		{name: "ErrorBranches", run: func(t *testing.T) {
			base := t.TempDir()
			id := sop.NewUUID()
			table := filepath.Join(base, "tbl")
			tests := []struct {
				name string
				fio  FileIO
				op   func(bs sop.BlobStore) error
				want string
			}{{"mkdir", &scenarioErrFileIO{failMkdir: true}, func(bs sop.BlobStore) error {
				return bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("x")}}}})
			}, "mkdir failure"}, {"write", &scenarioErrFileIO{failWrite: true}, func(bs sop.BlobStore) error {
				return bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("x")}}}})
			}, "write failure"}, {"read", &scenarioErrFileIO{failRead: true}, func(bs sop.BlobStore) error { _, e := bs.GetOne(ctx, table, id); return e }, "read failure"}, {"remove", &scenarioErrFileIO{failRemove: true, existsAlways: true}, func(bs sop.BlobStore) error {
				return bs.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: table, Blobs: []sop.UUID{id}}})
			}, "remove failure"}}
			for _, tc := range tests {
				bs := NewBlobStore("", nil, tc.fio)
				if err := tc.op(bs); err == nil || err.Error() != tc.want {
					t.Fatalf("%s want %q got %v", tc.name, tc.want, err)
				}
			}
		}},
		{name: "FileIOPrimitives", run: func(t *testing.T) {
			dio := NewFileIO()
			base := t.TempDir()
			nested := filepath.Join(base, "a", "b", "c")
			fn := filepath.Join(nested, "f.txt")
			if err := dio.WriteFile(ctx, fn, []byte("x"), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if !dio.Exists(ctx, fn) || !dio.Exists(ctx, nested) {
				t.Fatalf("Exists mismatch")
			}
			if dio.Exists(ctx, filepath.Join(base, "no")) {
				t.Fatalf("Exists false expected")
			}
			if _, err := dio.ReadDir(ctx, fn); err == nil {
				t.Fatalf("expected ReadDir error")
			}
			if err := dio.Remove(ctx, fn); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if err := dio.RemoveAll(ctx, filepath.Join(base, "a")); err != nil {
				t.Fatalf("RemoveAll: %v", err)
			}
		}},
		{name: "EC_ConfigValidation", run: func(t *testing.T) {
			bad := map[string]sop.ErasureCodingConfig{"t": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"only-two"}}}
			if _, err := NewBlobStoreWithEC(nil, nil, bad); err == nil {
				t.Fatalf("expected error mismatched config")
			}
		}},
		{name: "EC_GlobalSetGetReset", run: func(t *testing.T) {
			SetGlobalErasureConfig(nil)
			if GetGlobalErasureConfig() != nil {
				t.Fatalf("expected nil")
			}
			cfg := map[string]sop.ErasureCodingConfig{"": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"x", "y", "z"}}}
			SetGlobalErasureConfig(cfg)
			if len(GetGlobalErasureConfig()) != 1 {
				t.Fatalf("len mismatch")
			}
		}},
		{name: "EC_GlobalFallback", run: func(t *testing.T) {
			root := t.TempDir()
			p1 := filepath.Join(root, "d1")
			p2 := filepath.Join(root, "d2")
			p3 := filepath.Join(root, "d3")
			for _, d := range []string{p1, p2, p3} {
				os.MkdirAll(d, 0o755)
			}
			prev := GetGlobalErasureConfig()
			defer SetGlobalErasureConfig(prev)
			SetGlobalErasureConfig(map[string]sop.ErasureCodingConfig{"": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{p1, p2, p3}, RepairCorruptedShards: true}})
			bs, _ := NewBlobStoreWithEC(nil, nil, nil)
			tbl := "b_fallback"
			id := sop.NewUUID()
			pay := []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: tbl, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("data")}}}}
			if err := bs.Add(ctx, pay); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if g, err := bs.GetOne(ctx, tbl, id); err != nil || string(g) != "data" {
				t.Fatalf("GetOne mismatch: %v", err)
			}
		}},
		{name: "EC_AddParityToleranceAndExceed", run: func(t *testing.T) {
			base := t.TempDir()
			cfg := map[string]sop.ErasureCodingConfig{"tbl": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")}}}
			id := sop.NewUUID()
			within := &scenarioFileIOWithShardFail{FileIO: NewFileIO(), blobID: id, errorIndices: map[int]struct{}{1: {}}}
			bs1, _ := NewBlobStoreWithEC(nil, within, cfg)
			if err := bs1.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tbl", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("p")}}}}); err != nil {
				t.Fatalf("within parity: %v", err)
			}
			id2 := sop.NewUUID()
			exceed := &scenarioFileIOWithShardFail{FileIO: NewFileIO(), blobID: id2, errorIndices: map[int]struct{}{0: {}, 2: {}}}
			bs2, _ := NewBlobStoreWithEC(nil, exceed, cfg)
			if err := bs2.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tbl", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id2, Value: []byte("q")}}}}); err == nil {
				t.Fatalf("expected parity exceed error")
			}
		}},
		{name: "EC_AddMkdirAllFailure", run: func(t *testing.T) {
			base := t.TempDir()
			cfg := map[string]sop.ErasureCodingConfig{"t": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{filepath.Join(base, "a"), filepath.Join(base, "b")}}}
			fio := scenarioFailMkdirFileIO{FileIO: NewFileIO()}
			bs, _ := NewBlobStoreWithEC(nil, fio, cfg)
			id := sop.NewUUID()
			err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "t", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("x")}}}})
			if err == nil || !strings.Contains(err.Error(), "mkdir all induced error") {
				t.Fatalf("expected mkdir failure, got %v", err)
			}
		}},
		{name: "EC_AddExceedsParityReadOnlyDrives", run: func(t *testing.T) {
			root := t.TempDir()
			p1 := filepath.Join(root, "d1")
			p2 := filepath.Join(root, "d2")
			p3 := filepath.Join(root, "d3")
			for _, d := range []string{p1, p2, p3} {
				os.MkdirAll(d, 0o755)
			}
			os.Chmod(p1, 0o555)
			os.Chmod(p2, 0o555)
			prev := GetGlobalErasureConfig()
			defer SetGlobalErasureConfig(prev)
			table := "tbl_ec_err"
			SetGlobalErasureConfig(map[string]sop.ErasureCodingConfig{table: {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{p1, p2, p3}, RepairCorruptedShards: true}})
			bs, _ := NewBlobStoreWithEC(DefaultToFilePath, nil, nil)
			id := sop.NewUUID()
			if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("abc")}}}}); err == nil {
				t.Fatalf("expected add fail parity exceed")
			}
		}},
		{name: "EC_AddMixedExistingShardDirs", run: func(t *testing.T) {
			base1 := filepath.Join(t.TempDir(), "d1")
			base2 := filepath.Join(t.TempDir(), "d2")
			cfg := map[string]sop.ErasureCodingConfig{"tbl2": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{base1, base2}}}
			bsIntf, _ := NewBlobStoreWithEC(nil, nil, cfg)
			bs := bsIntf.(*BlobStoreWithEC)
			id := sop.NewUUID()
			pre := DefaultToFilePath(filepath.Join(base1, "tbl2"), id)
			os.MkdirAll(pre, 0o755)
			if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tbl2", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("xyz")}}}}); err != nil {
				t.Fatalf("add mixed: %v", err)
			}
		}},
		{name: "EC_RepairCorruptedShard", run: func(t *testing.T) {
			root := t.TempDir()
			d1 := filepath.Join(root, "d1")
			d2 := filepath.Join(root, "d2")
			d3 := filepath.Join(root, "d3")
			for _, d := range []string{d1, d2, d3} {
				os.MkdirAll(d, 0o755)
			}
			cfg := map[string]sop.ErasureCodingConfig{"rt": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2, d3}, RepairCorruptedShards: true}}
			bsIntf, _ := NewBlobStoreWithEC(nil, nil, cfg)
			bs := bsIntf.(*BlobStoreWithEC)
			id := sop.NewUUID()
			payload := []byte("repair-path")
			bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "rt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: payload}}}})
			del := ""
			for i, base := range []string{d1, d2, d3} {
				fp := bs.toFilePath(filepath.Join(base, "rt"), id)
				sf := filepath.Join(fp, id.String()+fmt.Sprintf("_%d", i))
				if i == 0 {
					del = sf
				}
				if _, err := os.Stat(sf); err != nil {
					t.Fatalf("missing shard %d", i)
				}
			}
			os.Remove(del)
			if got, err := bs.GetOne(ctx, "rt", id); err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("GetOne after repair: %v", err)
			}
		}},
		{name: "EC_RepairShardWriteFailWarning", run: func(t *testing.T) {
			root := t.TempDir()
			d1 := filepath.Join(root, "d1")
			d2 := filepath.Join(root, "d2")
			d3 := filepath.Join(root, "d3")
			for _, d := range []string{d1, d2, d3} {
				os.MkdirAll(d, 0o755)
			} // failAfter=3 (initial 3 shard writes), so repair attempt triggers failure
			fio := &scenarioRepairWriteFailFileIO{FileIO: NewFileIO(), failAfter: 3}
			cfg := map[string]sop.ErasureCodingConfig{"rt2": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2, d3}, RepairCorruptedShards: true}}
			bsIntf, _ := NewBlobStoreWithEC(nil, fio, cfg)
			bs := bsIntf.(*BlobStoreWithEC)
			id := sop.NewUUID()
			data := []byte("repairwarn")
			if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "rt2", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: data}}}}); err != nil {
				t.Fatalf("add: %v", err)
			} // remove one shard to trigger repair
			fp := bs.toFilePath(filepath.Join(d1, "rt2"), id)
			entries, _ := os.ReadDir(fp)
			if len(entries) == 0 {
				t.Fatalf("no shards")
			}
			os.Remove(filepath.Join(fp, entries[0].Name())) // GetOne triggers repair; we ignore returned warning (logged)
			if got, err := bs.GetOne(ctx, "rt2", id); err != nil || !bytes.Equal(got, data) {
				t.Fatalf("GetOne after repair with write fail: %v", err)
			}
		}},
		{name: "EC_RepairPartialFailure", run: func(t *testing.T) {
			base := t.TempDir()
			d1 := filepath.Join(base, "d1")
			d2 := filepath.Join(base, "d2")
			d3 := filepath.Join(base, "d3")
			cfg := map[string]sop.ErasureCodingConfig{"tbp": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2, d3}, RepairCorruptedShards: true}}
			bsIntf, _ := NewBlobStoreWithEC(nil, nil, cfg)
			bs := bsIntf.(*BlobStoreWithEC)
			id := sop.NewUUID()
			pay := []byte("abcdefgh")
			bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tbp", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: pay}}}})
			fp := bs.toFilePath(d3, id)
			entries, _ := os.ReadDir(fp)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), "_2") {
					os.Remove(filepath.Join(fp, e.Name()))
				}
			}
			if g, err := bs.GetOne(ctx, "tbp", id); err != nil || string(g) != string(pay) {
				t.Fatalf("GetOne partial repair: %v", err)
			}
		}},
		{name: "EC_RemoveErrorTolerance", run: func(t *testing.T) {
			b1 := filepath.Join(t.TempDir(), "d1")
			b2 := filepath.Join(t.TempDir(), "d2")
			cfg := map[string]sop.ErasureCodingConfig{"tbl": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{b1, b2}}}
			fio := &scenarioFailingRemoveFileIO{FileIO: NewFileIO(), fail: true}
			bsIntf, _ := NewBlobStoreWithEC(nil, fio, cfg)
			bs := bsIntf.(*BlobStoreWithEC)
			id := sop.NewUUID()
			bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tbl", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("abc")}}}})
			if err := bs.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: "tbl", Blobs: []sop.UUID{id}}}); err != nil {
				t.Fatalf("Remove tolerance: %v", err)
			}
		}},
		{name: "EC_AddExceedsParityMultiShardFileIO", run: func(t *testing.T) {
			fio := newScenarioFailingMultiShardFileIO(0, 1)
			cfg := map[string]sop.ErasureCodingConfig{"bX": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"d1", "d2", "d3"}}}
			bs, _ := NewBlobStoreWithEC(DefaultToFilePath, fio, cfg)
			id := sop.NewUUID()
			if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bX", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("abc")}}}}); err == nil {
				t.Fatalf("expected parity exceed error (multi shard fail)")
			}
		}},
		{name: "EC_AddEmptyInputNoOp", run: func(t *testing.T) {
			bs, _ := NewBlobStoreWithEC(DefaultToFilePath, newFileIOSim(), nil)
			if err := bs.Add(ctx, nil); err != nil {
				t.Fatalf("expected nil")
			}
		}},
		{name: "Plain_AddEmptyInputNoOp", run: func(t *testing.T) {
			bs := NewBlobStore("", nil, nil)
			if err := bs.Add(ctx, nil); err != nil {
				t.Fatalf("expected nil add no-op")
			}
		}},
		{name: "EC_GetOneAllShardsMissing", run: func(t *testing.T) {
			fileIO := newFileIOSim()
			bs, _ := NewBlobStoreWithEC(DefaultToFilePath, fileIO, nil)
			id := sop.NewUUID()
			data := []byte("payload")
			bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "b1", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: data}}}})
			uid := id.String()
			fileIO.locker.Lock()
			for i := 0; i < 3; i++ {
				delete(fileIO.lookup, filepath.Join("/", fmt.Sprintf("%s_%d", uid, i)))
			}
			fileIO.locker.Unlock()
			fileIO.setErrorOnSuffixNumber(0)
			fileIO.setErrorOnSuffixNumber2(1)
			if _, err := bs.GetOne(ctx, "b1", id); err == nil {
				t.Fatalf("expected error all shards missing")
			}
		}},
		{name: "EC_GetOneAllShardFailures", run: func(t *testing.T) {
			base := t.TempDir()
			cfg := map[string]sop.ErasureCodingConfig{"table": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{filepath.Join(base, "d1"), filepath.Join(base, "d2"), filepath.Join(base, "d3")}}}
			bs, _ := NewBlobStoreWithEC(nil, scenarioErrFileIOAlwaysErr{FileIO: NewFileIO()}, cfg)
			if _, err := bs.(*BlobStoreWithEC).GetOne(ctx, "table", sop.NewUUID()); err == nil {
				t.Fatalf("expected shard read error")
			}
		}},
		{name: "EC_RepairSuccessful", run: func(t *testing.T) {
			fio := newFileIOSim()
			bs, _ := NewBlobStoreWithEC(DefaultToFilePath, fio, nil)
			id := sop.NewUUID()
			data := []byte{9, 9, 9}
			bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "b1", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: data}}}})
			fio.setErrorOnSuffixNumber2(1)
			if got, err := bs.GetOne(ctx, "b1", id); err != nil || !bytes.Equal(got, data) {
				t.Fatalf("repair successful mismatch: %v", err)
			}
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) { sc.run(t) })
	}
}
