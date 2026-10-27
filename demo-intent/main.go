// Command demo-intent is the Joltrin Private Intent engine compiled to
// WebAssembly. It is the real intent package: the same rule matching, signed
// evidence, signed approvals, receipts and Guard that the Go library uses,
// running in the visitor's browser with keys generated on page load.
//
// Everything it "deletes" is simulated. There is no database, no network call
// and no server. The three roles (backup system, owner, engine) are played by
// three key pairs made in this page's memory.
//go:build js && wasm

package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"syscall/js"
	"time"

	"github.com/sharedcode/joltrin/v5/intent"
)

const preference = "Never delete production data unless a validated backup exists and the production owner approves."

type session struct {
	vault       *intent.Vault
	eng         *intent.Engine
	guard       *intent.Guard
	backupKey   ed25519.PrivateKey
	ownerKey    ed25519.PrivateKey
	action      intent.Action
	evidence    []intent.Evidence
	approvals   []intent.Approval
	lastReceipt *intent.Receipt
	log         []logLine
}

type logLine struct {
	Who  string `json:"who"`
	Text string `json:"text"`
	Kind string `json:"kind"` // ok, block, ask, info
	// Verdict is set on the engine's own lines so the page can colour the banner.
	Verdict string `json:"verdict,omitempty"`
}

var s *session

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func newSession() *session {
	backupPub, backupPriv, _ := intent.GenerateKey()
	ownerPub, ownerPriv, _ := intent.GenerateKey()
	_, enginePriv, _ := intent.GenerateKey()

	v := intent.NewVault("demo", nil)
	must(0, v.Add(intent.Rule{
		ID: "prod-data-deletion", Label: "Production data deletion", Preference: preference,
		Owner: "you", Purpose: "protect production data",
		Mode: intent.Grant, Agent: "ops-agent", Tool: "db.delete", Resource: "prod/*", Env: "production",
		Requires: []intent.Requirement{
			{Kind: intent.NeedEvidence, State: "backup_validated", Describe: "verified backup"},
			{Kind: intent.NeedApproval, Role: "production_owner", Describe: "owner approval"},
		},
	}))
	eng := must(intent.NewEngine(intent.Config{
		Vault: v, SigningKey: enginePriv, ReceiptTTL: 2 * time.Minute,
		Trust: intent.Trust{
			Owner:     ownerPub,
			Issuers:   map[string]intent.TrustedKey{intent.KeyID(backupPub): {Key: backupPub, Scope: []string{"backup_validated"}}},
			Approvers: map[string]intent.TrustedKey{intent.KeyID(ownerPub): {Key: ownerPub, Scope: []string{"production_owner"}}},
		},
	}))
	return &session{
		log:   []logLine{},
		vault: v, eng: eng, guard: intent.NewGuard(eng.ReceiptKey(), nil),
		backupKey: backupPriv, ownerKey: ownerPriv,
		action: intent.Action{Tenant: "demo", Agent: "ops-agent", Tool: "db.delete", Resource: "prod/orders", Env: "production",
			Params: map[string]string{"database": "orders"}},
	}
}

func (s *session) say(who, kind, text string) {
	s.log = append(s.log, logLine{Who: who, Text: text, Kind: kind})
}

func (s *session) attempt() {
	s.say("agent", "info", "ops-agent asks to run db.delete on prod/orders (SIMULATED).")
	d, r := s.eng.Authorize(s.action)
	kind := map[intent.Verdict]string{intent.Allow: "ok", intent.Block: "block", intent.RequireApproval: "ask"}[d.Verdict]
	s.log = append(s.log, logLine{Who: "joltrin", Text: d.Summary, Kind: kind, Verdict: string(d.Verdict)})
	if r == nil {
		return
	}
	s.lastReceipt = r
	err := s.guard.Run(s.action, r, func() error { return nil })
	if err != nil {
		s.say("executor", "block", "Refused: "+err.Error())
		return
	}
	s.say("executor", "ok", "Receipt verified. The delete would run now. (Simulated: nothing was deleted.)")
}

func (s *session) validateBackup() {
	ev := intent.SignEvidence(s.backupKey, intent.Evidence{Tenant: "demo", State: "backup_validated", Resource: "prod/orders"}, time.Now(), time.Hour)
	if err := s.eng.Ingest(intent.Envelope{Type: "evidence", Evidence: &ev}); err != nil {
		s.say("backup system", "block", err.Error())
		return
	}
	s.evidence = append(s.evidence, ev)
	s.say("backup system", "info", "Signed: backup of prod/orders validated (simulated). The agent did not write this and cannot.")
}

func (s *session) approve() {
	ap := intent.SignApproval(s.ownerKey, s.action, "production_owner", time.Now(), 10*time.Minute)
	if err := s.eng.Ingest(intent.Envelope{Type: "approval", Approval: &ap}); err != nil {
		s.say("owner", "block", err.Error())
		return
	}
	s.approvals = append(s.approvals, ap)
	s.say("owner", "info", "Signed: approve this exact delete of prod/orders, valid 10 minutes, one use.")
}

