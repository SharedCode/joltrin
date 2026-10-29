// Command private_intent is a runnable example of Joltrin Private Intent
// guarding a destructive tool behind an MCP server.
//
//	go run ./examples/private_intent demo
//	go run ./examples/private_intent init ./pi
//	go run ./examples/private_intent mcp ./pi          (stdio MCP server)
//	go run ./examples/private_intent attest ./pi prod/orders
//	go run ./examples/private_intent approve ./pi orders
//	go run ./examples/private_intent revoke ./pi <approval-id>
//
// The agent gets one tool, delete_database. Behind it the server asks the
// intent engine for a decision and then runs the (simulated) delete through a
// Guard, so the delete cannot happen without a valid receipt. Evidence and
// approvals are signed files dropped in <dir>/inbox by the backup system and
// the owner; the agent has no tool that writes them and cannot sign them.
//
// Nothing here touches a real database. The delete appends a line to
// <dir>/deleted.log.
package privateintent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/governance"
	"github.com/sharedcode/joltrin/v5/intent"
)

const (
	tenant = "demo"
	agent  = "ops-agent"
)

func Main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"demo"}
	}
	var err error
	switch args[0] {
	case "demo":
		err = runDemo()
	case "init", "mcp", "attest", "approve", "revoke":
		if len(args) < 2 {
			err = errors.New("usage: private_intent " + args[0] + " <dir> ...")
			break
		}
		err = dispatch(args[0], args[1], args[2:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(cmd, dir string, rest []string) error {
	switch cmd {
	case "init":
		return initDir(dir)
	case "mcp":
		return serveMCP(dir)
	case "attest":
		if len(rest) < 1 {
			return errors.New("usage: attest <dir> <resource>")
		}
		return printID(attest(dir, rest[0]))
	case "approve":
		if len(rest) < 1 {
			return errors.New("usage: approve <dir> <database>")
		}
		return printID(approve(dir, rest[0]))
	default:
		if len(rest) < 1 {
			return errors.New("usage: revoke <dir> <artifact-id>")
		}
		return printID(revoke(dir, rest[0]))
	}
}

func printID(id string, err error) error {
	if err == nil {
		fmt.Println(id)
	}
	return err
}

func deleteAction(db string) intent.Action {
	return intent.Action{Tenant: tenant, Agent: agent, Tool: "db.delete", Resource: "prod/" + db,
		Env: "production", Params: map[string]string{"database": db}}
}

func prodRule() intent.Rule {
	return intent.Rule{
		ID: "prod-data-deletion", Label: "Production data deletion", Owner: "owner",
		Preference: "Never delete production data unless a validated backup exists and the production owner approves.",
		Purpose:    "protect production data",
		Mode:       intent.Grant, Agent: agent, Tool: "db.delete", Resource: "prod/*", Env: "production",
		Requires: []intent.Requirement{
			{Kind: intent.NeedEvidence, State: "backup_validated", Describe: "verified backup"},
			{Kind: intent.NeedApproval, Role: "production_owner", Describe: "owner approval"},
		},
	}
}

// ---- key and file handling --------------------------------------------------

func writeKey(path string, k ed25519.PrivateKey) error {
	return os.WriteFile(path, []byte(hex.EncodeToString(k.Seed())), 0o600)
}

func readKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(string(b))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is not a key file", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

type trustFile struct {
	Owner    string `json:"owner"`
	Backup   string `json:"backup_issuer"`
	Approver string `json:"approver"`
}

func initDir(dir string) error {
	for _, d := range []string{"keys", "server", "inbox"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return err
		}
	}
	var pubs [3]ed25519.PublicKey
	for i, name := range []string{"owner", "backup", "engine"} {
		pub, priv, err := intent.GenerateKey()
		if err != nil {
			return err
		}
		where := "keys"
		if name == "engine" {
			where = "server"
		}
		if err := writeKey(filepath.Join(dir, where, name+".key"), priv); err != nil {
			return err
		}
		pubs[i] = pub
	}
	tf := trustFile{Owner: hex.EncodeToString(pubs[0]), Backup: hex.EncodeToString(pubs[1]), Approver: hex.EncodeToString(pubs[0])}
	b, _ := json.MarshalIndent(tf, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "server", "trust.json"), b, 0o600); err != nil {
		return err
	}
	vkey := make([]byte, 32)
	if _, err := rand.Read(vkey); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "server", "vault.key"), []byte(hex.EncodeToString(vkey)), 0o600); err != nil {
		return err
	}
	v := intent.NewVault(tenant, nil)
	if err := v.Add(prodRule()); err != nil {
		return err
	}
	if err := v.SaveFile(filepath.Join(dir, "server", "vault.sealed"), vkey); err != nil {
		return err
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("Created %s\n\nAdd to your MCP client config:\n\n", abs)
	fmt.Printf(`{"mcpServers":{"joltrin-intent":{"command":"go","args":["run","./examples/private_intent","mcp","%s"]}}}`+"\n\n", abs)
	fmt.Println("The agent now has one tool, delete_database, and it will be blocked. Then run:")
	fmt.Printf("  private_intent attest  %s prod/orders   (the backup system vouches for a validated backup)\n", dir)
	fmt.Printf("  private_intent approve %s orders        (the owner approves this exact delete)\n", dir)
	fmt.Printf("  private_intent revoke  %s <id>          (take an approval back)\n", dir)
	return nil
}

