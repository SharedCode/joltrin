// Command verify_barrier demonstrates the barrier using an in-memory trace.
package main

import (
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/v5/internal/barrierdemo"
)

func main() {
	if err := barrierdemo.Run(os.Stdout, barrierdemo.Pause(os.Getenv("JOLTRIN_DEMO_PAUSE"))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
