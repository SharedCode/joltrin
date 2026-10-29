package corecommandtest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type aliasCase struct {
	name     string
	args     []string
	exit     int
	contains []string
}

// checkAliases builds actual command packages without workspace replacements,
// then compares their behavior in separate disposable working directories.
func checkAliases(t *testing.T, name string, cases ...aliasCase) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	binaries := make([]string, 0, 2)
	for _, path := range []string{"./" + name, "./examples/" + name} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		binary := filepath.Join(t.TempDir(), "command.exe")
		cmd := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, path)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("build %s without workspace: %v\n%s", path, err, out)
		}
		binaries = append(binaries, binary)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first []byte
			for index, binary := range binaries {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				cmd := exec.CommandContext(ctx, binary, tc.args...)
				cmd.Args[0] = name // Give both executables the same name in flag usage.
				work := t.TempDir()
				cmd.Dir = work
				cmd.Env = append(os.Environ(), "GOWORK=off", "JOLTRIN_DEMO_PAUSE=0")
				out, err := cmd.CombinedOutput()
				cancel()
				code := 0
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						code = exit.ExitCode()
					} else {
						t.Fatalf("run %s: %v\n%s", name, err, out)
					}
				}
				if code != tc.exit {
					t.Fatalf("%s exited %d, want %d:\n%s", name, code, tc.exit, out)
				}
				for _, marker := range tc.contains {
					if !strings.Contains(string(out), marker) {
						t.Fatalf("%s missing %q:\n%s", name, marker, out)
					}
				}
				// Initialization output may include the absolute disposable working path.
				// Normalize only that known path, preserving all other output.
				if real, err := filepath.EvalSymlinks(work); err == nil {
					out = bytes.ReplaceAll(out, []byte(real), []byte("<working-directory>"))
				}
				out = bytes.ReplaceAll(out, []byte(work), []byte("<working-directory>"))
				if index == 0 {
					first = out
				} else if !bytes.Equal(first, out) {
					t.Fatalf("old and short %s commands differ:\nshort:\n%s\nold:\n%s", name, first, out)
				}
			}
		})
	}
}
