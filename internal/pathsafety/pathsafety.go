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

// normalizeSlashes gives a path a deterministic, OS-independent form for
// comparison against dangerousTargetList: filepath.ToSlash only replaces
// os.PathSeparator, which is '/' on non-Windows, so it silently does
// nothing to a backslash path when this code happens to be running on
// Unix. A plain string replace does the same job regardless of which OS
// the process is currently running on.
func normalizeSlashes(s string) string {
	return strings.ReplaceAll(s, `\`, "/")
}

var dangerousTargets = func() map[string]bool {
	m := make(map[string]bool, len(dangerousTargetList))
	for _, p := range dangerousTargetList {
		m[p] = true
		m[normalizeSlashes(p)] = true
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

	// Checked in its own OS-independent, slash-normalized form first.
	// filepath.Abs below resolves a path using the current OS's own
	// semantics, so a Unix-style value like "/etc" running on Windows
	// becomes something like "C:\etc" via drive-relative resolution -
	// which matches nothing in dangerousTargets, silently letting through
	// exactly the kind of well-known dangerous path this package exists
	// to block. A path value isn't guaranteed to match the OS it's
	// running on (cross-platform config, a copy-pasted path, a WSL-style
	// value reaching native Windows code), so this check doesn't depend
	// on the runtime OS's path resolution at all.
	if dangerousTargets[normalizeSlashes(trimmed)] {
		return fmt.Errorf("refusing to remove protected directory %q", trimmed)
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
