package governance

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

func TestMetadataTamperIsDetected(t *testing.T) {
	r := NewMemoryAuditRecorder(nil)
	_ = r.Record(context.Background(), &AuditEvent{ActorID: "a", Action: "x", Metadata: map[string]string{"ticket": "INC-1"}})
	r.events[0].Metadata["ticket"] = "INC-2"
	if err := r.VerifyIntegrity(); !errors.Is(err, ErrTamperedAuditChain) {
		t.Fatalf("want tamper error, got %v", err)
	}
}

func TestEventsWithoutMetadataKeepOldHash(t *testing.T) {
	a := &AuditEvent{ActorID: "a", Action: "x"}
	b := &AuditEvent{ActorID: "a", Action: "x", Metadata: map[string]string{}}
	if a.ComputeHash("p") != b.ComputeHash("p") {
		t.Fatal("empty metadata must not change the hash")
	}
}

func TestEd25519SignAndVerify(t *testing.T) {
	signer, pub, err := GenerateEd25519Signer()
	if err != nil {
		t.Fatal(err)
	}
	r := NewMemoryAuditRecorder(signer)
	for i := 0; i < 3; i++ {
		if err := r.Record(context.Background(), &AuditEvent{ActorID: "a", Action: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.VerifySignatures(pub); err != nil {
		t.Fatalf("clean log should verify: %v", err)
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)
	if err := r.VerifySignatures(otherPub); !errors.Is(err, ErrBadAuditSignature) {
		t.Fatalf("wrong key must fail, got %v", err)
	}
	r.events[1].Signature = nil
	if err := r.VerifySignatures(pub); !errors.Is(err, ErrBadAuditSignature) {
		t.Fatalf("stripped signature must fail, got %v", err)
	}
}

func TestRedactMasksKnownValues(t *testing.T) {
	cases := map[string]string{
		"mail jane.doe@example.com now":      "mail [email] now",
		"ssn 123-45-6789 ok":                 "ssn [ssn] ok",
		"card 4111 1111 1111 1111 paid":      "card [card] paid",
		"key AKIAIOSFODNN7EXAMPLE leaked":    "key [aws_key] leaked",
		"call 415-555-0134 today":            "call [phone] today",
		"from 10.1.2.3 blocked":              "from [ipv4] blocked",
		"order 1234567890123 shipped":        "order 1234567890123 shipped", // fails Luhn
		"ssn 000-12-3456 is not a valid SSN": "ssn 000-12-3456 is not a valid SSN",
	}
	var p PIIRedactor
	for in, want := range cases {
		if got := p.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPseudonymIsStableAndKeyed(t *testing.T) {
	p1, p2 := PIIRedactor{Key: []byte("k1")}, PIIRedactor{Key: []byte("k2")}
	a, b := p1.Redact("jane@example.com"), p1.Redact("jane@example.com")
	if a != b || strings.Contains(a, "jane") {
		t.Fatalf("pseudonym must be stable and hide the value: %q %q", a, b)
	}
	if a == p2.Redact("jane@example.com") {
		t.Fatal("different keys must give different pseudonyms")
	}
}

func TestRedactingSinkStoresOnlyRedactedText(t *testing.T) {
	signer, pub, _ := GenerateEd25519Signer()
	rec := NewMemoryAuditRecorder(signer)
	sink := &RedactingSink{Next: rec}
	ev := &AuditEvent{ActorID: "agent-7", Action: "send", Reason: "emailed bob@corp.io",
		Metadata: map[string]string{"note": "ssn 123-45-6789"}}
	if err := sink.Record(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	got := rec.Query(AuditFilter{})[0]
	if strings.Contains(got.Reason, "bob@") || strings.Contains(got.Metadata["note"], "123-45") {
		t.Fatalf("PII stored: %+v", got)
	}
	if got.ActorID != "agent-7" {
		t.Fatal("actor must be kept")
	}
	if !strings.Contains(ev.Reason, "bob@corp.io") {
		t.Fatal("caller's event must not be mutated")
	}
	if err := rec.VerifySignatures(pub); err != nil {
		t.Fatal(err)
	}
}
