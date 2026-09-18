// Package pathsafety guards destructive filesystem operations against a
// catastrophic path value. Database storage paths are legitimately allowed
// to point anywhere on disk (an operator chooses where their data lives),
// so this is a narrow safety net against an empty, root, or well-known
// system directory reaching os.RemoveAll, not a containment boundary.
package pathsafety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dangerousTargetList holds paths that must never be handed to
// os.RemoveAll, even indirectly through a config value a caller supplied.
var dangerousTargetList = []string{
	"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc",
	"/root", "/sbin", "/sys", "/usr", "/var", "/home", "/Users",
	"/System", "/Library", "/Applications", "/opt",
	`C:\`, `C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`,
}

var dangerousTargets = func() map[string]bool {
	m := make(map[string]bool, len(dangerousTargetList))
	for _, p := range dangerousTargetList {
		m[p] = true
	}
	return m
}()

// RejectDangerous errors out on an empty path, the filesystem root, a
// well-known top-level system directory, or the current user's home
// directory. It does not otherwise restrict where the path may point.
func RejectDangerous(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("cannot resolve path %q: %w", path, err)
	}
	clean := filepath.Clean(abs)
	if clean == string(filepath.Separator) || dangerousTargets[clean] {
		return fmt.Errorf("refusing to remove protected directory %q", clean)
	}
	if home, err := os.UserHomeDir(); err == nil && clean == filepath.Clean(home) {
		return fmt.Errorf("refusing to remove the user's home directory")
	}
	return nil
}

// RemoveAll runs RejectDangerous before calling os.RemoveAll.
func RemoveAll(path string) error {
	if err := RejectDangerous(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