func drop(dir string, env intent.Envelope, id string) (string, error) {
	b, _ := json.Marshal(env)
	return id, os.WriteFile(filepath.Join(dir, "inbox", id+".json"), b, 0o600)
}

func attest(dir, resource string) (string, error) {
	k, err := readKey(filepath.Join(dir, "keys", "backup.key"))
	if err != nil {
		return "", err
	}
	ev := intent.SignEvidence(k, intent.Evidence{Tenant: tenant, State: "backup_validated", Resource: resource}, time.Now(), time.Hour)
	return drop(dir, intent.Envelope{Type: "evidence", Evidence: &ev}, ev.ID)
}

func approve(dir, db string) (string, error) {
	k, err := readKey(filepath.Join(dir, "keys", "owner.key"))
	if err != nil {
		return "", err
	}
	ap := intent.SignApproval(k, deleteAction(db), "production_owner", time.Now(), 10*time.Minute)
	return drop(dir, intent.Envelope{Type: "approval", Approval: &ap}, ap.ID)
}

func revoke(dir, id string) (string, error) {
	k, err := readKey(filepath.Join(dir, "keys", "owner.key"))
	if err != nil {
		return "", err
	}
	rv := intent.SignRevocation(k, tenant, id, time.Now())
	return drop(dir, intent.Envelope{Type: "revocation", Revocation: &rv}, "rv_"+id)
}

// ---- the MCP server ---------------------------------------------------------

type app struct {
	dir   string
	eng   *intent.Engine
	guard *intent.Guard
	seen  map[string]bool
}

func loadEngine(dir string, audit *governance.MemoryAuditRecorder) (*app, error) {
	var tf trustFile
	b, err := os.ReadFile(filepath.Join(dir, "server", "trust.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &tf); err != nil {
		return nil, err
	}
	dec := func(s string) ed25519.PublicKey { b, _ := hex.DecodeString(s); return ed25519.PublicKey(b) }
	owner, backup, approver := dec(tf.Owner), dec(tf.Backup), dec(tf.Approver)
	engKey, err := readKey(filepath.Join(dir, "server", "engine.key"))
	if err != nil {
		return nil, err
	}
	hexKey, err := os.ReadFile(filepath.Join(dir, "server", "vault.key"))
	if err != nil {
		return nil, err
	}
	vkey, err := hex.DecodeString(string(hexKey))
	if err != nil {
		return nil, err
	}
	vault := intent.NewVault(tenant, nil)
	if err := vault.LoadFile(filepath.Join(dir, "server", "vault.sealed"), vkey); err != nil {
		return nil, err
	}
	cfg := intent.Config{
		Vault: vault, SigningKey: engKey,
		Trust: intent.Trust{
			Owner:     owner,
			Issuers:   map[string]intent.TrustedKey{intent.KeyID(backup): {Key: backup, Scope: []string{"backup_validated"}}},
			Approvers: map[string]intent.TrustedKey{intent.KeyID(approver): {Key: approver, Scope: []string{"production_owner"}}},
		},
	}
	if audit != nil {
		cfg.Audit = func(e intent.Entry) {
			_ = audit.Record(context.Background(), &governance.AuditEvent{
				TenantID: e.Tenant, ActorID: e.Agent, Action: e.Tool, Resource: e.Resource,
				Decision: auditDecision(e.Verdict), Reason: string(e.Verdict), Metadata: map[string]string{"action_hash": e.ActionHash, "receipt": e.ReceiptID},
			})
		}
	}
	eng, err := intent.NewEngine(cfg)
	if err != nil {
		return nil, err
	}
	return &app{dir: dir, eng: eng, guard: intent.NewGuard(eng.ReceiptKey(), nil), seen: map[string]bool{}}, nil
}

// pull ingests every signed file in the inbox it has not read yet. Files that
// fail verification are reported on stderr and ignored.
func (a *app) pull() {
	files, _ := filepath.Glob(filepath.Join(a.dir, "inbox", "*.json"))
	for _, f := range files {
		if a.seen[f] {
			continue
		}
		a.seen[f] = true
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var env intent.Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			fmt.Fprintf(os.Stderr, "inbox: %s: %v\n", filepath.Base(f), err)
			continue
		}
		if err := a.eng.Ingest(env); err != nil {
			fmt.Fprintf(os.Stderr, "inbox: %s refused: %v\n", filepath.Base(f), err)
		}
	}
}

