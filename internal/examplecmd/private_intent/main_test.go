package privateintent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func call(t *testing.T, a *app, db string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"database": db}
	res, err := a.deleteDatabase(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(mcp.TextContent).Text
}

func TestToolIsGatedEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := initDirQuiet(dir); err != nil {
		t.Fatal(err)
	}
	a, err := loadEngine(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "deleted.log")
	deleted := func() bool { _, err := os.Stat(logPath); return err == nil }

	if out := call(t, a, "orders"); !strings.Contains(out, "verified backup and owner approval required") || deleted() {
		t.Fatalf("first call must block without deleting: %s", out)
	}
	if _, err := attest(dir, "prod/orders"); err != nil {
		t.Fatal(err)
	}
	if out := call(t, a, "orders"); !strings.Contains(out, "REQUIRE_APPROVAL") || deleted() {
		t.Fatalf("second call must ask for approval: %s", out)
	}
	if _, err := approve(dir, "billing"); err != nil { // approval for a different database
		t.Fatal(err)
	}
	if out := call(t, a, "orders"); !strings.Contains(out, "REQUIRE_APPROVAL") || deleted() {
		t.Fatalf("approval for billing must not cover orders: %s", out)
	}
	id, err := approve(dir, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revoke(dir, id); err != nil {
		t.Fatal(err)
	}
	if out := call(t, a, "orders"); !strings.Contains(out, "REQUIRE_APPROVAL") || deleted() {
		t.Fatalf("revoked approval must not count: %s", out)
	}
	if _, err := approve(dir, "orders"); err != nil {
		t.Fatal(err)
	}
	if out := call(t, a, "orders"); !strings.Contains(out, `"executed"`) || !deleted() {
		t.Fatalf("fully authorized call must run: %s", out)
	}
	if out := call(t, a, "orders"); !strings.Contains(out, "REQUIRE_APPROVAL") {
		t.Fatalf("approval must be single use: %s", out)
	}
}

func TestAgentCannotForgeInboxFiles(t *testing.T) {
	dir := t.TempDir()
	if err := initDirQuiet(dir); err != nil {
		t.Fatal(err)
	}
	a, err := loadEngine(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// An agent with file access writes unsigned artifacts straight into the inbox.
	forged := `{"type":"evidence","evidence":{"id":"ev_x","tenant":"demo","state":"backup_validated","resource":"prod/*","issuer":"abc","issued_at":1,"expires_at":99999999999,"sig":"AAAA"}}`
	forgedApproval := `{"type":"approval","approval":{"id":"ap_x","tenant":"demo","role":"production_owner","approver":"abc","expires_at":99999999999,"sig":"AAAA"}}`
	os.WriteFile(filepath.Join(dir, "inbox", "a.json"), []byte(forged), 0o600)
	os.WriteFile(filepath.Join(dir, "inbox", "b.json"), []byte(forgedApproval), 0o600)
	if out := call(t, a, "orders"); !strings.Contains(out, "BLOCK") {
		t.Fatalf("forged inbox files unblocked the tool: %s", out)
	}
}
