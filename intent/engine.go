package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

// Entry is what the engine reports to an audit hook for every Authorize call.
// It carries ids, hashes and verdicts only, never a Preference or a param.
type Entry struct {
	At         time.Time
	Tenant     string
	Agent      string
	Tool       string
	Resource   string
	Verdict    Verdict
	ActionHash string
	RuleIDs    []string
	ReceiptID  string
}

// Config builds an Engine.
type Config struct {
	Vault *Vault
	Trust Trust
	// SigningKey signs receipts. Guards verify with its public half.
	SigningKey ed25519.PrivateKey
	// ReceiptTTL is how long a receipt stays usable. Default 60 seconds.
	ReceiptTTL time.Duration
	Now        func() time.Time
	// Audit, if set, is called once per Authorize with a metadata-only Entry.
	Audit func(Entry)
}

// Engine evaluates actions for one tenant. It is safe for concurrent use.
// Authorize holds the engine lock from evaluation through consuming the
// approval, so two concurrent calls cannot both spend one approval.
type Engine struct {
	cfg Config

	mu        sync.Mutex
	evidence  map[string]Evidence
	approvals map[string]*approvalState
	revoked   map[string]bool
}

type approvalState struct {
	Approval
	used bool
}

// NewEngine validates cfg and returns an Engine.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Vault == nil {
		return nil, errors.New("intent: engine needs a vault")
	}
	if len(cfg.SigningKey) != ed25519.PrivateKeySize {
		return nil, errors.New("intent: engine needs a receipt signing key")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ReceiptTTL <= 0 {
		cfg.ReceiptTTL = 60 * time.Second
	}
	return &Engine{cfg: cfg, evidence: map[string]Evidence{}, approvals: map[string]*approvalState{}, revoked: map[string]bool{}}, nil
}

// ReceiptKey is the public key a Guard uses to verify this engine's receipts.
func (e *Engine) ReceiptKey() ed25519.PublicKey {
	return e.cfg.SigningKey.Public().(ed25519.PublicKey)
}

// Ingest accepts one signed artifact. It is verified against the trust list
// before it is stored; anything unsigned, mis-signed, from an untrusted key,
// for another tenant or already revoked is refused and changes nothing.
func (e *Engine) Ingest(env Envelope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	tenant := e.cfg.Vault.Tenant()
	switch env.Type {
	case "evidence":
		if env.Evidence == nil {
			return errors.New("intent: empty evidence envelope")
		}
		ev := *env.Evidence
		if ev.Tenant != tenant {
			return fmt.Errorf("intent: evidence %q is for another tenant", ev.ID)
		}
		if err := e.cfg.Trust.checkEvidence(ev); err != nil {
			return err
		}
		if e.revoked[ev.ID] {
			return fmt.Errorf("intent: evidence %q was revoked", ev.ID)
		}
		e.evidence[ev.ID] = ev
	case "approval":
		if env.Approval == nil {
			return errors.New("intent: empty approval envelope")
		}
		ap := *env.Approval
		if ap.Tenant != tenant {
			return fmt.Errorf("intent: approval %q is for another tenant", ap.ID)
		}
		if err := e.cfg.Trust.checkApproval(ap); err != nil {
			return err
		}
		if e.revoked[ap.ID] {
			return fmt.Errorf("intent: approval %q was revoked", ap.ID)
		}
		if old, ok := e.approvals[ap.ID]; ok && old.used {
			return fmt.Errorf("intent: approval %q was already used", ap.ID)
		}
		e.approvals[ap.ID] = &approvalState{Approval: ap}
	case "revocation":
		if env.Revocation == nil {
			return errors.New("intent: empty revocation envelope")
		}
		return e.revokeLocked(*env.Revocation)
	default:
		return fmt.Errorf("intent: unknown envelope type %q", env.Type)
	}
	return nil
}

func (e *Engine) revokeLocked(r Revocation) error {
	if r.Tenant != e.cfg.Vault.Tenant() {
		return errors.New("intent: revocation is for another tenant")
	}
	if len(e.cfg.Trust.Owner) == ed25519.PublicKeySize && KeyID(e.cfg.Trust.Owner) == r.Signer && r.verify(e.cfg.Trust.Owner) {
		e.revoked[r.TargetID] = true
		return nil
	}
	// Otherwise the signer must be the key that signed the target.
	var signer string
	if ev, ok := e.evidence[r.TargetID]; ok {
		signer = ev.Issuer
	} else if ap, ok := e.approvals[r.TargetID]; ok {
		signer = ap.Approver
	}
	if signer == "" || signer != r.Signer {
		return errUntrusted
	}
	k, ok := e.cfg.Trust.Issuers[signer]
	if !ok {
		k, ok = e.cfg.Trust.Approvers[signer]
	}
	if !ok || !r.verify(k.Key) {
		return errUntrusted
	}
	e.revoked[r.TargetID] = true
	return nil
}

