package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5/translog"
)

// A receipt log lets an auditor who never sees the action check that a receipt
// was recorded, was signed by the engine, and was not later rewritten.
func TestReceiptsInTransparencyLog(t *testing.T) {
	f := newFixture(t)
	logPub, logPriv, _ := ed25519.GenerateKey(rand.Reader)
	log := translog.New()
	const origin = "joltrin/receipts/acme"

	var leaves [][]byte
	for i := 0; i < 5; i++ {
		f.attest("prod/orders", time.Hour)
		f.approve(10 * time.Minute)
		d, r := f.eng.Authorize(f.action)
		if d.Verdict != Allow || r == nil {
			t.Fatalf("round %d: %s", i, d.Verdict)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		leaves = append(leaves, raw)
		if idx := log.Append(raw); int(idx) != i {
			t.Fatalf("index %d, want %d", idx, i)
		}
	}
	// An auditor pinned the head after two receipts.
	oldHead := mustHeadAt(t, leaves[:2], origin, logPriv)
	head, err := log.SignHead(origin, logPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := head.VerifyHead(origin, logPub); err != nil {
		t.Fatal(err)
	}

	// Inclusion of receipt 3, checked with only its bytes, the proof and the head.
	proof, _ := log.InclusionProof(3, head.Size)
	if err := translog.VerifyInclusion(translog.LeafHash(leaves[3]), 3, head.Size, proof, head.Root); err != nil {
		t.Fatal(err)
	}
	var got Receipt
	if err := json.Unmarshal(leaves[3], &got); err != nil {
		t.Fatal(err)
	}
	if err := got.VerifySignature(f.eng.ReceiptKey()); err != nil {
		t.Fatal(err)
	}

	// The log only grew since the pinned head.
	cons, _ := log.ConsistencyProof(oldHead.Size, head.Size)
	if err := translog.VerifyConsistency(oldHead.Size, head.Size, oldHead.Root, head.Root, cons); err != nil {
		t.Fatal(err)
	}

	// A receipt edited after the fact no longer matches the log.
	got.Nonce = "edited"
	edited, _ := json.Marshal(got)
	if err := translog.VerifyInclusion(translog.LeafHash(edited), 3, head.Size, proof, head.Root); err == nil {
		t.Fatal("edited receipt verified against the log")
	}
}

func mustHeadAt(t *testing.T, leaves [][]byte, origin string, priv ed25519.PrivateKey) translog.TreeHead {
	t.Helper()
	l := translog.New()
	for _, b := range leaves {
		l.Append(b)
	}
	h, err := l.SignHead(origin, priv)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
