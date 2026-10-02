# Examples

## 1. In-Memory Quickstart (Zero Dependencies)

```bash
# Clone the repository and run the quickstart
git clone https://github.com/sharedcode/joltrin.git
cd joltrin
go run ./examples/quickstart
```

```go
package main

import (
	"fmt"

	"github.com/sharedcode/joltrin/v5/inmemory"
)

func main() {
	fmt.Println("Joltrin quickstart: in-memory ordered B-Tree")

	// Unique keys, string values.
	b3 := inmemory.NewBtree[int, string](true)

	// Add a few build records keyed by build number.
	builds := map[int]string{
		101: "commit 9f2c1a  build ok",
		102: "commit 4be77d  build ok",
		103: "commit c30e52  tests failed",
		104: "commit 8d91f0  build ok",
		105: "commit 77aa3e  released",
	}
	for k, v := range builds {
		if !b3.Add(k, v) {
			fmt.Printf("Add(%d) failed\n", k)
			return
		}
	}
	fmt.Printf("added %d items, count=%d\n", len(builds), b3.Count())

	// Point lookup.
	if b3.Find(103, true) {
		fmt.Printf("Find(103): %s\n", b3.GetCurrentValue())
	}

	// Update in place.
	b3.Update(103, "commit c30e52  tests fixed, build ok")
	b3.Find(103, true)
	fmt.Printf("after Update(103): %s\n", b3.GetCurrentValue())

	// Ordered scan, no sort call needed: the tree keeps keys sorted.
	fmt.Println("ordered scan:")
	for k, v := range b3.All() {
		fmt.Printf("  build %d -> %s\n", k, v)
	}

	// Range scan seeks straight to the start key, then walks in order.
	fmt.Println("range scan, builds 102-104:")
	for k, v := range b3.Range(102, 104) {
		fmt.Printf("  build %d -> %s\n", k, v)
	}

	// Descending scan: newest builds first.
	fmt.Println("descending scan, newest 3 builds:")
	n := 0
	for k, v := range b3.AllDesc() {
		fmt.Printf("  build %d -> %s\n", k, v)
		if n++; n == 3 {
			break
		}
	}

	fmt.Println("quickstart: OK")
}
```

This is the actual content of `examples/quickstart/main.go`, kept in sync here rather than paraphrased, so what you run and what you read match.

## 2. AI Agent Memory & Swarm Task Hand-off

Run the new dedicated Agent Memory demo:

```bash
go run ./examples/agent_memory
```

This demo demonstrates an AI worker creating a context checkpoint, crashing mid-step, rolling back cleanly, and having a healthy peer worker resume the task in `<15ms`.
