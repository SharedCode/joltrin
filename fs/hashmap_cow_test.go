package fs

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func TestCow_BasicLifecycle(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	filename := filepath.Join(base, "test.reg")
	offset := int64(4096)
	data := make([]byte, blockSize)
	// Fill data with some pattern
	dataLen := handlesPerBlock * sop.HandleSizeInBytes
	for i := 0; i < dataLen; i++ {
		data[i] = byte(i % 255)
	}
	// Calculate and set checksum
	checksum := crc32.ChecksumIEEE(data[:dataLen])
	binary.LittleEndian.PutUint32(data[dataLen:], checksum)

	// 1. Create COW
	if err := hm.createCow(ctx, filename, offset, data); err != nil {
		t.Fatalf("createCow failed: %v", err)
	}

	// 2. Check COW
	readData, valid, err := hm.checkCow(ctx, filename, offset)
	if err != nil {
		t.Fatalf("checkCow failed: %v", err)
	}
	if !valid {
		t.Fatalf("checkCow returned invalid")
	}
	if len(readData) != len(data) {
		t.Fatalf("readData length mismatch: got %d, want %d", len(readData), len(data))
	}
	for i := range data {
		if readData[i] != data[i] {
			t.Fatalf("readData mismatch at index %d", i)
		}
	}

	// 3. Delete COW
	if err := hm.deleteCow(ctx, filename, offset); err != nil {
		t.Fatalf("deleteCow failed: %v", err)
	}

	// 4. Check COW again (should be gone)
	readData, valid, err = hm.checkCow(ctx, filename, offset)
	if err != nil {
		t.Fatalf("checkCow after delete failed: %v", err)
	}
	if valid {
		t.Fatalf("checkCow should return invalid/not found after delete")
	}
	if readData != nil {
		t.Fatalf("readData should be nil after delete")
	}
}

func TestCow_Restore(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	filename := filepath.Join(base, "restore.reg")
	offset := int64(0)

	// Create a dummy main file
	f, err := os.Create(filename)
	if err != nil {
		t.Fatalf("create main file: %v", err)
	}
	// Write some "corrupted" or old data
	oldData := make([]byte, blockSize)
	f.Write(oldData)
	f.Close()

	// Prepare "good" data for COW
	goodData := make([]byte, blockSize)
	dataLen := handlesPerBlock * sop.HandleSizeInBytes
	for i := 0; i < dataLen; i++ {
		goodData[i] = 0xAA // Pattern
	}
	// Calculate and set checksum
	checksum := crc32.ChecksumIEEE(goodData[:dataLen])
	binary.LittleEndian.PutUint32(goodData[dataLen:], checksum)

	// Create COW file with good data
	if err := hm.createCow(ctx, filename, offset, goodData); err != nil {
		t.Fatalf("createCow failed: %v", err)
	}

	// Setup DirectIO for restore
	dio := newFileDirectIO()
	if err := dio.open(ctx, filename, os.O_RDWR, 0644); err != nil {
		t.Fatalf("dio open failed: %v", err)
	}
	defer dio.close()

	// Perform Restore
	// We need to manually read the COW data first as restoreFromCow expects it
	cowData, valid, err := hm.checkCow(ctx, filename, offset)
	if err != nil || !valid {
		t.Fatalf("checkCow failed before restore: %v, valid: %v", err, valid)
	}

	// The cowData returned by checkCow is just the payload (blockSize).
	// restoreFromCow expects the raw COW data (payload + checksum)?
	// Let's check the implementation of restoreFromCow in hashmap.cow.go.
	// Wait, restoreFromCow signature: func (hm *hashmap) restoreFromCow(ctx context.Context, dio *fileDirectIO, blockOffset int64, alignedBuffer []byte, cowData []byte) error
	// And implementation: copy(alignedBuffer, cowData) -> then write alignedBuffer.
	// So cowData passed to restoreFromCow should be the data to write (blockSize).
	// checkCow returns data[:blockSize]. So this matches.

	alignedBuffer := dio.createAlignedBlock()
	if err := hm.restoreFromCow(ctx, dio, offset, alignedBuffer, cowData); err != nil {
		t.Fatalf("restoreFromCow failed: %v", err)
	}

	// Verify main file now has good data
	readBuf := make([]byte, blockSize)
	if _, err := dio.readAt(ctx, readBuf, offset); err != nil {
		t.Fatalf("readAt failed: %v", err)
	}
	for i := range goodData {
		if readBuf[i] != goodData[i] {
			t.Fatalf("main file data mismatch at %d: got %x, want %x", i, readBuf[i], goodData[i])
		}
	}
}

func TestCow_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	filename := filepath.Join(base, "concurrent.reg")

	var wg sync.WaitGroup
	concurrency := 50

	// Concurrent creation and deletion of COW files for different offsets
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			offset := int64(idx * blockSize)
			data := make([]byte, blockSize)
			binary.LittleEndian.PutUint64(data, uint64(idx))
			// Calculate and set checksum
			dataLen := handlesPerBlock * sop.HandleSizeInBytes
			checksum := crc32.ChecksumIEEE(data[:dataLen])
			binary.LittleEndian.PutUint32(data[dataLen:], checksum)

			// Create
			if err := hm.createCow(ctx, filename, offset, data); err != nil {
				t.Errorf("createCow %d failed: %v", idx, err)
				return
			}

			// Check
			readData, valid, err := hm.checkCow(ctx, filename, offset)
			if err != nil {
				t.Errorf("checkCow %d failed: %v", idx, err)
				return
			}
			if !valid {
				t.Errorf("checkCow %d invalid", idx)
				return
			}
			if binary.LittleEndian.Uint64(readData) != uint64(idx) {
				t.Errorf("data mismatch %d", idx)
				return
			}

			// Delete
			if err := hm.deleteCow(ctx, filename, offset); err != nil {
				t.Errorf("deleteCow %d failed: %v", idx, err)
				return
			}
		}(i)
	}
	wg.Wait()
}

func TestCow_CorruptionDetection(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	filename := filepath.Join(base, "corrupt.reg")
	offset := int64(8192)
	data := make([]byte, blockSize)

	// Manually create a corrupt COW file (bad checksum)
	cowPath := hm.makeBlockCowPath(filename, offset)
	// Ensure dir exists
	os.MkdirAll(filepath.Dir(cowPath), 0755)

	dataLen := handlesPerBlock * sop.HandleSizeInBytes
	// Calculate correct checksum
	checksum := crc32.ChecksumIEEE(data[:dataLen])
	// Write WRONG checksum
	binary.LittleEndian.PutUint32(data[dataLen:], checksum+1)

	if err := os.WriteFile(cowPath, data, 0644); err != nil {
		t.Fatalf("write corrupt cow failed: %v", err)
	}

	// Check COW - should return invalid/false because checksum mismatch
	readData, valid, err := hm.checkCow(ctx, filename, offset)
	if err != nil {
		t.Fatalf("checkCow failed: %v", err)
	}
	if valid {
		t.Fatalf("checkCow should have detected corruption (invalid checksum)")
	}
	if readData != nil {
		t.Fatalf("readData should be nil for corrupt file")
	}
}

func TestCow_DeleteNonExistent(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	filename := filepath.Join(base, "nonexistent.reg")
	offset := int64(0)

	// Delete non-existent COW file
	if err := hm.deleteCow(ctx, filename, offset); err != nil {
		t.Fatalf("deleteCow on non-existent file failed: %v", err)
	}
}
