package mocks

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
)

type mockBlobStore struct {
	lookup map[sop.UUID][]byte
}

// NewBlobStore instantiates a new (mocked) blobstore.
func NewMockBlobStore() sop.BlobStore {
	return &mockBlobStore{
		lookup: make(map[sop.UUID][]byte),
	}
}

func (b *mockBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	if ba, ok := b.lookup[blobID]; ok {
		return ba, nil
	}
	return nil, nil
}

func (b *mockBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	for _, storeBlobs := range storesblobs {
		for _, blob := range storeBlobs.Blobs {
			ba := blob.Value
			b.lookup[blob.Key] = ba
		}
	}
	return nil
}

func (b *mockBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	for _, storeBlobs := range storesblobs {
		for _, blob := range storeBlobs.Blobs {
			ba := blob.Value
			b.lookup[blob.Key] = ba
		}
	}
	return nil
}

func (b *mockBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	for _, storeBlobIDs := range storesBlobsIDs {
		for _, blobID := range storeBlobIDs.Blobs {
			delete(b.lookup, blobID)
		}
	}
	return nil
}
