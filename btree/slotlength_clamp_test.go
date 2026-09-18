package btree

import (
	"testing"

	sop "github.com/sharedcode/joltrin"
)

// TestGetSlotLength_ClampsPathologicalValues guards against an
// uncontrolled allocation: StoreInfo loaded straight from persisted
// storage bypasses sop.NewStoreInfo's own clamp, so a corrupted or
// maliciously large slot_length value must still be bounded here, at the
// point a node's Slots slice is actually allocated.
func TestGetSlotLength_ClampsPathologicalValues(t *testing.T) {
	cases := []struct {
		name  string
		input int
		want  int
	}{
		{"huge value", 2_000_000_000, sop.MaxSlotLength},
		{"negative value", -1, 2},
		{"zero", 0, 2},
		{"within range", 100, 100},
		{"exactly max", sop.MaxSlotLength, sop.MaxSlotLength},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bt := &Btree[int, int]{StoreInfo: &sop.StoreInfo{SlotLength: c.input}}
			if got := bt.getSlotLength(); got != c.want {
				t.Errorf("getSlotLength() with input %d = %d, want %d", c.input, got, c.want)
			}
		})
	}
}
