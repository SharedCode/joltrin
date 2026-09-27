package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestUpsert_Table(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Prepare root
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	// seed repo
	if fr, ok := b.storeInterface.NodeRepository.(*fakeNR[int, string]); ok {
		fr.Add(root)
	}

	tests := []struct {
		name      string
		seedKey   int
		upsertKey int
		seedVal   string
		upsertVal string
		wantVal   string
	}{
		{"add new", 0, 10, "", "aa", "aa"},
		{"update existing", 20, 20, "bb", "cc", "cc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// optional seed
			if tc.seedKey != 0 {
				if ok, err := b.Add(nil, tc.seedKey, tc.seedVal); err != nil || !ok {
					t.Fatalf("seed add: %v %v", ok, err)
				}
			}
			if ok, err := b.Upsert(nil, tc.upsertKey, tc.upsertVal); err != nil || !ok {
				t.Fatalf("upsert err=%v ok=%v", err, ok)
			}
			if ok, _ := b.Find(nil, tc.upsertKey, false); !ok {
				t.Fatalf("find upserted key")
			}
			got, _ := b.GetCurrentItem(nil)
			if *got.Value != tc.wantVal {
				t.Fatalf("got %q want %q", *got.Value, tc.wantVal)
			}
		})
	}
}
