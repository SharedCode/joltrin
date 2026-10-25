package intent

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const secretPref = "SECRET-WORDING-do-not-leak-orders-db"

type fixture struct {
	t        *testing.T
	now      time.Time
	vault    *Vault
	eng      *Engine
	backup   ed25519.PrivateKey
	owner    ed25519.PrivateKey
	rogue    ed25519.PrivateKey
	engineKP ed25519.PrivateKey
	action   Action
}

func (f *fixture) clock() time.Time { return f.now }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	key := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		pub, priv, err := GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		return pub, priv
	}
	backupPub, backupPriv := key()
	ownerPub, ownerPriv := key()
	_, rogue := key()
	_, engPriv := key()
	f.backup, f.owner, f.rogue, f.engineKP = backupPriv, ownerPriv, rogue, engPriv

	f.vault = NewVault("acme", f.clock)
	err := f.vault.Add(Rule{
		ID: "prod-data-deletion", Label: "Production data deletion", Preference: secretPref,
		Owner: "gerard", Purpose: "protect production data",
		Mode: Grant, Agent: "ops-agent", Tool: "db.delete", Resource: "prod/*", Env: "production",
		Requires: []Requirement{
			{Kind: NeedEvidence, State: "backup_validated", Describe: "verified backup"},
			{Kind: NeedApproval, Role: "production_owner", Describe: "owner approval"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.eng, err = NewEngine(Config{
		Vault: f.vault, SigningKey: engPriv, Now: f.clock,
		Trust: Trust{
			Owner:     ownerPub,
			Issuers:   map[string]TrustedKey{KeyID(backupPub): {Key: backupPub, Scope: []string{"backup_validated"}}},
			Approvers: map[string]TrustedKey{KeyID(ownerPub): {Key: ownerPub, Scope: []string{"production_owner"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.action = Action{Tenant: "acme", Agent: "ops-agent", Tool: "db.delete", Resource: "prod/orders", Env: "production",
		Params: map[string]string{"database": "orders"}}
	return f
}

func (f *fixture) attest(resource string, ttl time.Duration) Evidence {
	ev := SignEvidence(f.backup, Evidence{Tenant: "acme", State: "backup_validated", Resource: resource}, f.now, ttl)
	if err := f.eng.Ingest(Envelope{Type: "evidence", Evidence: &ev}); err != nil {
		f.t.Fatal(err)
	}
	return ev
}

func (f *fixture) approve(ttl time.Duration) Approval {
	ap := SignApproval(f.owner, f.action, "production_owner", f.now, ttl)
	if err := f.eng.Ingest(Envelope{Type: "approval", Approval: &ap}); err != nil {
		f.t.Fatal(err)
	}
	return ap
}

func TestFullScenario(t *testing.T) {
	f := newFixture(t)

	d, r := f.eng.Authorize(f.action)
	if d.Verdict != Block || r != nil {
		t.Fatalf("want BLOCK with no receipt, got %s", d.Verdict)
	}
	if d.Summary != "Blocked: verified backup and owner approval required." {
		t.Fatalf("summary = %q", d.Summary)
	}

	f.attest("prod/orders", time.Hour)
	d, r = f.eng.Authorize(f.action)
	if d.Verdict != RequireApproval || r != nil {
		t.Fatalf("want REQUIRE_APPROVAL, got %s: %s", d.Verdict, d.Summary)
	}
	if len(d.Missing) != 1 || d.Missing[0].Role != "production_owner" {
		t.Fatalf("missing = %+v", d.Missing)
	}

	ap := f.approve(10 * time.Minute)
	d, r = f.eng.Authorize(f.action)
	if d.Verdict != Allow || r == nil {
		t.Fatalf("want ALLOW, got %s: %s", d.Verdict, d.Summary)
	}
	if err := r.Verify(f.eng.ReceiptKey(), f.action, f.now); err != nil {
		t.Fatal(err)
	}
	if len(r.ApprovalIDs) != 1 || r.ApprovalIDs[0] != ap.ID || len(r.EvidenceIDs) != 1 {
		t.Fatalf("receipt refs = %+v", r)
	}

	// The approval was spent, so the same action is blocked for approval again.
	d, _ = f.eng.Authorize(f.action)
	if d.Verdict != RequireApproval {
		t.Fatalf("approval must be single use, got %s", d.Verdict)
	}
}

func TestRevokedApprovalBlocksAgain(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Hour)
	ap := f.approve(10 * time.Minute)
	if d := f.eng.Evaluate(f.action); d.Verdict != Allow {
		t.Fatalf("setup: %s", d.Verdict)
	}
	rv := SignRevocation(f.owner, "acme", ap.ID, f.now)
	if err := f.eng.Ingest(Envelope{Type: "revocation", Revocation: &rv}); err != nil {
		t.Fatal(err)
	}
	if d := f.eng.Evaluate(f.action); d.Verdict != RequireApproval {
		t.Fatalf("after revoke want REQUIRE_APPROVAL, got %s", d.Verdict)
	}
	// Re-submitting the revoked approval must not bring it back.
	if err := f.eng.Ingest(Envelope{Type: "approval", Approval: &ap}); err == nil {
		t.Fatal("revoked approval was accepted again")
	}
}

func TestRevokedEvidenceBlocks(t *testing.T) {
	f := newFixture(t)
	ev := f.attest("prod/orders", time.Hour)
	f.approve(10 * time.Minute)
	// The backup system itself withdraws its claim.
	rv := SignRevocation(f.backup, "acme", ev.ID, f.now)
	if err := f.eng.Ingest(Envelope{Type: "revocation", Revocation: &rv}); err != nil {
		t.Fatal(err)
	}
	if d := f.eng.Evaluate(f.action); d.Verdict != Block {
		t.Fatalf("want BLOCK, got %s", d.Verdict)
	}
}

func TestRogueCannotRevoke(t *testing.T) {
	f := newFixture(t)
	ev := f.attest("prod/orders", time.Hour)
	rv := SignRevocation(f.rogue, "acme", ev.ID, f.now)
	if err := f.eng.Ingest(Envelope{Type: "revocation", Revocation: &rv}); err == nil {
		t.Fatal("untrusted revocation accepted")
	}
}

func TestExpiry(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Minute)
	f.approve(time.Minute)
	if d := f.eng.Evaluate(f.action); d.Verdict != Allow {
		t.Fatalf("setup: %s", d.Verdict)
	}
	f.now = f.now.Add(2 * time.Minute)
	if d := f.eng.Evaluate(f.action); d.Verdict != Block {
		t.Fatalf("expired evidence and approval must block, got %s", d.Verdict)
	}
}

func TestForgedAndWrongEvidence(t *testing.T) {
	f := newFixture(t)
	// Signed by a key nobody trusts.
	forged := SignEvidence(f.rogue, Evidence{Tenant: "acme", State: "backup_validated", Resource: "prod/orders"}, f.now, time.Hour)
	if err := f.eng.Ingest(Envelope{Type: "evidence", Evidence: &forged}); err == nil {
		t.Fatal("evidence from an untrusted key accepted")
	}
	// Trusted issuer, but a state it may not attest.
	wrong := SignEvidence(f.backup, Evidence{Tenant: "acme", State: "tests_passed", Resource: "prod/orders"}, f.now, time.Hour)
	if err := f.eng.Ingest(Envelope{Type: "evidence", Evidence: &wrong}); err == nil {
		t.Fatal("issuer attested a state outside its scope")
	}
	// Tampered after signing.
	ev := SignEvidence(f.backup, Evidence{Tenant: "acme", State: "backup_validated", Resource: "prod/other"}, f.now, time.Hour)
	ev.Resource = "prod/orders"
	if err := f.eng.Ingest(Envelope{Type: "evidence", Evidence: &ev}); err == nil {
		t.Fatal("tampered evidence accepted")
	}
	if d := f.eng.Evaluate(f.action); d.Verdict != Block {
		t.Fatalf("got %s", d.Verdict)
	}
}

func TestEvidenceForOtherResourceDoesNotCount(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/billing", time.Hour)
	f.approve(time.Minute)
	if d := f.eng.Evaluate(f.action); d.Verdict != Block {
		t.Fatalf("backup of another database must not unblock this one, got %s", d.Verdict)
	}
}

func TestApprovalBoundToAction(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/*", time.Hour)
	f.approve(time.Minute)
	other := f.action
	other.Resource = "prod/billing"
	if d := f.eng.Evaluate(other); d.Verdict != RequireApproval {
		t.Fatalf("approval for orders must not cover billing, got %s", d.Verdict)
	}
	changed := f.action
	changed.Params = map[string]string{"database": "orders", "force": "true"}
	if d := f.eng.Evaluate(changed); d.Verdict != RequireApproval {
		t.Fatalf("approval must not survive changed params, got %s", d.Verdict)
	}
}

func TestDefaultDenyAndDenyRule(t *testing.T) {
	f := newFixture(t)
	other := f.action
	other.Agent = "intern-agent"
	if d := f.eng.Evaluate(other); d.Verdict != Block || d.Reasons[0].Code != "no_grant" {
		t.Fatalf("unscoped agent must be default-denied, got %+v", d)
	}
	if err := f.vault.Add(Rule{ID: "no-secrets", Label: "No secrets to external services", Owner: "gerard",
		Purpose: "keep credentials internal", Mode: Deny, Agent: "*", Tool: "http.post", Resource: "external/*"}); err != nil {
		t.Fatal(err)
	}
	post := Action{Tenant: "acme", Agent: "ops-agent", Tool: "http.post", Resource: "external/paste", Env: "production"}
	if d := f.eng.Evaluate(post); d.Verdict != Block || d.Reasons[0].Code != "denied_by_rule" {
		t.Fatalf("got %+v", d)
	}
}

func TestLowRiskGrantNeedsNoApproval(t *testing.T) {
	f := newFixture(t)
	if err := f.vault.Add(Rule{ID: "dev-free", Label: "Development operations", Owner: "gerard",
		Purpose: "let agents work in dev", Mode: Grant, Agent: "*", Tool: "db.*", Resource: "dev/*", Env: "dev"}); err != nil {
		t.Fatal(err)
	}
	a := Action{Tenant: "acme", Agent: "ops-agent", Tool: "db.delete", Resource: "dev/scratch", Env: "dev"}
	if d, r := f.eng.Authorize(a); d.Verdict != Allow || r == nil {
		t.Fatalf("got %s", d.Verdict)
	}
}

func TestRevokingRuleBlocksAndWhatIfPredicts(t *testing.T) {
	f := newFixture(t)
	f.vault.Add(Rule{ID: "dev-free", Label: "Development operations", Owner: "gerard",
		Purpose: "dev", Mode: Grant, Agent: "*", Tool: "db.*", Resource: "dev/*", Env: "dev"})
	a := Action{Tenant: "acme", Agent: "ops-agent", Tool: "db.delete", Resource: "dev/scratch", Env: "dev"}
	imp, err := f.eng.WhatIfRevoke("dev-free", []Action{a})
	if err != nil || imp[0].Before != Allow || imp[0].After != Block {
		t.Fatalf("impact = %+v, %v", imp, err)
	}
	if d := f.eng.Evaluate(a); d.Verdict != Allow {
		t.Fatal("WhatIfRevoke changed the vault")
	}
	if err := f.vault.Revoke("dev-free"); err != nil {
		t.Fatal(err)
	}
	if d := f.eng.Evaluate(a); d.Verdict != Block {
		t.Fatalf("got %s", d.Verdict)
	}
}

func TestRuleExpiry(t *testing.T) {
	f := newFixture(t)
	r, _ := f.vault.Get("prod-data-deletion")
	r.ExpiresAt = f.now.Add(time.Hour)
	if err := f.vault.Edit(r); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if d := f.eng.Evaluate(f.action); d.Reasons[0].Code != "no_grant" {
		t.Fatalf("expired rule still applied: %+v", d)
	}
}

func TestTenantIsolation(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	foreign := f.action
	foreign.Tenant = "globex"
	if d := f.eng.Evaluate(foreign); d.Verdict != Block || d.Reasons[0].Code != "tenant_mismatch" {
		t.Fatalf("got %+v", d)
	}
	ev := SignEvidence(f.backup, Evidence{Tenant: "globex", State: "backup_validated", Resource: "prod/orders"}, f.now, time.Hour)
	if err := f.eng.Ingest(Envelope{Type: "evidence", Evidence: &ev}); err == nil {
		t.Fatal("evidence for another tenant accepted")
	}
}

func TestReceiptForgeryAndGuard(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	_, r := f.eng.Authorize(f.action)
	if r == nil {
		t.Fatal("no receipt")
	}
	g := NewGuard(f.eng.ReceiptKey(), f.clock)
	ran := 0
	run := func() error { ran++; return nil }

	// A different action, a modified policy digest, a flipped verdict and a
	// foreign signature all fail without running anything.
	other := f.action
	other.Resource = "prod/billing"
	if err := g.Run(other, r, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("receipt reused for another action: %v", err)
	}
	tampered := *r
	tampered.PolicyDigest = strings.Repeat("0", 64)
	if err := g.Run(f.action, &tampered, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("modified policy digest accepted: %v", err)
	}
	extended := *r
	extended.ExpiresAt += 3600
	if err := g.Run(f.action, &extended, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("extended expiry accepted: %v", err)
	}
	_, rogue, _ := GenerateKey()
	resigned := *r
	resigned.Sig = nil
	resigned.Sig = ed25519.Sign(rogue, signedBytes("receipt", &resigned))
	if err := g.Run(f.action, &resigned, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("receipt signed by another key accepted: %v", err)
	}
	if err := g.Run(f.action, nil, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("nil receipt accepted: %v", err)
	}
	if ran != 0 {
		t.Fatalf("action ran %d times on invalid receipts", ran)
	}

	if err := g.Run(f.action, r, run); err != nil || ran != 1 {
		t.Fatalf("valid receipt: %v ran=%d", err, ran)
	}
	if err := g.Run(f.action, r, run); !errors.Is(err, ErrNotAuthorized) || ran != 1 {
		t.Fatalf("replay must fail: %v ran=%d", err, ran)
	}

	// An expired receipt fails even if never used.
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	_, r2 := f.eng.Authorize(f.action)
	f.now = f.now.Add(5 * time.Minute)
	if err := g.Run(f.action, r2, run); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("expired receipt accepted: %v", err)
	}
}

func TestBlockedActionNeverGetsReceipt(t *testing.T) {
	f := newFixture(t)
	if _, r := f.eng.Authorize(f.action); r != nil {
		t.Fatal("receipt issued for a blocked action")
	}
}

func TestOneApprovalSpentOnceUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, _ := f.eng.Authorize(f.action); d.Verdict == Allow {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("one approval authorized %d actions", allowed.Load())
	}
}

func TestPreferenceNeverLeaks(t *testing.T) {
	f := newFixture(t)
	var logged []Entry
	f.eng.cfg.Audit = func(e Entry) { logged = append(logged, e) }
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	d, r := f.eng.Authorize(f.action)
	blocked := f.eng.Evaluate(Action{Tenant: "acme", Agent: "x", Tool: "y", Resource: "z", Env: "e"})
	for name, v := range map[string]any{"decision": d, "receipt": r, "blocked": blocked, "audit": logged} {
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), secretPref) || strings.Contains(string(b), "orders-db") {
			t.Fatalf("%s leaked private text: %s", name, b)
		}
	}
}

func TestPolicyDigestChangesWithPolicy(t *testing.T) {
	f := newFixture(t)
	_, before, v1 := f.vault.snapshot(f.now)
	r, _ := f.vault.Get("prod-data-deletion")
	r.Requires = r.Requires[:1] // drop the approval requirement
	if err := f.vault.Edit(r); err != nil {
		t.Fatal(err)
	}
	_, after, v2 := f.vault.snapshot(f.now)
	if before == after || v2 <= v1 {
		t.Fatal("weakening the policy must change digest and version")
	}
}

func TestSealedVault(t *testing.T) {
	f := newFixture(t)
	key := []byte(strings.Repeat("k", 32))
	sealed, err := f.vault.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), secretPref) {
		t.Fatal("preference visible in sealed vault")
	}
	other := NewVault("acme", f.clock)
	if err := other.Unseal(key, sealed); err != nil {
		t.Fatal(err)
	}
	if r, _ := other.Get("prod-data-deletion"); r.Preference != secretPref {
		t.Fatal("preference lost in round trip")
	}
	if err := NewVault("acme", nil).Unseal([]byte(strings.Repeat("x", 32)), sealed); err == nil {
		t.Fatal("wrong key opened the vault")
	}
	if err := NewVault("globex", nil).Unseal(key, sealed); err == nil {
		t.Fatal("vault opened under another tenant")
	}
	sealed[len(sealed)-1] ^= 1
	if err := NewVault("acme", nil).Unseal(key, sealed); err == nil {
		t.Fatal("damaged vault opened")
	}
	if _, err := f.vault.Seal([]byte("short")); !errors.Is(err, ErrBadKey) {
		t.Fatal("short key accepted")
	}
}

func TestSaveLoadFile(t *testing.T) {
	f := newFixture(t)
	key := []byte(strings.Repeat("k", 32))
	p := t.TempDir() + "/vault.sealed"
	if err := f.vault.SaveFile(p, key); err != nil {
		t.Fatal(err)
	}
	v2 := NewVault("acme", nil)
	if err := v2.LoadFile(p, key); err != nil || len(v2.List()) != 1 {
		t.Fatalf("load: %v", err)
	}
}

func TestSuggestionsStayInert(t *testing.T) {
	f := newFixture(t)
	s := NewSuggestions(3)
	a := Action{Tenant: "acme", Agent: "ops-agent", Tool: "deploy", Resource: "staging/api", Env: "staging"}
	for i := 0; i < 3; i++ {
		s.Observe(a)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("pending = %v", s.Pending())
	}
	if d := f.eng.Evaluate(a); d.Verdict != Block {
		t.Fatalf("a suggestion acted as a permission: %s", d.Verdict)
	}
	rule := Rule{ID: "staging-deploys", Label: "Staging deploys", Owner: "gerard", Purpose: "routine staging deploys"}
	if err := s.Accept(f.vault, s.Pending()[0].ID, rule); err == nil {
		t.Fatal("accepted without an expiry")
	}
	rule.ExpiresAt = f.now.Add(24 * time.Hour)
	if err := s.Accept(f.vault, s.Pending()[0].ID, rule); err != nil {
		t.Fatal(err)
	}
	if d := f.eng.Evaluate(a); d.Verdict != Allow {
		t.Fatalf("accepted rule not active: %s", d.Verdict)
	}
	got, _ := f.vault.Get("staging-deploys")
	if got.Provenance.Source != "suggestion" {
		t.Fatalf("provenance = %+v", got.Provenance)
	}
}

func TestRuleValidation(t *testing.T) {
	f := newFixture(t)
	bad := []Rule{
		{ID: "a", Label: "x", Owner: "o", Purpose: "p", Mode: "maybe", Agent: "*", Tool: "*", Resource: "*"},
		{ID: "b", Label: "x", Owner: "o", Purpose: "p", Mode: Grant, Agent: "[", Tool: "*", Resource: "*"},
		{ID: "c", Label: "x", Owner: "o", Purpose: "p", Mode: Deny, Agent: "*", Tool: "*", Resource: "*",
			Requires: []Requirement{{Kind: NeedApproval, Role: "r", Describe: "d"}}},
		{ID: "d", Label: "x", Owner: "o", Purpose: "", Mode: Grant, Agent: "*", Tool: "*", Resource: "*"},
	}
	for _, r := range bad {
		if err := f.vault.Add(r); err == nil {
			t.Errorf("rule %q accepted", r.ID)
		}
	}
}

func TestIncompleteActionBlocks(t *testing.T) {
	f := newFixture(t)
	a := f.action
	a.Env = ""
	if d := f.eng.Evaluate(a); d.Verdict != Block || d.Reasons[0].Code != "invalid_action" {
		t.Fatalf("got %+v", d)
	}
}

func TestActionHashIsStable(t *testing.T) {
	a := Action{Tenant: "t", Agent: "a", Tool: "x", Resource: "r", Env: "e", Params: map[string]string{"b": "2", "a": "1"}}
	b := Action{Tenant: "t", Agent: "a", Tool: "x", Resource: "r", Env: "e", Params: map[string]string{"a": "1", "b": "2"}}
	if a.Hash() != b.Hash() {
		t.Fatal("param order changed the hash")
	}
	b.Params["a"] = "9"
	if a.Hash() == b.Hash() {
		t.Fatal("param change did not change the hash")
	}
}

func FuzzGuardNeverRunsOnGarbage(f *testing.F) {
	f.Add([]byte(`{"v":1}`))
	f.Add([]byte(`{`))
	f.Add([]byte(`{"v":1,"verdict":"ALLOW","sig":"AAAA"}`))
	pub, _, _ := GenerateKey()
	act := Action{Tenant: "t", Agent: "a", Tool: "x", Resource: "r", Env: "e"}
	f.Fuzz(func(t *testing.T, data []byte) {
		var r Receipt
		if json.Unmarshal(data, &r) != nil {
			return
		}
		g := NewGuard(pub, nil)
		if err := g.Run(act, &r, func() error { t.Fatal("ran on a receipt not signed by the trusted key"); return nil }); err == nil {
			t.Fatal("garbage receipt accepted")
		}
	})
}

func FuzzIngestNeverPanics(f *testing.F) {
	f.Add([]byte(`{"type":"evidence"}`))
	f.Add([]byte(`{"type":"approval","approval":{}}`))
	f.Add([]byte(`{"type":"revocation","revocation":{}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var env Envelope
		if json.Unmarshal(data, &env) != nil {
			return
		}
		pub, priv, _ := GenerateKey()
		v := NewVault("t", nil)
		e, _ := NewEngine(Config{Vault: v, SigningKey: priv, Trust: Trust{Owner: pub}})
		_ = e.Ingest(env)
	})
}

func BenchmarkAuthorizeAllow(b *testing.B) {
	f := newFixture(&testing.T{})
	f.attest("prod/orders", 24*time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ap := SignApproval(f.owner, f.action, "production_owner", f.now, time.Minute)
		_ = f.eng.Ingest(Envelope{Type: "approval", Approval: &ap})
		f.eng.Authorize(f.action)
	}
}

func BenchmarkEvaluateBlock(b *testing.B) {
	f := newFixture(&testing.T{})
	for i := 0; i < b.N; i++ {
		f.eng.Evaluate(f.action)
	}
}

func TestReceiptVerifySignatureNeedsNoAction(t *testing.T) {
	f := newFixture(t)
	f.attest("prod/orders", time.Hour)
	f.approve(time.Minute)
	_, r := f.eng.Authorize(f.action)
	if r == nil {
		t.Fatal("no receipt")
	}
	if err := r.VerifySignature(f.eng.ReceiptKey()); err != nil {
		t.Fatalf("genuine receipt rejected: %v", err)
	}
	tampered := *r
	tampered.PolicyDigest = strings.Repeat("0", 64)
	if err := tampered.VerifySignature(f.eng.ReceiptKey()); err == nil {
		t.Fatal("tampered receipt accepted")
	}
	otherPub, _, _ := GenerateKey()
	if err := r.VerifySignature(otherPub); err == nil {
		t.Fatal("receipt accepted under a key that did not sign it")
	}
	var none *Receipt
	if err := none.VerifySignature(f.eng.ReceiptKey()); err == nil {
		t.Fatal("nil receipt accepted")
	}
}
