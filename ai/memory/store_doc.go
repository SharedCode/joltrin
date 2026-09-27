package memory

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

func (s *store[T]) Documents(ctx context.Context) (btree.BtreeInterface[sop.UUID, Document], error) {
	return s.documents, nil
}

func (s *store[T]) UpsertDocument(ctx context.Context, doc Document) error {
	if s.documents == nil {
		return fmt.Errorf("documents store is not initialized")
	}
	if doc.ID.IsNil() {
		doc.ID = sop.NewUUID()
	}
	_, err := s.documents.Upsert(ctx, doc.ID, doc)
	return err
}

func (s *store[T]) GetDocument(ctx context.Context, id sop.UUID) (*Document, error) {
	if s.documents == nil {
		return nil, fmt.Errorf("documents store is not initialized")
	}
	if id.IsNil() {
		return nil, fmt.Errorf("document ID cannot be nil")
	}
	found, err := s.documents.Find(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // Return nil on not found
	}
	doc, err := s.documents.GetCurrentValue(ctx)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}