// Evaluate decides an action without changing anything: no approval is spent
// and no receipt is made. Use it for dry runs and for UIs.
func (e *Engine) Evaluate(a Action) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evaluateLocked(a)
}

// Authorize decides an action. On ALLOW it spends the approvals the decision
// relied on and returns a signed Receipt. On any other verdict the receipt is
// nil. Any internal failure is a BLOCK.
func (e *Engine) Authorize(a Action) (d Decision, r *Receipt) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			d = Decision{Verdict: Block, Summary: "Blocked: the policy engine failed, so the action was refused.",
				Reasons: []Reason{{Code: "internal_error", Message: "evaluation failed closed"}}, ActionHash: a.Hash()}
			r = nil
		}
	}()
	d = e.evaluateLocked(a)
	if d.Verdict == Allow {
		now := e.cfg.Now().UTC()
		var err error
		r, err = e.newReceipt(a, d, now)
		if err != nil {
			d = Decision{Verdict: Block, Summary: "Blocked: a receipt could not be issued.",
				Reasons: []Reason{{Code: "receipt_failed", Message: err.Error()}}, ActionHash: a.Hash()}
			return d, nil
		}
		for _, id := range d.approvalIDs {
			e.approvals[id].used = true
		}
	}
	if e.cfg.Audit != nil {
		en := Entry{At: e.cfg.Now().UTC(), Tenant: a.Tenant, Agent: a.Agent, Tool: a.Tool, Resource: a.Resource,
			Verdict: d.Verdict, ActionHash: d.ActionHash, RuleIDs: d.AppliedRules}
		if r != nil {
			en.ReceiptID = r.ID
		}
		e.cfg.Audit(en)
	}
	return d, r
}

func (e *Engine) evaluateLocked(a Action) Decision {
	now := e.cfg.Now().UTC()
	rules, digest, ver := e.cfg.Vault.snapshot(now)
	d := Decision{ActionHash: a.Hash(), PolicyDigest: digest, PolicyVer: ver, AppliedRules: []string{}, Reasons: []Reason{}}
	block := func(code, msg string) Decision {
		d.Verdict, d.Summary = Block, "Blocked: "+msg
		d.Reasons = append(d.Reasons, Reason{Code: code, Message: msg})
		return d
	}

	if a.Tenant != e.cfg.Vault.Tenant() {
		return block("tenant_mismatch", "this action belongs to a different tenant.")
	}
	if !a.complete() {
		return block("invalid_action", "the action is missing its agent, tool, resource or environment.")
	}

	var denies, grants []Rule
	for _, r := range rules {
		if !r.matches(a) {
			continue
		}
		if r.Mode == Deny {
			denies = append(denies, r)
		} else {
			grants = append(grants, r)
		}
	}
	if len(denies) > 0 {
		for _, r := range denies {
			d.AppliedRules = append(d.AppliedRules, r.ID)
			d.Reasons = append(d.Reasons, Reason{Code: "denied_by_rule", RuleID: r.ID, Message: "rule \"" + r.Label + "\" forbids this action"})
		}
		d.Verdict, d.Summary = Block, "Blocked: rule \""+denies[0].Label+"\" forbids this action."
		return d
	}
	if len(grants) == 0 {
		return block("no_grant", "no active rule lets this agent do this to this resource.")
	}

	// Gather the union of requirements across every matching grant. When two
	// rules apply, both must be satisfied: the stricter one wins.
	reqs := map[string]Missing{}
	for _, r := range grants {
		d.AppliedRules = append(d.AppliedRules, r.ID)
		for _, q := range r.Requires {
			if _, ok := reqs[q.key()]; !ok {
				reqs[q.key()] = Missing{Kind: q.Kind, State: q.State, Role: q.Role, Describe: q.Describe, RuleID: r.ID}
			}
		}
	}
	keys := make([]string, 0, len(reqs))
	for k := range reqs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Facts that currently hold: evidence that is signed (checked at ingest),
	// unexpired, unrevoked and about this resource.
	holds := map[verify.State]bool{}
	evBy := map[string]string{}
	for _, ev := range e.evidence {
		if e.revoked[ev.ID] || now.Unix() >= ev.ExpiresAt || !glob(ev.Resource, a.Resource) {
			continue
		}
		if _, ok := evBy[ev.State]; !ok || ev.ID < evBy[ev.State] {
			evBy[ev.State] = ev.ID
		}
		holds[verify.State(ev.State)] = true
	}

	missingEv := e.missingEvidence(keys, reqs, holds)
	var missingAp []Missing
	var usedApprovals []string
	for _, k := range keys {
		q := reqs[k]
		if q.Kind != NeedApproval {
			continue
		}
		if id := e.findApproval(q.Role, d.ActionHash, now); id != "" {
			usedApprovals = append(usedApprovals, id)
		} else {
			missingAp = append(missingAp, q)
		}
	}

	switch {
	case len(missingEv) > 0:
		d.Verdict, d.Missing = Block, append(missingEv, missingAp...)
		d.Summary = "Blocked: " + joinPhrases(phrases(d.Missing)) + " required."
		d.Reasons = append(d.Reasons, Reason{Code: "missing_prerequisite", RuleID: d.Missing[0].RuleID, Message: d.Summary})
	case len(missingAp) > 0:
		d.Verdict, d.Missing = RequireApproval, missingAp
		d.Summary = "Approval required: " + joinPhrases(phrases(missingAp)) + "."
		d.Reasons = append(d.Reasons, Reason{Code: "approval_needed", RuleID: missingAp[0].RuleID, Message: d.Summary})
	default:
		d.Verdict = Allow
		d.Summary = "Allowed: every prerequisite is met."
		d.Reasons = append(d.Reasons, Reason{Code: "prerequisites_met", Message: d.Summary})
		for _, k := range keys {
			if q := reqs[k]; q.Kind == NeedEvidence {
				d.evidenceIDs = append(d.evidenceIDs, evBy[q.State])
			}
		}
		d.approvalIDs = usedApprovals
	}
	return d
}

