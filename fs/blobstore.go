package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sharedcode/joltrin/v5"
)

// BlobStore has no caching built in because blobs are huge, caller code can apply caching on top of it.
// This implementation maps blob IDs to files under a 4-level UUID hierarchy using ToFilePath.
type blobStore struct {
	basePath   string
	fileIO     FileIO
	toFilePath ToFilePathFunc
}

// Directory/File permission.
const permission os.FileMode = os.ModeSticky | os.ModePerm

// NewBlobStore instantiates a new blobstore for File System storage.
// If fileIO or toFilePath are nil, sensible defaults are used.
func NewBlobStore(basePath string, toFilePath ToFilePathFunc, fileIO FileIO) sop.BlobStore {
	if fileIO == nil {
		fileIO = newFileIO(sop.FileIOError)
	}
	if toFilePath == nil {
		toFilePath = DefaultToFilePath
	}
	return &blobStore{
		basePath:   basePath,
		fileIO:     fileIO,
		toFilePath: toFilePath,
	}
}

// GetOne reads and returns the blob data for the given blobID from the provided blobFilePath.
// The file path is computed via toFilePath and then the blob ID is used as filename.
func (b blobStore) GetOne(ctx context.Context, blobFilePath string, blobID sop.UUID) ([]byte, error) {
	if !filepath.IsAbs(blobFilePath) {
		blobFilePath = filepath.Join(b.basePath, blobFilePath)
	}
	fp := b.toFilePath(blobFilePath, blobID)
	fn := fmt.Sprintf("%s%c%s", fp, os.PathSeparator, blobID.String())
	ba, err := b.fileIO.ReadFile(ctx, fn)
	if err != nil {
		return nil, err
	}
	return ba, nil
}

// Add writes the provided blobs to disk under their computed file paths, creating directories as needed.
// Existing files are overwritten.
func (b blobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	for _, storeBlobs := range storesblobs {
		blobFilePath := storeBlobs.BlobTable
		if !filepath.IsAbs(blobFilePath) {
			blobFilePath = filepath.Join(b.basePath, blobFilePath)
		}
		for _, blob := range storeBlobs.Blobs {
			ba := blob.Value
			fp := b.toFilePath(blobFilePath, blob.Key)
			if !b.fileIO.Exists(ctx, fp) {
				if err := b.fileIO.MkdirAll(ctx, fp, permission); err != nil {
					return err
				}
			}
			fn := fmt.Sprintf("%s%c%s", fp, os.PathSeparator, blob.Key.String())
			if err := b.fileIO.WriteFile(ctx, fn, ba, permission); err != nil {
				return err
			}
		}
	}
	return nil
}

// Update replaces existing blobs with the provided data. It is functionally identical to Add.
func (b blobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return b.Add(ctx, storesblobs)
}

// Remove deletes the blobs identified by the given IDs. Non-existent files are ignored.
func (b blobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	for _, storeBlobIDs := range storesBlobsIDs {
		blobFilePath := storeBlobIDs.BlobTable
		if !filepath.IsAbs(blobFilePath) {
			blobFilePath = filepath.Join(b.basePath, blobFilePath)
		}
		for _, blobID := range storeBlobIDs.Blobs {
			fp := b.toFilePath(blobFilePath, blobID)
			fn := fmt.Sprintf("%s%c%s", fp, os.PathSeparator, blobID.String())
			// Do nothing if file already not existent.
			if !b.fileIO.Exists(ctx, fn) {
				continue
			}

			if err := b.fileIO.Remove(ctx, fn); err != nil {
				return err
			}
		}
	}
	return nil
}
