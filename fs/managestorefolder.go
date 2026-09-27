package fs

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
)

type manageStoreFolder struct {
	fileIO FileIO
}

// NewManageStoreFolder returns a ManageStore implementation that creates and removes
// directories on the local filesystem.
func NewManageStoreFolder(fileIO FileIO) sop.ManageStore {
	if fileIO == nil {
		fileIO = NewFileIO()
	}
	return &manageStoreFolder{
		fileIO: fileIO,
	}
}

// CreateStore creates the base folder for a store, including intermediate directories.
func (bf *manageStoreFolder) CreateStore(ctx context.Context, blobStoreBaseFolderPath string) error {
	return bf.fileIO.MkdirAll(ctx, blobStoreBaseFolderPath, permission)
}

// RemoveStore recursively deletes the base folder for a store and all of its contents.
func (bf *manageStoreFolder) RemoveStore(ctx context.Context, blobStoreBaseFolderPath string) error {
	return bf.fileIO.RemoveAll(ctx, blobStoreBaseFolderPath)
}
