package inmemory

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// Ensure NodeRepository.Add and Fetched are exercised (previously 0%).
func TestNodeRepository_Add_And_Fetched(t *testing.T) {
	nr := newNodeRepository[int, string]()
	repo := nr.(*nodeRepository[int, string])

	// Add a node with a non-nil ID
	n1 := &btree.Node[int, string]{ID: sop.NewUUID()}
	repo.Add(n1)
	if got, _ := repo.Get(nil, n1.ID); got != n1 {
		t.Fatalf("node not added")
	}
	// Add a node with NilUUID (valid map key in-memory); then Fetched no-op.
	var nilID sop.UUID
	n2 := &btree.Node[int, string]{ID: nilID}
	repo.Add(n2)
	repo.Fetched(n2.ID) // should not panic
	if got, _ := repo.Get(nil, n2.ID); got != n2 {
		t.Fatalf("nil-ID node not added")
	}
}
