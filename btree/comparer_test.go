package btree

import (
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// cmpWrapper implements Comparer for testing the Comparer path in Compare/CoerceComparer.
type cmpWrapper int

func (c cmpWrapper) Compare(other interface{}) int {
	// compare as ints
	oi, _ := other.(int)
	if int(c) < oi {
		return -1
	}
	if int(c) > oi {
		return 1
	}
	return 0
}

func TestComparer_Compare_And_Coerce(t *testing.T) {
	// Compare: ints
	if got := Compare(1, 2); got >= 0 {
		t.Fatalf("Compare int failed: %d", got)
	}
	if got := Compare(2, 1); got <= 0 {
		t.Fatalf("Compare int failed: %d", got)
	}
	if got := Compare(2, 2); got != 0 {
		t.Fatalf("Compare int failed: %d", got)
	}

	// strings
	if got := Compare("a", "b"); got >= 0 {
		t.Fatalf("Compare string failed: %d", got)
	}

	// time.Time
	t1 := time.Now()
	t2 := t1.Add(time.Second)
	if got := Compare(t1, t2); got >= 0 {
		t.Fatalf("Compare time failed: %d", got)
	}

	// nils
	if got := Compare(nil, nil); got != 0 {
		t.Fatalf("Compare nil==nil failed: %d", got)
	}
	if got := Compare(nil, 1); got != -1 {
		t.Fatalf("Compare nil<1 failed: %d", got)
	}
	if got := Compare(1, nil); got != 1 {
		t.Fatalf("Compare 1>nil failed: %d", got)
	}

	// Comparer implementation
	if got := Compare(cmpWrapper(1), 2); got != -1 {
		t.Fatalf("Compare Comparer failed: %d", got)
	}

	// CoerceComparer: int
	ci := CoerceComparer(0)
	if got := ci(1, 2); got >= 0 {
		t.Fatalf("Coerce int failed: %d", got)
	}
	// CoerceComparer: string
	cs := CoerceComparer("")
	if got := cs("a", "b"); got >= 0 {
		t.Fatalf("Coerce string failed: %d", got)
	}
	// CoerceComparer: time.Time
	ct := CoerceComparer(time.Time{})
	if got := ct(t1, t2); got >= 0 {
		t.Fatalf("Coerce time failed: %d", got)
	}
}

// Cover additional primitive branches in Compare and CoerceComparer: int variants, uint variants,
// floats, uintptr, and default fallback string comparison.
func TestComparer_PrimitiveBranches(t *testing.T) {
	// ints
	if got := Compare(int8(1), int8(2)); got >= 0 {
		t.Fatalf("int8 cmp")
	}
	if got := Compare(int16(1), int16(2)); got >= 0 {
		t.Fatalf("int16 cmp")
	}
	if got := Compare(int32(1), int32(2)); got >= 0 {
		t.Fatalf("int32 cmp")
	}
	if got := Compare(int64(1), int64(2)); got >= 0 {
		t.Fatalf("int64 cmp")
	}

	// uints
	if got := Compare(uint(1), uint(2)); got >= 0 {
		t.Fatalf("uint cmp")
	}
	if got := Compare(uint8(1), uint8(2)); got >= 0 {
		t.Fatalf("uint8 cmp")
	}
	if got := Compare(uint16(1), uint16(2)); got >= 0 {
		t.Fatalf("uint16 cmp")
	}
	if got := Compare(uint32(1), uint32(2)); got >= 0 {
		t.Fatalf("uint32 cmp")
	}
	if got := Compare(uint64(1), uint64(2)); got >= 0 {
		t.Fatalf("uint64 cmp")
	}

	// floats
	if got := Compare(float32(1.0), float32(2.0)); got >= 0 {
		t.Fatalf("float32 cmp")
	}
	if got := Compare(float64(1.0), float64(2.0)); got >= 0 {
		t.Fatalf("float64 cmp")
	}

	// uintptr
	if got := Compare(uintptr(1), uintptr(2)); got >= 0 {
		t.Fatalf("uintptr cmp")
	}

	// CoerceComparer for the same primitive set
	if cf := CoerceComparer(int8(0)); cf(int8(1), int8(2)) >= 0 {
		t.Fatalf("coerce int8")
	}
	if cf := CoerceComparer(int16(0)); cf(int16(1), int16(2)) >= 0 {
		t.Fatalf("coerce int16")
	}
	if cf := CoerceComparer(int32(0)); cf(int32(1), int32(2)) >= 0 {
		t.Fatalf("coerce int32")
	}
	if cf := CoerceComparer(int64(0)); cf(int64(1), int64(2)) >= 0 {
		t.Fatalf("coerce int64")
	}
	if cf := CoerceComparer(uint(0)); cf(uint(1), uint(2)) >= 0 {
		t.Fatalf("coerce uint")
	}
	if cf := CoerceComparer(uint8(0)); cf(uint8(1), uint8(2)) >= 0 {
		t.Fatalf("coerce uint8")
	}
	if cf := CoerceComparer(uint16(0)); cf(uint16(1), uint16(2)) >= 0 {
		t.Fatalf("coerce uint16")
	}
	if cf := CoerceComparer(uint32(0)); cf(uint32(1), uint32(2)) >= 0 {
		t.Fatalf("coerce uint32")
	}
	if cf := CoerceComparer(uint64(0)); cf(uint64(1), uint64(2)) >= 0 {
		t.Fatalf("coerce uint64")
	}
	if cf := CoerceComparer(uintptr(0)); cf(uintptr(1), uintptr(2)) >= 0 {
		t.Fatalf("coerce uintptr")
	}
	if cf := CoerceComparer(float32(0)); cf(float32(1.0), float32(2.0)) >= 0 {
		t.Fatalf("coerce float32")
	}
	if cf := CoerceComparer(float64(0)); cf(float64(1.0), float64(2.0)) >= 0 {
		t.Fatalf("coerce float64")
	}

	// Default fallback to string formatting for non-Comparer types
	type s struct{ A int }
	if got := Compare(s{1}, s{2}); got >= 0 {
		t.Fatalf("default string fallback cmp")
	}
	df := CoerceComparer(s{})
	if got := df(s{1}, s{2}); got >= 0 {
		t.Fatalf("default string fallback coerce")
	}
}

func TestComparer_UUID_Branch(t *testing.T) {
	u1 := sop.NewUUID()
	u2 := sop.NewUUID()
	if got := Compare(u1, u2); !(got == -1 || got == 1) { // distinct most likely
		t.Fatalf("Compare UUID unexpected: %d", got)
	}
	cf := CoerceComparer(u1)
	if got := cf(u1, u2); !(got == -1 || got == 1) {
		t.Fatalf("CoerceComparer UUID unexpected: %d", got)
	}
}
