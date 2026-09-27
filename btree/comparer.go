package btree

import (
	"bytes"
	"cmp"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
)

// Comparer specifies how to compare this value against another value.
type Comparer interface {
	// Compare compares this object with the other and returns -1, 0, or 1.
	// -1 means this < other, 0 means equal, 1 means this > other.
	Compare(other any) int
}

// ComparerFunc allows providing a comparer function separate from the key object.
type ComparerFunc[TK Ordered] func(a TK, b TK) int

// Ordered constrains key types that can be stored in a Btree.
// It permits built-in ordered types, UUIDs, Comparer implementations, and any as a fallback.
type Ordered interface {
	cmp.Ordered | *Comparer | any
}

// Compare compares two values, handling common built-in types, UUIDs, time.Time,
// Comparer implementations, and finally falling back to string comparison.
func Compare(anyX, anyY any) int {
	switch anyX.(type) {
	case int:
		x1, _ := anyX.(int)
		y1, _ := anyY.(int)
		return cmp.Compare(x1, y1)
	case int8:
		x1, _ := anyX.(int8)
		y1, _ := anyY.(int8)
		return cmp.Compare(x1, y1)
	case int16:
		x1, _ := anyX.(int16)
		y1, _ := anyY.(int16)
		return cmp.Compare(x1, y1)
	case int32:
		x1, _ := anyX.(int32)
		y1, _ := anyY.(int32)
		return cmp.Compare(x1, y1)
	case int64:
		x1, _ := anyX.(int64)
		y1, _ := anyY.(int64)
		return cmp.Compare(x1, y1)
	case uint:
		x1, _ := anyX.(uint)
		y1, _ := anyY.(uint)
		return cmp.Compare(x1, y1)
	case uint8:
		x1, _ := anyX.(uint8)
		y1, _ := anyY.(uint8)
		return cmp.Compare(x1, y1)
	case uint16:
		x1, _ := anyX.(uint16)
		y1, _ := anyY.(uint16)
		return cmp.Compare(x1, y1)
	case uint32:
		x1, _ := anyX.(uint32)
		y1, _ := anyY.(uint32)
		return cmp.Compare(x1, y1)
	case uint64:
		x1, _ := anyX.(uint64)
		y1, _ := anyY.(uint64)
		return cmp.Compare(x1, y1)
	case uintptr:
		x1, _ := anyX.(uintptr)
		y1, _ := anyY.(uintptr)
		return cmp.Compare(x1, y1)
	case float32:
		x1, _ := anyX.(float32)
		y1, _ := anyY.(float32)
		return cmp.Compare(x1, y1)
	case float64:
		x1, _ := anyX.(float64)
		y1, _ := anyY.(float64)
		return cmp.Compare(x1, y1)
	case string:
		x1, _ := anyX.(string)
		y1, _ := anyY.(string)
		return cmp.Compare(x1, y1)
	case uuid.UUID:
		x1, _ := anyX.(uuid.UUID)
		y1, _ := anyY.(uuid.UUID)
		return bytes.Compare(x1[:], y1[:])
	case sop.UUID:
		x1, _ := anyX.(sop.UUID)
		y1, _ := anyY.(sop.UUID)
		return x1.Compare(y1)
	case time.Time:
		x1, _ := anyX.(time.Time)
		y1, _ := anyY.(time.Time)
		return x1.Compare(y1)
	case []any:
		x1, _ := anyX.([]any)
		y1, _ := anyY.([]any)
		lenX := len(x1)
		lenY := len(y1)
		minLen := lenX
		if lenY < minLen {
			minLen = lenY
		}
		for i := 0; i < minLen; i++ {
			if c := Compare(x1[i], y1[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(lenX, lenY)
	case []byte:
		x1, _ := anyX.([]byte)
		y1, _ := anyY.([]byte)
		return bytes.Compare(x1, y1)
	case []string:
		x1, _ := anyX.([]string)
		y1, _ := anyY.([]string)
		lenX := len(x1)
		lenY := len(y1)
		minLen := lenX
		if lenY < minLen {
			minLen = lenY
		}
		for i := 0; i < minLen; i++ {
			if c := cmp.Compare(x1[i], y1[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(lenX, lenY)
	case []int:
		x1, _ := anyX.([]int)
		y1, _ := anyY.([]int)
		lenX := len(x1)
		lenY := len(y1)
		minLen := lenX
		if lenY < minLen {
			minLen = lenY
		}
		for i := 0; i < minLen; i++ {
			if c := cmp.Compare(x1[i], y1[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(lenX, lenY)
	case []float64:
		x1, _ := anyX.([]float64)
		y1, _ := anyY.([]float64)
		lenX := len(x1)
		lenY := len(y1)
		minLen := lenX
		if lenY < minLen {
			minLen = lenY
		}
		for i := 0; i < minLen; i++ {
			if c := cmp.Compare(x1[i], y1[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(lenX, lenY)
	case []float32:
		x1, _ := anyX.([]float32)
		y1, _ := anyY.([]float32)
		lenX := len(x1)
		lenY := len(y1)
		minLen := lenX
		if lenY < minLen {
			minLen = lenY
		}
		for i := 0; i < minLen; i++ {
			if c := cmp.Compare(x1[i], y1[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(lenX, lenY)
	default:
		if anyX == nil && anyY == nil {
			return 0
		}
		if anyX == nil {
			return -1
		}
		if anyY == nil {
			return 1
		}
		cX, ok := anyX.(Comparer)
		if ok {
			return cX.Compare(anyY)
		}
		// Last resort, compare their string values.
		s1 := fmt.Sprintf("%v", anyX)
		s2 := fmt.Sprintf("%v", anyY)
		return cmp.Compare(s1, s2)
	}
}

// CoerceComparer returns a type-appropriate comparer function for values similar to anyX.
// It specializes for common built-in types, UUIDs, time.Time, and Comparer implementations.
func CoerceComparer(anyX any) func(x, y any) int {
	switch anyX.(type) {
	case int:
		return func(x, y any) int {
			x1, _ := x.(int)
			y1, _ := y.(int)
			return cmp.Compare(x1, y1)
		}
	case int8:
		return func(x, y any) int {
			x1, _ := x.(int8)
			y1, _ := y.(int8)
			return cmp.Compare(x1, y1)
		}
	case int16:
		return func(x, y any) int {
			x1, _ := x.(int16)
			y1, _ := y.(int16)
			return cmp.Compare(x1, y1)
		}
	case int32:
		return func(x, y any) int {
			x1, _ := x.(int32)
			y1, _ := y.(int32)
			return cmp.Compare(x1, y1)
		}
	case int64:
		return func(x, y any) int {
			x1, _ := x.(int64)
			y1, _ := y.(int64)
			return cmp.Compare(x1, y1)
		}
	case uint:
		return func(x, y any) int {
			x1, _ := x.(uint)
			y1, _ := y.(uint)
			return cmp.Compare(x1, y1)
		}
	case uint8:
		return func(x, y any) int {
			x1, _ := x.(uint8)
			y1, _ := y.(uint8)
			return cmp.Compare(x1, y1)
		}
	case uint16:
		return func(x, y any) int {
			x1, _ := x.(uint16)
			y1, _ := y.(uint16)
			return cmp.Compare(x1, y1)
		}
	case uint32:
		return func(x, y any) int {
			x1, _ := x.(uint32)
			y1, _ := y.(uint32)
			return cmp.Compare(x1, y1)
		}
	case uint64:
		return func(x, y any) int {
			x1, _ := x.(uint64)
			y1, _ := y.(uint64)
			return cmp.Compare(x1, y1)
		}
	case uintptr:
		return func(x, y any) int {
			x1, _ := x.(uintptr)
			y1, _ := y.(uintptr)
			return cmp.Compare(x1, y1)
		}
	case float32:
		return func(x, y any) int {
			x1, _ := x.(float32)
			y1, _ := y.(float32)
			return cmp.Compare(x1, y1)
		}
	case float64:
		return func(x, y any) int {
			x1, _ := x.(float64)
			y1, _ := y.(float64)
			return cmp.Compare(x1, y1)
		}
	case string:
		return func(x, y any) int {
			x1, _ := x.(string)
			y1, _ := y.(string)
			return cmp.Compare(x1, y1)
		}
	case uuid.UUID:
		return func(x, y any) int {
			x1, _ := x.(uuid.UUID)
			y1, _ := y.(uuid.UUID)
			return bytes.Compare(x1[:], y1[:])
		}
	case sop.UUID:
		return func(x, y any) int {
			x1, _ := x.(sop.UUID)
			y1, _ := y.(sop.UUID)
			return x1.Compare(y1)
		}
	case time.Time:
		return func(x, y any) int {
			x1, _ := x.(time.Time)
			y1, _ := y.(time.Time)
			return x1.Compare(y1)
		}
	case []any:
		return func(x, y any) int {
			x1, _ := x.([]any)
			y1, _ := y.([]any)
			lenX := len(x1)
			lenY := len(y1)
			minLen := lenX
			if lenY < minLen {
				minLen = lenY
			}
			for i := 0; i < minLen; i++ {
				if c := Compare(x1[i], y1[i]); c != 0 {
					return c
				}
			}
			return cmp.Compare(lenX, lenY)
		}
	case []byte:
		return func(x, y any) int {
			x1, _ := x.([]byte)
			y1, _ := y.([]byte)
			return bytes.Compare(x1, y1)
		}
	case []string:
		return func(x, y any) int {
			x1, _ := x.([]string)
			y1, _ := y.([]string)
			lenX := len(x1)
			lenY := len(y1)
			minLen := lenX
			if lenY < minLen {
				minLen = lenY
			}
			for i := 0; i < minLen; i++ {
				if c := cmp.Compare(x1[i], y1[i]); c != 0 {
					return c
				}
			}
			return cmp.Compare(lenX, lenY)
		}
	case []int:
		return func(x, y any) int {
			x1, _ := x.([]int)
			y1, _ := y.([]int)
			lenX := len(x1)
			lenY := len(y1)
			minLen := lenX
			if lenY < minLen {
				minLen = lenY
			}
			for i := 0; i < minLen; i++ {
				if c := cmp.Compare(x1[i], y1[i]); c != 0 {
					return c
				}
			}
			return cmp.Compare(lenX, lenY)
		}
	case []float64:
		return func(x, y any) int {
			x1, _ := x.([]float64)
			y1, _ := y.([]float64)
			lenX := len(x1)
			lenY := len(y1)
			minLen := lenX
			if lenY < minLen {
				minLen = lenY
			}
			for i := 0; i < minLen; i++ {
				if c := cmp.Compare(x1[i], y1[i]); c != 0 {
					return c
				}
			}
			return cmp.Compare(lenX, lenY)
		}
	case []float32:
		return func(x, y any) int {
			x1, _ := x.([]float32)
			y1, _ := y.([]float32)
			lenX := len(x1)
			lenY := len(y1)
			minLen := lenX
			if lenY < minLen {
				minLen = lenY
			}
			for i := 0; i < minLen; i++ {
				if c := cmp.Compare(x1[i], y1[i]); c != 0 {
					return c
				}
			}
			return cmp.Compare(lenX, lenY)
		}
	default:
		return func(x, y any) int {
			if x == nil && y == nil {
				return 0
			}
			if x == nil {
				return -1
			}
			if y == nil {
				return 1
			}
			cX, ok := x.(Comparer)
			if ok {
				return cX.Compare(y)
			}
			// Last resort, compare their string values.
			s1 := fmt.Sprintf("%v", x)
			s2 := fmt.Sprintf("%v", y)
			return cmp.Compare(s1, s2)
		}
	}
}

// IsPrimitive returns true if data type is primitive type.
func IsPrimitive[TK any]() bool {
	var zero TK
	if any(zero) == nil {
		return false
	}
	switch any(zero).(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr, float32,
		float64, string, uuid.UUID, sop.UUID, time.Time, []byte, []string, []int, []float64, []float32:
		return true
	default:
		return false
	}
}
