package governance

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
)

// ErrBadAuditSignature is returned when an event signature does not verify.
var ErrBadAuditSignature = errors.New("governance: audit signature invalid")

// Ed25519Signer signs audit event hashes with an Ed25519 private key.
// Keep the private key in your own secret store. Publish only the public key.
type Ed25519Signer struct {
	priv ed25519.PrivateKey
}

// NewEd25519Signer wraps an existing private key.
func NewEd25519Signer(priv ed25519.PrivateKey) (*Ed25519Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("governance: ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	return &Ed25519Signer{priv: priv}, nil
}

// GenerateEd25519Signer creates a new key pair and returns the signer and the public key.
func GenerateEd25519Signer() (*Ed25519Signer, ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return &Ed25519Signer{priv: priv}, pub, nil
}

func (s *Ed25519Signer) Sign(digest []byte) ([]byte, error) { return ed25519.Sign(s.priv, digest), nil }
func (s *Ed25519Signer) Algorithm() string                  { return "ed25519" }

// VerifySignatures checks the hash chain and then every event signature against pub.
// An unsigned event is a failure, so removing signatures cannot pass as a clean log.
func (r *MemoryAuditRecorder) VerifySignatures(pub ed25519.PublicKey) error {
	if err := r.VerifyIntegrity(); err != nil {
		return err
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("governance: ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for idx, ev := range r.events {
		if !ed25519.Verify(pub, []byte(ev.Hash), ev.Signature) {
			return fmt.Errorf("%w: event index %d (ID %s)", ErrBadAuditSignature, idx, ev.ID)
		}
	}
	return nil
}
