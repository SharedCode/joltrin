package pathsafety

import (
	"os"
	"path/filepath"
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