func (s *session) revoke() {
	if len(s.approvals) == 0 {
		s.say("owner", "info", "There is no approval to revoke.")
		return
	}
	ap := s.approvals[len(s.approvals)-1]
	rv := intent.SignRevocation(s.ownerKey, "demo", ap.ID, time.Now())
	if err := s.eng.Ingest(intent.Envelope{Type: "revocation", Revocation: &rv}); err != nil {
		s.say("owner", "block", err.Error())
		return
	}
	s.say("owner", "info", "Revoked approval "+ap.ID+".")
}

func (s *session) tamper() {
	if s.lastReceipt == nil {
		s.say("executor", "info", "No receipt yet. Get an ALLOW first, then try to tamper with it.")
		return
	}
	forged := *s.lastReceipt
	forged.PolicyDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	err := s.guard.Run(s.action, &forged, func() error { return nil })
	s.say("executor", "block", "Edited receipt (policy digest changed): "+errText(err))
}

func (s *session) replay() {
	if s.lastReceipt == nil {
		s.say("executor", "info", "No receipt yet. Get an ALLOW first, then replay it.")
		return
	}
	err := s.guard.Run(s.action, s.lastReceipt, func() error { return nil })
	if err == nil {
		s.say("executor", "ok", "Receipt accepted.")
		return
	}
	s.say("executor", "block", "Same receipt used again: "+errText(err))
}

func errText(err error) string {
	if err == nil {
		return "accepted (this should not happen)"
	}
	return "refused. " + err.Error()
}

// state is what the page renders. The Preference text is returned only under
// owner_view; the verifier_view is exactly what a receipt or decision exposes.
func (s *session) state() string {
	d := s.eng.Evaluate(s.action)
	type ownerRule struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		Preference string `json:"preference"`
		Version    int    `json:"version"`
	}
	r, _ := s.vault.Get("prod-data-deletion")
	out := map[string]any{
		"owner_view":    ownerRule{r.ID, r.Label, r.Preference, r.Version},
		"verifier_view": map[string]any{"rule_id": r.ID, "label": r.Label, "policy_digest": d.PolicyDigest, "policy_version": d.PolicyVer, "action_hash": d.ActionHash},
		"decision":      d,
		"has_backup":    !hasMissing(d, intent.NeedEvidence),
		"has_approval":  !hasMissing(d, intent.NeedApproval) && len(d.AppliedRules) > 0,
		"receipt":       s.lastReceipt,
		"signer_key":    hex.EncodeToString(s.eng.ReceiptKey()),
		"log":           s.log,
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func hasMissing(d intent.Decision, k intent.RequirementKind) bool {
	for _, m := range d.Missing {
		if m.Kind == k {
			return true
		}
	}
	return false
}

// checkReceipt is what anyone holding a receipt and the signer's public key can
// do: it never sees the action, the rule wording or the vault. It checks the
// signature only, so a receipt edited by even one character fails.
func checkReceipt(raw, keyHex string) string {
	type result struct {
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
		Verdict   string `json:"verdict,omitempty"`
		Signer    string `json:"signer,omitempty"`
		IssuedAt  int64  `json:"issued_at,omitempty"`
		ExpiresAt int64  `json:"expires_at,omitempty"`
		Expired   bool   `json:"expired,omitempty"`
	}
	out := func(r result) string { b, _ := json.Marshal(r); return string(b) }
	var r intent.Receipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return out(result{Error: "That is not a receipt. Paste the receipt JSON from the demo."})
	}
	pub, err := hex.DecodeString(keyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return out(result{Error: "The signer key must be 64 hex characters."})
	}
	if err := r.VerifySignature(ed25519.PublicKey(pub)); err != nil {
		return out(result{Error: "The signature does not match. The receipt was edited, or this is not the signer's key."})
	}
	return out(result{OK: true, Verdict: string(r.Verdict), Signer: r.Signer, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt, Expired: time.Now().Unix() >= r.ExpiresAt})
}

func bind(name string, f func()) {
	js.Global().Set(name, js.FuncOf(func(this js.Value, args []js.Value) any {
		f()
		return s.state()
	}))
}

func main() {
	s = newSession()
	bind("joltrinIntentState", func() {})
	bind("joltrinIntentAttempt", s.attempt)
	bind("joltrinIntentValidateBackup", s.validateBackup)
	bind("joltrinIntentApprove", s.approve)
	bind("joltrinIntentRevoke", s.revoke)
	bind("joltrinIntentTamper", s.tamper)
	bind("joltrinIntentReplay", s.replay)
	js.Global().Set("joltrinIntentCheckReceipt", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 2 {
			return checkReceipt("", "")
		}
		return checkReceipt(args[0].String(), args[1].String())
	}))
	js.Global().Set("joltrinIntentReset", js.FuncOf(func(this js.Value, args []js.Value) any {
		s = newSession()
		return s.state()
	}))
	js.Global().Set("__JOLTRIN_INTENT_WASM_READY__", js.ValueOf(true))
	if doc := js.Global().Get("document"); !doc.IsUndefined() && !doc.IsNull() {
		doc.Call("dispatchEvent", js.Global().Get("CustomEvent").New("joltrin-intent-wasm-ready"))
	}
	select {}
}
