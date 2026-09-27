package fs

import (
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/v5"
)

// ToFilePathFunc formats a base path and UUID into a filesystem path optimized for I/O locality.
type ToFilePathFunc func(basePath string, id sop.UUID) string

// ToFilePath holds the global path formatting function used by the blob stores.
// Applications may override this to control file placement and partitioning.
var ToFilePath ToFilePathFunc = DefaultToFilePath

// DefaultToFilePath formats a path by appending a 4-level folder hierarchy derived from the UUID.
// This reduces per-directory file counts and improves filesystem performance on large datasets.
func DefaultToFilePath(basePath string, id sop.UUID) string {
	if len(basePath) > 0 && basePath[len(basePath)-1] == os.PathSeparator {
		return fmt.Sprintf("%s%s", basePath, Apply4LevelHierarchy(id))
	}
	return fmt.Sprintf("%s%c%s", basePath, os.PathSeparator, Apply4LevelHierarchy(id))
}

// Apply4LevelHierarchy maps a UUID to a 4-level directory structure using its first four hex digits.
// Example: abcd-... -> a/b/c/d, enabling broad distribution across subfolders.
func Apply4LevelHierarchy(id sop.UUID) string {
	s := id.String()
	ps := os.PathSeparator
	// Use the first four hex characters directly (e.g., abcd -> a/b/c/d).
	return fmt.Sprintf("%c%c%c%c%c%c%c", s[0], ps, s[1], ps, s[2], ps, s[3])
}
