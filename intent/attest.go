package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// KeyID names a public key: the first 16 hex characters of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// GenerateKey makes an Ed25519 key pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// Evidence is a signed statement from a system the agent does not control
// that a state holds for a resource, such as the backup system saying
// "backup_validated" for "prod/orders". The agent cannot mint it: the engine
// accepts it only from an issuer key it trusts for that state.
type Evidence struct {
	ID        string `json:"id"`
	Tenant    string `json:"tenant"`
	State     string `json:"state"`
	Resource  string `json:"resource"`
	Issuer    string `json:"issuer"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	Sig       []byte `json:"sig"`
}

// Approval is a person's signed yes to one exact action. It is bound to the
// action hash, so approving a delete of one database cannot be reused for
// another, and it is used up by the first ALLOW it supports.
type Approval struct {
	ID         string `json:"id"`
	Tenant     string `json:"tenant"`
	ActionHash string `json:"action_hash"`
	Role       string `json:"role"`
	Approver   string `json:"approver"`
	IssuedAt   int64  `json:"issued_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Sig        []byte `json:"sig"`
}

// Revocation withdraws an evidence or approval id. It must be signed by the
// vault owner's key or by the key that signed the target.
type Revocation struct {
	Tenant   string `json:"tenant"`
	TargetID string `json:"target_id"`
	Signer   string `json:"signer"`
	IssuedAt int64  `json:"issued_at"`
	Sig      []byte `json:"sig"`
}

func signedBytes(domain string, v any) []byte {
	b, _ := json.Marshal(v)
	return append([]byte("joltrin-intent/"+domain+"/v1\n"), b...)
}

// SignEvidence signs e with the issuer's key and fills Issuer and Sig.
func SignEvidence(priv ed25519.PrivateKey, e Evidence, now time.Time, ttl time.Duration) Evidence {
	e.Issuer = KeyID(priv.Public().(ed25519.PublicKey))
	if e.ID == "" {
		e.ID = newID("ev")
	}
	e.IssuedAt, e.ExpiresAt, e.Sig = now.Unix(), now.Add(ttl).Unix(), nil
	e.Sig = ed25519.Sign(priv, signedBytes("evidence", e))
	return e
}

func (e Evidence) verify(pub ed25519.PublicKey) bool {
	sig := e.Sig
	e.Sig = nil
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, signedBytes("evidence", e), sig)
}

// SignApproval signs an approval of action for role.
func SignApproval(priv ed25519.PrivateKey, action Action, role string, now time.Time, ttl time.Duration) Approval {
	a := Approval{
		ID: newID("ap"), Tenant: action.Tenant, ActionHash: action.Hash(), Role: role,
		Approver: KeyID(priv.Public().(ed25519.PublicKey)),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	}
	a.Sig = ed25519.Sign(priv, signedBytes("approval", a))
	return a
}

func (a Approval) verify(pub ed25519.PublicKey) bool {
	sig := a.Sig
	a.Sig = nil
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, signedBytes("approval", a), sig)
}

// SignRevocation signs a revocation of targetID.
func SignRevocation(priv ed25519.PrivateKey, tenant, targetID string, now time.Time) Revocation {
	r := Revocation{Tenant: tenant, TargetID: targetID, Signer: KeyID(priv.Public().(ed25519.PublicKey)), IssuedAt: now.Unix()}
	r.Sig = ed25519.Sign(priv, signedBytes("revocation", r))
	return r
}

func (r Revocation) verify(pub ed25519.PublicKey) bool {
	sig := r.Sig
	r.Sig = nil
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, signedBytes("revocation", r), sig)
}

// Envelope carries one signed artifact as JSON, so a file, a queue message or
// an HTTP body can hand it to Engine.Ingest.
type Envelope struct {
	Type       string      `json:"type"` // "evidence", "approval" or "revocation"
	Evidence   *Evidence   `json:"evidence,omitempty"`
	Approval   *Approval   `json:"approval,omitempty"`
	Revocation *Revocation `json:"revocation,omitempty"`
}

// Trust lists the keys an engine accepts. A key is trusted for specific
// things only: an issuer for named states, an approver for named roles.
type Trust struct {
	// Owner may revoke any artifact.
	Owner ed25519.PublicKey
	// Issuers maps a key id to the key and the states it may attest.
	Issuers map[string]TrustedKey
	// Approvers maps a key id to the key and the roles it may approve as.
	Approvers map[string]TrustedKey
}

// TrustedKey is a public key and what it is trusted for.
type TrustedKey struct {
	Key   ed25519.PublicKey
	Scope []string
}

func (t TrustedKey) allows(s string) bool {
	for _, x := range t.Scope {
		if x == s {
			return true
		}
	}
	return false
}

var errUntrusted = errors.New("intent: signer is not trusted for this")

func (t *Trust) checkEvidence(e Evidence) error {
	k, ok := t.Issuers[e.Issuer]
	if !ok || !k.allows(e.State) || !e.verify(k.Key) {
		return fmt.Errorf("%w (evidence %q)", errUntrusted, e.ID)
	}
	return nil
}

func (t *Trust) checkApproval(a Approval) error {
	k, ok := t.Approvers[a.Approver]
	if !ok || !k.allows(a.Role) || !a.verify(k.Key) {
		return fmt.Errorf("%w (approval %q)", errUntrusted, a.ID)
	}
	return nil
}
