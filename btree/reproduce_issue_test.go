package btree

import (
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Simulated Btree New function logic
func NewSimulated[TK any](si *sop.StoreInfo) {
	// Detect Key IsPrimitiveKey type accurately.
	var zero TK
	if any(zero) != nil {
		si.IsPrimitiveKey = IsPrimitive[TK]()
	}
}

func TestPrimitiveOverwrite(t *testing.T) {
	// Scenario: Python binding wants a primitive key (e.g. string), so it sets IsPrimitiveKey = true
	// But it instantiates the Go Btree with [any] because it's a generic wrapper.
	si := &sop.StoreInfo{
		Name:           "python_string_tree",
		IsPrimitiveKey: true,
	}

	fmt.Printf("Before New: IsPrimitiveKey=%v\n", si.IsPrimitiveKey)

	// Call with [any]
	NewSimulated[any](si)

	fmt.Printf("After New[any]: IsPrimitiveKey=%v\n", si.IsPrimitiveKey)

	if !si.IsPrimitiveKey {
		fmt.Println("FAILURE: The flag was overwritten to false!")
		t.Fail()
	} else {
		fmt.Println("SUCCESS: The flag was preserved.")
	}
}

func main() {}
