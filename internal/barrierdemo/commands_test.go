package barrierdemo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Build each actual public command, then run it outside the source checkout.
// Both paths must stay usable and must execute the same real verifier transcript.
func TestCommandPathsWorkOutsideCheckout(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"./verify_barrier", "./examples/verify_barrier"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			work := t.TempDir()
			binary := filepath.Join(work, "verify_barrier.exe")
			build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, path)
			build.Dir = root
			build.Env = append(os.Environ(), "GOWORK=off")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", path, err, output)
			}
			command := exec.CommandContext(ctx, binary)
			command.Dir = work
			command.Env = append(os.Environ(), "JOLTRIN_DEMO_PAUSE=0")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("run %s: %v\n%s", path, err, output)
			}
			if string(output) != expectedOutput {
				t.Fatalf("%s transcript differs:\n%s", path, output)
			}
		})
	}
}
