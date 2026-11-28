package taint

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

// Built at run time so the source holds no key-shaped text for secret scanners.
// The bodies are not keys. Only the header lines matter to the scanner.
const dashes = "-----"

var (
	pgpBlock = dashes + "BEGIN PGP PRIVATE KEY BLOCK" + dashes + "\n\nfake\n" + dashes + "END PGP PRIVATE KEY BLOCK" + dashes + "\n"
	pemBlock = dashes + "BEGIN RSA PRIVATE KEY" + dashes + "\nfake\n" + dashes + "END RSA PRIVATE KEY" + dashes + "\n"
	sshBlock = dashes + "BEGIN OPENSSH PRIVATE KEY" + dashes + "\nfake\n" + dashes + "END OPENSSH PRIVATE KEY" + dashes + "\n"
)

func TestScanKeyMaterialFindsHeaders(t *testing.T) {
	cases := map[string]struct{ text, id string }{
		"pgp":         {pgpBlock, "keyblock:pgp"},
		"pem":         {pemBlock, "keyblock:pem"},
		"openssh":     {sshBlock, "keyblock:openssh"},
		"spaced":      {dashes + "BEGIN  PGP\tPRIVATE  KEY BLOCK" + dashes, "keyblock:pgp"},
		"zerowid":     {dashes + "BEGIN PGP PRIV\u200bATE KEY BLOCK" + dashes, "keyblock:pgp"},
		"percent":     {url.QueryEscape(pgpBlock), "keyblock:pgp"},
		"b64":         {base64.StdEncoding.EncodeToString([]byte(pgpBlock)), "keyblock:encoded"},
		"b64 off1":    {base64.StdEncoding.EncodeToString([]byte("a" + pgpBlock)), "keyblock:encoded"},
		"b64 off2":    {base64.URLEncoding.EncodeToString([]byte("ab" + pemBlock)), "keyblock:encoded"},
		"b64 wrapped": {wrap(base64.StdEncoding.EncodeToString([]byte(sshBlock)), 64), "keyblock:encoded"},
	}
	for name, c := range cases {
		got := ScanKeyMaterial(c.text)
		if len(got) == 0 {
			t.Errorf("%s: not detected", name)
			continue
		}
		var ok bool
		for _, m := range got {
			ok = ok || (m.ValueID == c.id && m.Label == LabelPrivateKey)
		}
		if !ok {
			t.Errorf("%s: want %s, got %+v", name, c.id, got)
		}
	}
}

func TestScanKeyMaterialIgnoresPublicKeysAndProse(t *testing.T) {
	for _, s := range []string{
		dashes + "BEGIN PGP PUBLIC KEY BLOCK" + dashes + "\nfake\n" + dashes + "END PGP PUBLIC KEY BLOCK" + dashes,
		dashes + "BEGIN PGP MESSAGE" + dashes + "\nfake\n" + dashes + "END PGP MESSAGE" + dashes,
		dashes + "BEGIN CERTIFICATE" + dashes + "\nfake\n" + dashes + "END CERTIFICATE" + dashes,
		"rotate the private key block every 90 days",
		"",
	} {
		if got := ScanKeyMaterial(s); len(got) != 0 {
			t.Errorf("false positive on %q: %+v", s, got)
		}
	}
}