func (a *app) deleteDatabase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.pull()
	db := req.GetString("database", "")
	if db == "" {
		return mcp.NewToolResultError("database is required"), nil
	}
	act := deleteAction(db)
	d, receipt := a.eng.Authorize(act)
	out := map[string]any{"decision": d}
	if d.Verdict != intent.Allow {
		b, _ := json.MarshalIndent(out, "", "  ")
		return mcp.NewToolResultText(string(b)), nil
	}
	err := a.guard.Run(act, receipt, func() error {
		f, err := os.OpenFile(filepath.Join(a.dir, "deleted.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "%s SIMULATED delete of prod/%s receipt=%s\n", time.Now().UTC().Format(time.RFC3339), db, receipt.ID)
		return err
	})
	if err != nil {
		return mcp.NewToolResultError("not executed: " + err.Error()), nil
	}
	out["receipt"] = receipt
	out["executed"] = "simulated: a line was appended to deleted.log. No real database was touched."
	b, _ := json.MarshalIndent(out, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func serveMCP(dir string) error {
	a, err := loadEngine(dir, governance.NewMemoryAuditRecorder(nil))
	if err != nil {
		return err
	}
	s := server.NewMCPServer("joltrin-private-intent", "0.1.0")
	s.AddTool(mcp.NewTool("delete_database",
		mcp.WithDescription("Delete a production database. Runs only if Joltrin Private Intent allows it. If blocked, the response says which verified evidence or approval is missing; do not retry until it exists. Simulated: nothing real is deleted."),
		mcp.WithString("database", mcp.Required(), mcp.Description("Database name, for example orders.")),
	), a.deleteDatabase)
	return server.ServeStdio(s)
}

// ---- the scripted demo ------------------------------------------------------

func runDemo() error {
	dir, err := os.MkdirTemp("", "private-intent-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := initDirQuiet(dir); err != nil {
		return err
	}
	a, err := loadEngine(dir, governance.NewMemoryAuditRecorder(nil))
	if err != nil {
		return err
	}
	step := func(title string) { fmt.Printf("\n== %s\n", title) }
	try := func() *intent.Receipt {
		a.pull()
		act := deleteAction("orders")
		d, r := a.eng.Authorize(act)
		fmt.Printf("   %s\n", d.Summary)
		if r != nil {
			if err := a.guard.Run(act, r, func() error { fmt.Println("   executor: receipt verified, running SIMULATED delete"); return nil }); err != nil {
				fmt.Println("   executor refused:", err)
			}
		}
		return r
	}
	step("1. The agent tries to delete prod/orders")
	try()
	step("2. The backup system signs a validated-backup attestation")
	if _, err := attest(dir, "prod/orders"); err != nil {
		return err
	}
	try()
	step("3. The owner approves this exact delete")
	if _, err := approve(dir, "orders"); err != nil {
		return err
	}
	r := try()
	step("4. The same receipt is replayed")
	if r != nil {
		if err := a.guard.Run(deleteAction("orders"), r, func() error { return nil }); err != nil {
			fmt.Println("   executor refused:", err)
		}
	}
	step("5. A new approval is granted, then revoked before use")
	apID, err := approve(dir, "orders")
	if err != nil {
		return err
	}
	if _, err := revoke(dir, apID); err != nil {
		return err
	}
	try()
	fmt.Println("\nAll steps simulated. No real system was touched.")
	return nil
}

func initDirQuiet(dir string) error {
	old := os.Stdout
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devnull
	defer func() { os.Stdout = old; devnull.Close() }()
	return initDir(dir)
}

func auditDecision(v intent.Verdict) governance.Decision {
	if v == intent.Allow {
		return governance.DecisionAllow
	}
	return governance.DecisionDeny
}
