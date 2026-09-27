package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sharedcode/joltrin/v5"
)

const (
	FILE_NOT_FOUND = "no such file or directory"
)

func (hm *hashmap) restoreFromCow(ctx context.Context, dio *fileDirectIO, blockOffset int64, alignedBuffer []byte, cowData []byte) error {
	if len(cowData) == 0 {
		return nil
	}
	copy(alignedBuffer, cowData)
	// Restore the main file from the COW data to fix any potential "torn page" from a previous crash.
	if n, err := dio.writeAt(ctx, alignedBuffer, blockOffset); n != blockSize || err != nil {
		// If we are in Read-Only mode, we can't restore the file, but we have the valid data in memory.
		// We should log it and continue.
		if !hm.readWrite {
			// Log warning?
			return nil
		}
		if err == nil {
			err = fmt.Errorf("stale cow cleanup: only partially (n=%d) wrote at block offset %v", n, blockOffset)
		}
		return sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  err,
		}
	}
	return nil
}

func (hm *hashmap) createCow(ctx context.Context, filename string, offset int64, data []byte) error {
	cowPath := hm.makeBlockCowPath(filename, offset)
	// Ensure directory exists
	cowDir := filepath.Dir(cowPath)
	fio := NewFileIO()
	if !fio.Exists(ctx, cowDir) {
		if err := fio.MkdirAll(ctx, cowDir, 0755); err != nil {
			return sop.Error{
				Code: sop.RestoreRegistryFileSectorFailure,
				Err:  err,
			}
		}
	}

	if err := fio.WriteFile(ctx, cowPath, data, 0644); err != nil {
		return sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  err,
		}
	}
	return nil
}

func (hm *hashmap) deleteCow(ctx context.Context, filename string, offset int64) error {
	cowPath := hm.makeBlockCowPath(filename, offset)
	fio := NewFileIO()
	if err := fio.Remove(ctx, cowPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// Fallback check for string message, in case of wrapping issues or platform specific error messages.
		if strings.Contains(err.Error(), FILE_NOT_FOUND) {
			return nil
		}
		return sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  err,
		}
	}
	return nil
}

func (hm *hashmap) makeBlockCowPath(filename string, offset int64) string {
	if filepath.IsAbs(filename) {
		// Remove .reg extension if present
		base := filename
		if filepath.Ext(base) == registryFileExtension {
			base = base[:len(base)-len(registryFileExtension)]
		}
		return fmt.Sprintf("%s_%d.cow", base, offset)
	}
	cowBase := filename
	// Remove .reg extension if present
	if filepath.Ext(cowBase) == registryFileExtension {
		cowBase = cowBase[:len(cowBase)-len(registryFileExtension)]
	}
	absCowBase := hm.replicationTracker.formatActiveFolderEntity(cowBase)
	return fmt.Sprintf("%s_%d.cow", absCowBase, offset)
}

// checkCow checks and reads COW file, returns the data, if it is valid or not, and the error or nil if no error.
func (hm *hashmap) checkCow(ctx context.Context, filename string, offset int64) ([]byte, bool, error) {
	cowPath := hm.makeBlockCowPath(filename, offset)
	fio := NewFileIO()
	if !fio.Exists(ctx, cowPath) {
		return nil, false, nil
	}
	data, err := fio.ReadFile(ctx, cowPath)
	if err != nil {
		return nil, false, sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  err,
		}
	}
	if len(data) == 0 {
		// Empty file means it was a claim but no data was written (crash before backup).
		// Safe to delete.
		return nil, true, nil
	}
	if len(data) != blockSize {
		// Invalid data size.
		return nil, false, nil
	}
	// Verify embedded checksum using unmarshalData, which handles both checksum verification
	// and the "all zeros" optimization.
	if _, err := unmarshalData(data); err == nil {
		return data, true, nil
	}
	return nil, false, nil
}
