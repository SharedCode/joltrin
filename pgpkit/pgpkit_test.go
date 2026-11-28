package pgpkit

import (
	"strings"
	"testing"
)

func mustKey(t *testing.T) Key {
	t.Helper()
	k, err := GenerateKey("test", "pgpkit test", "test@localhost")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEncryptDecryptRoundTripUsesArmor(t *testing.T) {
	k := mustKey(t)
	ct, err := Encrypt(k.Public, "order 43: ship to Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "-----BEGIN PGP MESSAGE-----") || strings.Contains(ct, "Bob") {
		t.Fatalf("not an opaque armored message: %q", ct)
	}
	got, err := Decrypt(k.Private, ct, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Plaintext != "order 43: ship to Bob" || got.Signed {
		t.Fatalf("got %+v", got)
	}
}

func TestSignedMessageVerifiesAndNamesTheSigner(t *testing.T) {
	recipient, sender := mustKey(t), mustKey(t)
	ct, err := Encrypt(recipient.Public, "approve", sender.Private)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(recipient.Private, ct, sender.Public)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Signed || got.SignerFingerprint != sender.Fingerprint || got.Plaintext != "approve" {
		t.Fatalf("got %+v, want signed by %s", got, sender.Fingerprint)
	}
}

func TestRequiredSignatureIsEnforced(t *testing.T) {
	recipient, sender, other := mustKey(t), mustKey(t), mustKey(t)
	unsigned, _ := Encrypt(recipient.Public, "x", "")
	if _, err := Decrypt(recipient.Private, unsigned, sender.Public); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("unsigned message accepted: %v", err)
	}
	signed, _ := Encrypt(recipient.Public, "x", other.Private)
	if _, err := Decrypt(recipient.Private, signed, sender.Public); err == nil {
		t.Fatal("message signed by a different key was accepted")
	}
}

func TestWrongKeyAndGarbageFailWithoutPlaintext(t *testing.T) {
	a, b := mustKey(t), mustKey(t)
	ct, _ := Encrypt(a.Public, "secret", "")
	if got, err := Decrypt(b.Private, ct, ""); err == nil || got.Plaintext != "" {
		t.Fatalf("wrong key decrypted: %+v %v", got, err)
	}
	if _, err := Decrypt(a.Private, "not armor", ""); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := Decrypt(a.Private, a.Public, ""); err == nil {
		t.Fatal("a key block was accepted as a message")
	}
}

func TestTamperedCiphertextNeverReturnsPlaintext(t *testing.T) {
	k := mustKey(t)
	ct, _ := Encrypt(k.Public, strings.Repeat("secret ", 200), "")
	lines := strings.Split(ct, "\n")
	mid := len(lines) / 2
	line := []byte(lines[mid])
	if line[3] == 'A' {
		line[3] = 'B'
	} else {
		line[3] = 'A'
	}
	lines[mid] = string(line)
	got, err := Decrypt(k.Private, strings.Join(lines, "\n"), "")
	if err == nil || got.Plaintext != "" {
		t.Fatalf("tampered message returned %+v, err %v", got, err)
	}
}

func TestPublicKeyCannotDecryptOrSign(t *testing.T) {
	k := mustKey(t)
	ct, _ := Encrypt(k.Public, "x", "")
	if _, err := Decrypt(k.Public, ct, ""); err == nil {
		t.Fatal("public key used to decrypt")
	}
	if _, err := ClearSign(k.Public, "x"); err == nil {
		t.Fatal("public key used to sign")
	}
	if _, err := Encrypt(k.Public, "x", k.Public); err == nil {
		t.Fatal("public key used as signer")
	}
}

func TestSizeLimit(t *testing.T) {
	k := mustKey(t)
	if _, err := Encrypt(k.Public, strings.Repeat("a", MaxSize+1), ""); err == nil {
		t.Fatal("oversized plaintext accepted")
	}
	if _, err := ClearSign(k.Private, strings.Repeat("a", MaxSize+1)); err == nil {
		t.Fatal("oversized text signed")
	}
}

func TestClearSignVerifyAndRejectEdits(t *testing.T) {
	k, other := mustKey(t), mustKey(t)
	signed, err := ClearSign(k.Private, "release 5.14 approved")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed, "-----BEGIN PGP SIGNED MESSAGE-----") || !strings.Contains(signed, "release 5.14 approved") {
		t.Fatalf("not a clear-signed message: %q", signed)
	}
	text, err := VerifyClearSigned(k.Public, signed)
	if err != nil || text != "release 5.14 approved" {
		t.Fatalf("got %q, %v", text, err)
	}
	if _, err := VerifyClearSigned(other.Public, signed); err == nil {
		t.Fatal("verified against the wrong key")
	}
	if _, err := VerifyClearSigned(k.Public, strings.Replace(signed, "5.14", "9.99", 1)); err == nil {
		t.Fatal("edited text verified")
	}
	if _, err := VerifyClearSigned(k.Public, "extra words\n"+signed); err == nil {
		t.Fatal("text before the signed block was accepted")
	}
	if _, err := VerifyClearSigned(k.Public, signed+"\nextra words after\n"); err == nil {
		t.Fatal("text after the signed block was accepted")
	}
}

func TestKeyErrorsAreClear(t *testing.T) {
	if _, err := Encrypt("nope", "x", ""); err == nil || !strings.Contains(err.Error(), "recipient key") {
		t.Fatalf("got %v", err)
	}
	if _, err := ClearSign("", "x"); err == nil {
		t.Fatal("empty key accepted")
	}
}
