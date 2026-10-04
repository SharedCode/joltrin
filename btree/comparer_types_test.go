package btree

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
)

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// orderCase is two values of one type with lo ordered before hi.
type orderCase struct {
	name   string
	lo, hi any
}

func uuidOf(s string) uuid.UUID { return uuid.MustParse(s) }

func orderCases() []orderCase {
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	u1 := uuidOf("00000000-0000-0000-0000-000000000001")
	u2 := uuidOf("00000000-0000-0000-0000-000000000002")
	return []orderCase{
		{"int", 1, 2},
		{"int8", int8(-3), int8(4)},
		{"int16", int16(5), int16(300)},
		{"int32", int32(-7), int32(7)},
		{"int64", int64(1) << 40, int64(1)<<40 + 1},
		{"uint", uint(1), uint(2)},
		{"uint8", uint8(1), uint8(200)},
		{"uint16", uint16(1), uint16(60000)},
		{"uint32", uint32(1), uint32(4000000000)},
		{"uint64", uint64(1), uint64(1) << 63},
		{"uintptr", uintptr(10), uintptr(20)},
		{"float32", float32(-1.5), float32(2.5)},
		{"float64", -0.25, 0.25},
		{"string", "apple", "banana"},
		{"uuid.UUID", u1, u2},
		{"sop.UUID", sop.UUID(u1), sop.UUID(u2)},
		{"time.Time", t0, t0.Add(time.Second)},
		{"[]any element", []any{1, "a"}, []any{1, "b"}},
		{"[]any shorter prefix", []any{1}, []any{1, 2}},
		{"[]byte element", []byte{1, 2}, []byte{1, 3}},
		{"[]byte shorter prefix", []byte{1}, []byte{1, 0}},
		{"[]string element", []string{"a", "b"}, []string{"a", "c"}},
		{"[]string shorter prefix", []string{"a"}, []string{"a", "a"}},
		{"[]int element", []int{1, 2}, []int{1, 3}},
		{"[]int shorter prefix", []int{1}, []int{1, 0}},
		{"[]float64 element", []float64{1, 2}, []float64{1, 2.5}},
		{"[]float64 shorter prefix", []float64{1}, []float64{1, 0}},
		{"[]float32 element", []float32{1, 2}, []float32{1, 2.5}},
		{"[]float32 shorter prefix", []float32{1}, []float32{1, 0}},
		{"Comparer", cmpWrapper(1), 5},
	}
}

func TestCompare_OrdersEveryKeyType(t *testing.T) {
	for _, c := range orderCases() {
		t.Run(c.name, func(t *testing.T) {
			if got := sign(Compare(c.lo, c.hi)); got != -1 {
				t.Errorf("Compare(lo, hi) = %d, want -1", got)
			}
			if c.name == "Comparer" {
				return // the Comparer here only orders against an int, so the reverse call is not meaningful
			}
			if got := sign(Compare(c.hi, c.lo)); got != 1 {
				t.Errorf("Compare(hi, lo) = %d, want 1", got)
			}
			if got := Compare(c.lo, c.lo); got != 0 {
				t.Errorf("Compare(lo, lo) = %d, want 0", got)
			}
		})
	}
}

// CoerceComparer must agree with Compare for every type it specializes.
func TestCoerceComparer_AgreesWithCompare(t *testing.T) {
	for _, c := range orderCases() {
		t.Run(c.name, func(t *testing.T) {
			f := CoerceComparer(c.lo)
			if f == nil {
				t.Fatal("CoerceComparer returned nil")
			}
			if got := sign(f(c.lo, c.hi)); got != -1 {
				t.Errorf("comparer(lo, hi) = %d, want -1", got)
			}
			if c.name == "Comparer" {
				return
			}
			if got := sign(f(c.hi, c.lo)); got != 1 {
				t.Errorf("comparer(hi, lo) = %d, want 1", got)
			}
			if got := f(c.lo, c.lo); got != 0 {
				t.Errorf("comparer(lo, lo) = %d, want 0", got)
			}
		})
	}
}

