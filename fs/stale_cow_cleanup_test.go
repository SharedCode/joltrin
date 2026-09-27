package fs

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func Test_StaleCowCleanup_OnUpdate_RollbackRestoresOriginal(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	path := t.TempDir()

	rt, err := NewReplicationTracker(ctx, []string{path}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker failed: %v", err)
	}
	// Ensure table directory exists
	if err := os.MkdirAll(path+"/t1", 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	reg := NewRegistry(true, 250, rt, mocks.NewMockClient())

	// Add Handle (Value 1)
	id := sop.NewUUID()
	h1 := sop.Handle{LogicalID: id, Version: 1}
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "t1", IDs: []sop.Handle{h1}}}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify Value 1
	res, err := reg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "t1", IDs: []sop.UUID{id}}})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(res) == 0 || len(res[0].IDs) == 0 || res[0].IDs[0].Version != 1 {
		t.Fatal("Expected Version 1")
	}

	// 2. Manually Corrupt State (Simulate Crash)
	// - Main File: Version 2 (Corrupted/Partial Write)
	// - COW File: Version 1 (Undo Log)

	hm := reg.hashmap.hashmap
	frd, err := hm.findOneFileRegion(ctx, true, "t1", id)
	if err != nil {
		t.Fatalf("findOneFileRegion failed: %v", err)
	}

	// Prepare Data
	m := encoding.NewHandleMarshaler()

	// Version 2 Data
	h2 := h1
	h2.Version = 2
	buf2, _ := m.Marshal(h2, nil)

	// Write Version 2 to Main File (Simulate Corruption)
	// We use the dio from frd to write directly to the block
	alignedBuf := frd.dio.createAlignedBlock()
	// Read current block first to preserve other handles
	if _, err := frd.dio.readAt(ctx, alignedBuf, frd.blockOffset); err != nil {
		t.Fatalf("readAt failed: %v", err)
	}
	// Update the slot
	copy(alignedBuf[frd.handleInBlockOffset:frd.handleInBlockOffset+sop.HandleSizeInBytes], buf2)
	// Write back
	frd.dio.writeAt(ctx, alignedBuf, frd.blockOffset)

	// Create COW File (Version 1)
	h1Data, _ := m.Marshal(h1, nil)

	cowBlock := make([]byte, blockSize)
	copy(cowBlock[frd.handleInBlockOffset:], h1Data)

	// Calculate and embed checksum
	dataLen := handlesPerBlock * sop.HandleSizeInBytes
	checksum := crc32.ChecksumIEEE(cowBlock[:dataLen])
	binary.LittleEndian.PutUint32(cowBlock[dataLen:], checksum)

	// Use the absolute path from the file handle to ensure the COW path matches what checkCow will generate.
	// frd.dio.filename is relative (e.g. "t1-1.reg"), but checkCow uses dio.file.Name() which is absolute.
	cowPath := hm.makeBlockCowPath(frd.dio.file.Name(), frd.blockOffset)
	fio := NewFileIO()
	if err := fio.WriteFile(ctx, cowPath, cowBlock, 0644); err != nil {
		t.Fatalf("Failed to write COW file: %v", err)
	}

	// Verify State Before Update
	// Get should return Version 1 (because COW exists)
	res, _ = reg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "t1", IDs: []sop.UUID{id}}})
	if res[0].IDs[0].Version != 1 {
		t.Fatalf("Expected Version 1 from COW, got %d", res[0].IDs[0].Version)
	}

	// 3. Perform Update (Value 3) with Fault Injection
	// We want to verify that the Stale COW (V1) was used to restore the Main File *before* the new update.
	// To do this, we inject a failure in the *second* write to the Main File (the update itself).
	// This leaves the "New COW" (Undo Log) on disk.
	// If Stale COW was processed, the New COW will contain V1 (restored).
	// If Stale COW was ignored, the New COW will contain V2 (corrupted).

	// Inject Mock DirectIO
	originalDio := frd.dio.directIO
	mockDio := &mockDirectIO{
		realDio:    originalDio,
		writeCount: 0,
	}
	frd.dio.directIO = mockDio

	h3 := h1
	h3.Version = 3

	// We call hashmap.set
	err = reg.hashmap.set(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "t1", IDs: []sop.Handle{h3}}})

	// Expect error from injected failure
	if err == nil || err.Error() != "injected write failure" {
		t.Fatalf("Expected injected write failure, got: %v", err)
	}

	// 4. Verify New COW Content
	// The New COW should exist and contain Version 1.

	cowContent, err := fio.ReadFile(ctx, cowPath)
	if err != nil {
		t.Fatalf("New COW file not found after failed update: %v", err)
	}
	var hCow sop.Handle
	m.Unmarshal(cowContent[frd.handleInBlockOffset:frd.handleInBlockOffset+sop.HandleSizeInBytes], &hCow)

	if hCow.Version != 1 {
		t.Fatalf("Stale COW Cleanup Failed! Expected New COW to contain Version 1 (Restored), but got Version %d. This means the Stale COW was ignored.", hCow.Version)
	}
}

type mockDirectIO struct {
	realDio    DirectIO
	writeCount int
}

func (m *mockDirectIO) Open(ctx context.Context, filename string, flag int, permission os.FileMode) (*os.File, error) {
	return m.realDio.Open(ctx, filename, flag, permission)
}
func (m *mockDirectIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return m.realDio.ReadAt(ctx, file, block, offset)
}
func (m *mockDirectIO) Close(file *os.File) error {
	return m.realDio.Close(file)
}
func (m *mockDirectIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	m.writeCount++
	// 1st Write: Restore Stale COW (Should succeed)
	// 2nd Write: Update Main File (Should fail)
	if m.writeCount == 2 {
		return 0, fmt.Errorf("injected write failure")
	}
	return m.realDio.WriteAt(ctx, file, block, offset)
}
