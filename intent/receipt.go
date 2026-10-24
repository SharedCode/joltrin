package intent

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ReceiptStatement is printed in every receipt so nobody reads it as more
// than it is.
const ReceiptStatement = "Records that an authorization decision was made for this action under this policy. It does not prove the action ran or succeeded."

// Receipt is the signed record of an ALLOW decision. It names the action it
// covers by hash, the policy by digest, and the rules, evidence and approvals
// by id. It holds no preference text and no action parameters.
type Receipt struct {
	Version      int      `json:"v"`
	ID           string   `json:"id"`
	Tenant       string   `json:"tenant"`
	Verdict      Verdict  `json:"verdict"`
	ActionHash   string   `json:"action_hash"`
	PolicyDigest string   `json:"policy_digest"`
	PolicyVer    int      `json:"policy_version"`
	RuleIDs      []string `json:"rule_ids"`
	EvidenceIDs  []string `json:"evidence_ids"`
	ApprovalIDs  []string `json:"approval_ids"`
	IssuedAt     int64    `json:"issued_at"`
	ExpiresAt    int64    `json:"expires_at"`
	Nonce        string   `json:"nonce"`
	Statement    string   `json:"statement"`
	Signer       string   `json:"signer"`
	Sig          []byte   `json:"sig"`
}

func (e *Engine) newReceipt(a Action, d Decision, now time.Time) (*Receipt, error) {
	r := &Receipt{
		Version: 1, ID: newID("rc"), Tenant: a.Tenant, Verdict: Allow, ActionHash: d.ActionHash,
		PolicyDigest: d.PolicyDigest, PolicyVer: d.PolicyVer,
		RuleIDs: nonNil(d.AppliedRules), EvidenceIDs: nonNil(d.evidenceIDs), ApprovalIDs: nonNil(d.approvalIDs),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(e.cfg.ReceiptTTL).Unix(),
		Nonce: randomHex(16), Statement: ReceiptStatement, Signer: KeyID(e.ReceiptKey()),
	}
	r.Sig = ed25519.Sign(e.cfg.SigningKey, signedBytes("receipt", r))
	return r, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s...)
}

// Verify checks the signature, the verdict and the expiry, and that the
// receipt covers action. It is a pure function so any party can run it.
func (r *Receipt) Verify(pub ed25519.PublicKey, action Action, now time.Time) error {
	if r == nil {
		return errors.New("intent: no receipt")
	}
	c := *r
	c.Sig = nil
	if r.Version != 1 || len(r.Sig) != ed25519.SignatureSize || !ed25519.Verify(pub, signedBytes("receipt", &c), r.Sig) {
		return errors.New("intent: receipt signature is not valid")
	}
	if r.Verdict != Allow {
		return errors.New("intent: receipt is not an ALLOW")
	}
	if r.Tenant != action.Tenant || r.ActionHash != action.Hash() {
		return errors.New("intent: receipt was issued for a different action")
	}
	if now.Unix() >= r.ExpiresAt || now.Unix() < r.IssuedAt-5 {
		return errors.New("intent: receipt is outside its validity window")
	}
	return nil
}

// Guard sits on the executing side, in the process that holds the credentials,
// and refuses to run an action unless it carries a valid, unused receipt.
// This is the enforcement point: without it a decision is advice.
//
// Used nonces are kept in memory until their receipt would have expired, so
// the set stays small. A restart forgets them, which is why receipts are
// short-lived: a replay after a restart works only inside the remaining TTL.
type Guard struct {
	pub  ed25519.PublicKey
	now  func() time.Time
	mu   sync.Mutex
	seen map[string]int64
}

// NewGuard returns a Guard that trusts receipts signed by pub. now may be nil.
func NewGuard(pub ed25519.PublicKey, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{pub: pub, now: now, seen: map[string]int64{}}
}

// ErrNotAuthorized wraps every Guard refusal.
var ErrNotAuthorized = errors.New("intent: action not authorized")

// Run executes fn only if receipt is valid for action and has not been used.
// The nonce is spent before fn runs, so a crash inside fn does not let the
// same receipt run it twice. Failure to verify returns an error and never
// calls fn.
func (g *Guard) Run(action Action, receipt *Receipt, fn func() error) error {
	now := g.now()
	if err := receipt.Verify(g.pub, action, now); err != nil {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, err)
	}
	g.mu.Lock()
	for n, exp := range g.seen {
		if now.Unix() >= exp {
			delete(g.seen, n)
		}
	}
	if _, used := g.seen[receipt.Nonce]; used {
		g.mu.Unlock()
		return fmt.Errorf("%w: receipt already used", ErrNotAuthorized)
	}
	g.seen[receipt.Nonce] = receipt.ExpiresAt
	g.mu.Unlock()
	return fn()
}