func TestCompare_EmptySlicesAreEqualAndOrderBeforeNonEmpty(t *testing.T) {
	cases := []struct {
		name         string
		empty, other any
	}{
		{"[]any", []any{}, []any{1}},
		{"[]byte", []byte{}, []byte{0}},
		{"[]string", []string{}, []string{""}},
		{"[]int", []int{}, []int{0}},
		{"[]float64", []float64{}, []float64{0}},
		{"[]float32", []float32{}, []float32{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(c.empty, c.empty); got != 0 {
				t.Errorf("empty vs empty = %d, want 0", got)
			}
			if got := sign(Compare(c.empty, c.other)); got != -1 {
				t.Errorf("empty vs non-empty = %d, want -1", got)
			}
			f := CoerceComparer(c.empty)
			if got := sign(f(c.other, c.empty)); got != 1 {
				t.Errorf("coerced non-empty vs empty = %d, want 1", got)
			}
		})
	}
}

type plainStruct struct{ N int }

func TestCompare_FallbackForNilAndUnknownTypes(t *testing.T) {
	if got := Compare(nil, nil); got != 0 {
		t.Errorf("nil vs nil = %d, want 0", got)
	}
	if got := Compare(nil, 1); got != -1 {
		t.Errorf("nil vs value = %d, want -1", got)
	}
	if got := Compare(struct{}{}, nil); got != 1 {
		t.Errorf("value vs nil = %d, want 1", got)
	}
	// No Comparer and no special case: compared by their printed form.
	if got := sign(Compare(plainStruct{1}, plainStruct{2})); got != -1 {
		t.Errorf("struct fallback = %d, want -1", got)
	}
	if got := Compare(plainStruct{3}, plainStruct{3}); got != 0 {
		t.Errorf("equal structs = %d, want 0", got)
	}

	f := CoerceComparer(plainStruct{})
	if got := f(nil, nil); got != 0 {
		t.Errorf("coerced nil vs nil = %d, want 0", got)
	}
	if got := f(nil, plainStruct{1}); got != -1 {
		t.Errorf("coerced nil vs value = %d, want -1", got)
	}
	if got := f(plainStruct{1}, nil); got != 1 {
		t.Errorf("coerced value vs nil = %d, want 1", got)
	}
	if got := sign(f(plainStruct{1}, plainStruct{2})); got != -1 {
		t.Errorf("coerced struct fallback = %d, want -1", got)
	}
}

func TestCompare_ComparerImplementationIsUsedForUnknownTypes(t *testing.T) {
	if got := Compare(cmpWrapper(7), 7); got != 0 {
		t.Errorf("equal via Comparer = %d, want 0", got)
	}
	if got := Compare(cmpWrapper(9), 7); got != 1 {
		t.Errorf("greater via Comparer = %d, want 1", got)
	}
	f := CoerceComparer(cmpWrapper(0))
	if got := f(cmpWrapper(3), 5); got != -1 {
		t.Errorf("coerced Comparer = %d, want -1", got)
	}
}

func TestIsPrimitive(t *testing.T) {
	yes := map[string]bool{
		"int":       IsPrimitive[int](),
		"int8":      IsPrimitive[int8](),
		"int16":     IsPrimitive[int16](),
		"int32":     IsPrimitive[int32](),
		"int64":     IsPrimitive[int64](),
		"uint":      IsPrimitive[uint](),
		"uint8":     IsPrimitive[uint8](),
		"uint16":    IsPrimitive[uint16](),
		"uint32":    IsPrimitive[uint32](),
		"uint64":    IsPrimitive[uint64](),
		"uintptr":   IsPrimitive[uintptr](),
		"float32":   IsPrimitive[float32](),
		"float64":   IsPrimitive[float64](),
		"string":    IsPrimitive[string](),
		"uuid.UUID": IsPrimitive[uuid.UUID](),
		"sop.UUID":  IsPrimitive[sop.UUID](),
		"time.Time": IsPrimitive[time.Time](),
		"[]byte":    IsPrimitive[[]byte](),
		"[]string":  IsPrimitive[[]string](),
		"[]int":     IsPrimitive[[]int](),
		"[]float64": IsPrimitive[[]float64](),
		"[]float32": IsPrimitive[[]float32](),
	}
	for name, got := range yes {
		if !got {
			t.Errorf("IsPrimitive[%s] = false, want true", name)
		}
	}
	if IsPrimitive[plainStruct]() {
		t.Error("a struct is not primitive")
	}
	if IsPrimitive[any]() {
		t.Error("any is not primitive")
	}
	if IsPrimitive[[]any]() {
		t.Error("[]any is not primitive")
	}
	if IsPrimitive[*int]() {
		t.Error("a pointer is not primitive")
	}
}
