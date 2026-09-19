package etl

import (
	"context"
	"path/filepath"
	"testing"
)

// TestPrepareData_RejectsInternalURL is the second real consumer proof for
// internal/netguard: before this, PrepareData had no SSRF validation at
// all on its caller-supplied URL. It's driven today only by a local
// workflow JSON file through the ai/cmd/etl CLI (not a remote HTTP
// handler), but a workflow file can come from anywhere an operator copies
// one from, so the same guard used by tools/httpserver applies here too.
func TestPrepareData_RejectsInternalURL(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.json")
	err := PrepareData(context.Background(), "http://169.254.169.254/latest/meta-data/", out, 0)
	if err == nil {
		t.Fatal("PrepareData allowed a request to the cloud metadata endpoint")
	}
}
