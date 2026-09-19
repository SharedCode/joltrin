package pathsafety

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRejectDangerous(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	dangerous := []string{"", "  ", "/", "/etc", "/usr", "/Users", home}
	for _, p := range dangerous {
		if err := RejectDangerous(p); err == nil {
			t.Errorf("RejectDangerous(%q) = nil, want error", p)
		}
	}

	safe := []string{
		filepath.Join(t.TempDir(), "mydb"),
		"relative/path/to/mydb",
	}
	for _, p := range safe {
		if err := RejectDangerous(p); err != nil {
			t.Errorf("RejectDangerous(%q) = %v, want nil", p, err)
		}
	}
}

// TestRejectDangerous_CrossPlatform locks down the actual bug found when
// this package's tests first ran on a Windows CI runner (they never had
// before): filepath.Abs resolves a path using the CURRENT OS's semantics,
// so a Unix-style value like "/etc" running on Windows becomes something
// like "C:\etc" via drive-relative resolution, matching nothing in
// dangerousTargets and silently letting through exactly the well-known
// dangerous path this package exists to block. The reverse is equally true
// running Windows-style values through Unix's path resolution. Since this
// only has a non-Windows runner available, it proves the fix the same way
// in reverse: a Windows-style dangerous path must still be rejected when
// running on this (non-Windows) machine, which only holds if the rejection
// doesn't depend on the runtime OS's own path-resolution semantics.
func TestRejectDangerous_CrossPlatform(t *testing.T) {
	dangerous := []string{
		`C:\`,
		`C:\Windows`,
		`C:\Program Files`,
		`C:\Program Files (x86)`,
	}
	for _, p := range dangerous {
		if err := RejectDangerous(p); err == nil {
			t.Errorf("RejectDangerous(%q) = nil, want error (Windows-style dangerous path, tested on %s)", p, runtime.GOOS)
		}
	}
}

func TestRemoveAll_RefusesDangerousPath(t *testing.T) {
	if err := RemoveAll("/etc"); err == nil {
		t.Fatal("RemoveAll(\"/etc\") = nil, want error")
	}
}

func TestRemoveAll_RemovesSafePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mydb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll(%q) error = %v", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists after RemoveAll")
	}
}