// missingEvidence asks the verify engine which required states are absent. It
// builds a one-step workflow whose step would perform the action, with one
// safety rule per required state, then checks it against a scratch trace that
// holds the current valid facts. verify reports the first missing state, so
// the loop marks it held and asks again until nothing is missing.
func (e *Engine) missingEvidence(keys []string, reqs map[string]Missing, holds map[verify.State]bool) []Missing {
	var rules []verify.SafetyRule
	byState := map[string]Missing{}
	for _, k := range keys {
		q := reqs[k]
		if q.Kind != NeedEvidence {
			continue
		}
		rules = append(rules, verify.SafetyRule{Name: "needs-" + q.State, Forbidden: "action_performed", Requires: verify.State(q.State)})
		byState[q.State] = q
	}
	if len(rules) == 0 {
		return nil
	}
	wf, err := verify.NewWorkflow([]verify.Step{{ID: "act", Establishes: []verify.State{"action_performed"}}}, rules, nil)
	if err != nil {
		return nil
	}
	tr := verify.NewTrace()
	for s := range holds {
		tr.Holds[s] = true
	}
	var out []Missing
	for i := 0; i <= len(rules); i++ {
		err := wf.CheckSafety(tr, "act")
		var v *verify.Violation
		if !errors.As(err, &v) {
			break
		}
		out = append(out, byState[string(v.MissingState)])
		tr.Holds[v.MissingState] = true
	}
	return out
}

func (e *Engine) findApproval(role, hash string, now time.Time) string {
	best := ""
	for id, ap := range e.approvals {
		if ap.used || e.revoked[id] || ap.Role != role || ap.ActionHash != hash || now.Unix() >= ap.ExpiresAt {
			continue
		}
		if best == "" || id < best {
			best = id
		}
	}
	return best
}

func phrases(m []Missing) []string {
	out := make([]string, len(m))
	for i := range m {
		out[i] = m[i].Describe
	}
	return out
}

// WhatIfRevoke shows how revoking ruleID would change the verdict for each
// sample action. Nothing is changed.
func (e *Engine) WhatIfRevoke(ruleID string, samples []Action) ([]Impact, error) {
	r, ok := e.cfg.Vault.Get(ruleID)
	if !ok {
		return nil, fmt.Errorf("intent: no rule %q", ruleID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Impact, 0, len(samples))
	before := make([]Verdict, len(samples))
	for i, a := range samples {
		before[i] = e.evaluateLocked(a).Verdict
	}
	// Evaluate against a copy of the vault with the rule revoked.
	shadow := NewVault(e.cfg.Vault.Tenant(), e.cfg.Now)
	for _, x := range e.cfg.Vault.List() {
		if x.ID == r.ID {
			x.Revoked = true
		}
		shadow.rules[x.ID] = &x
	}
	alt := &Engine{cfg: e.cfg, evidence: e.evidence, approvals: e.approvals, revoked: e.revoked}
	alt.cfg.Vault = shadow
	for i, a := range samples {
		out = append(out, Impact{Action: a, Before: before[i], After: alt.evaluateLocked(a).Verdict})
	}
	return out, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // never reached on supported platforms; Authorize turns it into a BLOCK
	}
	return hex.EncodeToString(b)
}