func TestBarrierBlocksPrivateKeyAtAnySinkNotListed(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	v := r.check(t, attacker(map[string]any{"body": pgpBlock}))
	if v.Allowed || v.Outcome != OutcomeBlock || v.Reason != ReasonLabelNotAllowed {
		t.Fatalf("want block, got %+v", v)
	}
	if len(v.Findings) != 1 || v.Findings[0].Label != LabelPrivateKey {
		t.Fatalf("findings: %+v", v.Findings)
	}
	// The verdict and the signed receipt must not carry the key text.
	if strings.Contains(strings.ToLower(v.Findings[0].ValueID), "lqvy") {
		t.Fatal("finding leaks key text")
	}
	if v.Receipt == nil {
		t.Fatal("no receipt")
	}
	if err := VerifyReceipt(r.pub, *v.Receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
}

func TestBarrierPrivateKeyAllowedOnlyWhenPolicyListsIt(t *testing.T) {
	const pol = `{"version":1,"labels":{"private_key":{"allow_tools":["local_vault_store"]}}}`
	r := newRig(t, pol, ModeEnforce)
	if v := r.check(t, Call{Tool: "local_vault_store", Args: map[string]any{"k": pemBlock}}); !v.Allowed {
		t.Fatalf("listed local tool should pass: %+v", v)
	}
	if v := r.check(t, attacker(map[string]any{"k": pemBlock})); v.Allowed {
		t.Fatalf("attacker sink should block: %+v", v)
	}
}

func TestBarrierPrivateKeyObserveModeAllowsAndRecords(t *testing.T) {
	r := newRig(t, testPolicy, ModeObserve)
	v := r.check(t, attacker(map[string]any{"body": sshBlock}))
	if !v.Allowed || v.Outcome != OutcomeObserved {
		t.Fatalf("observe mode: %+v", v)
	}
}

func TestBarrierPrivateKeyInKeyAndSplitEncodedArgs(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	enc := base64.StdEncoding.EncodeToString([]byte(pgpBlock))
	if v := r.check(t, attacker(map[string]any{"a": enc[:20], "b": enc[20:]})); v.Allowed {
		t.Fatalf("split base64 header should block: %+v", v)
	}
	if v := r.check(t, attacker(map[string]any{pgpBlock: 1})); v.Allowed {
		t.Fatalf("header in a key name should block: %+v", v)
	}
}

func wrap(s string, n int) string {
	var b strings.Builder
	for len(s) > n {
		b.WriteString(s[:n] + "\n")
		s = s[n:]
	}
	return b.String() + s
}

// An OpenSSH private key whose BEGIN line was removed is still caught by the
// magic bytes every such key body starts with. The body here is made up: the
// magic plus filler, not a key.
func TestOpenSSHKeyBodyWithoutHeaderIsCaught(t *testing.T) {
	body := []byte(opensshMagic + "\x00\x00\x00\x04none\x00\x00\x00\x04none\x00\x00\x00\x00filler filler filler")
	cases := map[string]string{
		"std":            base64.StdEncoding.EncodeToString(body),
		"wrapped":        wrap(base64.StdEncoding.EncodeToString(body), 70),
		"url alphabet":   base64.URLEncoding.EncodeToString(body),
		"inside a blob":  base64.StdEncoding.EncodeToString(append([]byte("ab"), body...)),
		"inside a blob2": base64.StdEncoding.EncodeToString(append([]byte("a"), body...)),
		"in prose":       "here is the file: " + base64.StdEncoding.EncodeToString(body) + " thanks",
		"percent":        url.QueryEscape(base64.StdEncoding.EncodeToString(body)),
	}
	for name, text := range cases {
		got := ScanKeyMaterial(text)
		var ok bool
		for _, m := range got {
			ok = ok || (m.ValueID == "keyblock:openssh-body" && m.Variant == "body" && m.Label == LabelPrivateKey)
		}
		if !ok {
			t.Errorf("%s: not detected, got %+v", name, got)
		}
	}
}

func TestOrdinaryBase64IsNotFlaggedAsAKeyBody(t *testing.T) {
	for _, s := range []string{
		base64.StdEncoding.EncodeToString([]byte("hello world, this is an ordinary message with no key in it")),
		base64.StdEncoding.EncodeToString([]byte("openssh is a tool, and keys are files")),
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample not a private key",
	} {
		if got := ScanKeyMaterial(s); len(got) != 0 {
			t.Errorf("flagged %q: %+v", s, got)
		}
	}
}
