package memory

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

func (s *store[T]) CategoriesByDistance(ctx context.Context) (btree.BtreeInterface[DistanceKey, byte], error) {
	return s.categoriesByDistance, nil
}

// AddCategory dynamically inserts a new explicit sub-category directly into the B-Tree.
// During creation, it only links to the Primary Parent (the first parent in the array).
// Connecting a category to multiple parents is handled via AddCategoryParent.
func (s *store[T]) AddCategory(ctx context.Context, c *Category) (sop.UUID, error) {
	if c == nil {
		return sop.UUID{}, fmt.Errorf("category cannot be nil")
	}

	// Validate/Ensure ID
	if c.ID.IsNil() {
		c.ID = sop.NewUUID()
	}

	// Lock the transaction on this insert in the categories BTree
	ok, err := s.categories.Add(ctx, c.ID, c)
	if err != nil {
		return sop.UUID{}, fmt.Errorf("failed to insert new category: %w", err)
	}
	// Maintain the Path index so it can be dynamically resolved by root paths
	path := c.Path
	if path == "" {
		path = c.Name
	}
	if path != "" && s.categoriesByPath != nil {
		s.categoriesByPath.Add(ctx, path, c.ID)
	}
	if !ok {
		return sop.UUID{}, fmt.Errorf("category with ID %v already exists", c.ID)
	}

	// CategoriesByDistance is not maintained during AddCategory, Vectorize will populate it.

	// Establish the primary hierarchy link if provided during creation
	if len(c.ParentIDs) > 0 {
		cp := c.ParentIDs[0]
		if !cp.ParentID.IsNil() {
			ok, err := s.categories.Find(ctx, cp.ParentID, false)
			if err != nil {
				return sop.UUID{}, fmt.Errorf("failed to lookup parent category: %w", err)
			}
			if ok {
				parent, err := s.categories.GetCurrentValue(ctx)
				if err != nil {
					return sop.UUID{}, fmt.Errorf("failed to fetch parent category value: %w", err)
				}
				// Link the target category to the parent category hierarchy
				parent.ChildrenIDs = append(parent.ChildrenIDs, c.ID)
				_, err = s.categories.UpdateCurrentItem(ctx, cp.ParentID, parent)
				if err != nil {
					return sop.UUID{}, fmt.Errorf("failed to link category to primary parent: %w", err)
				}
			}
		}
	}

	return c.ID, nil
}

// AddCategoryParent connects an existing category to an additional parent, supporting
// the polyhierarchy DAG structure. This is often leveraged during LLM Sleep Cycles.

func (s *store[T]) AddCategoryParent(ctx context.Context, categoryID sop.UUID, newParent CategoryParent) error {
	if newParent.ParentID.IsNil() || categoryID.IsNil() {
		return fmt.Errorf("category and parent IDs must be valid")
	}

	// 1. Validate the target category exists and add the new parent relationship
	ok, err := s.categories.Find(ctx, categoryID, false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("target category not found")
	}

	category, err := s.categories.GetCurrentValue(ctx)
	if err != nil {
		return err
	}

	// Prevent duplicate parent links
	for _, p := range category.ParentIDs {
		if p.ParentID == newParent.ParentID {
			// Already a parent, just update the use-case
			return s.updateCategoryParent(ctx, categoryID, newParent)
		}
	}

	// 2. Validate the parent exists before modifying the child
	ok, err = s.categories.Find(ctx, newParent.ParentID, false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("parent category not found")
	}

	parent, err := s.categories.GetCurrentValue(ctx)
	if err != nil {
		return err
	}

	// Prevent duplicate child links
	for _, cid := range parent.ChildrenIDs {
		if cid == categoryID {
			return nil // Link already exists downstream
		}
	}

	// 3. Mutate the parent
	parent.ChildrenIDs = append(parent.ChildrenIDs, categoryID)
	_, err = s.categories.UpdateCurrentItem(ctx, newParent.ParentID, parent)
	if err != nil {
		return err
	}

	// 4. Seek back to the child and mutate it
	ok, err = s.categories.Find(ctx, categoryID, false)
	if err != nil || !ok {
		return fmt.Errorf("failed to seek back to target category: %w", err)
	}

	category.ParentIDs = append(category.ParentIDs, newParent)
	_, err = s.categories.UpdateCurrentItem(ctx, categoryID, category)
	if err != nil {
		return err
	}

	return nil
}

// updateCategoryParent updates the metadata of an existing parent link.
func (s *store[T]) updateCategoryParent(ctx context.Context, categoryID sop.UUID, updatedParent CategoryParent) error {
	ok, err := s.categories.Find(ctx, categoryID, false)
	if err != nil || !ok {
		return err
	}
	category, err := s.categories.GetCurrentValue(ctx)
	if err != nil {
		return err
	}

	for i, p := range category.ParentIDs {
		if p.ParentID == updatedParent.ParentID {
			category.ParentIDs[i].UseCase = updatedParent.UseCase
			break
		}
	}

	_, err = s.categories.UpdateCurrentItem(ctx, categoryID, category)
	return err
}

func (s *store[T]) CategoriesByPath(ctx context.Context) (btree.BtreeInterface[string, sop.UUID], error) {
	return s.categoriesByPath, nil
}
