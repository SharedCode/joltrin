package privateintent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5/intent"
)

type rig struct {
	t   *testing.T
	dir string
	g   *gateway
	srv *httptest.Server
	tok string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	if err := initDirQuiet(dir); err != nil {
		t.Fatal(err)
	}
	return startRig(t, dir)
}

func startRig(t *testing.T, dir string) *rig {
	t.Helper()
	g, err := newGateway(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(dir, "server", "gateway.token"))
	srv := httptest.NewServer(g.handler())
	t.Cleanup(srv.Close)
	return &rig{t: t, dir: dir, g: g, srv: srv, tok: strings.TrimSpace(string(tok))}
}

type result struct {
	Status   int
	Verdict  string
	Receipt  *intent.Receipt
	Executed bool
	Error    string
	Raw      string
}

func (r *rig) post(path, token string, body any) result {
	r.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	raw.ReadFrom(resp.Body)
	var out struct {
		Verdict  string          `json:"verdict"`
		Receipt  *intent.Receipt `json:"receipt"`
		Executed bool            `json:"executed"`
		Error    string          `json:"error"`
	}
	_ = json.Unmarshal(raw.Bytes(), &out)
	return result{resp.StatusCode, out.Verdict, out.Receipt, out.Executed, out.Error, raw.String()}
}

func (r *rig) authorize(key, payload string) result {
	return r.post("/v1/authorize", r.tok, map[string]any{"key": key, "payload": payload})
}

func (r *rig) execute(key, payload string, rc *intent.Receipt) result {
	return r.post("/v1/execute", r.tok, map[string]any{"key": key, "payload": payload, "receipt": rc})
}

func (r *rig) approve(key, payload string) {
	r.t.Helper()
	if _, err := approveRecord(r.dir, key, payload); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) records() []string {
	m, _ := filepath.Glob(filepath.Join(r.dir, "records", "*.txt"))
	return m
}

func (r *rig) approved(key, payload string) *intent.Receipt {
	r.t.Helper()
	r.approve(key, payload)
	a := r.authorize(key, payload)
	if a.Verdict != "ALLOW" || a.Receipt == nil {
		r.t.Fatalf("want ALLOW with a receipt, got %s", a.Raw)
	}
	return a.Receipt
}

func TestApprovedActionRunsOnce(t *testing.T) {
	r := newRig(t)
	if a := r.authorize("order-1", "hello"); a.Verdict != "REQUIRE_APPROVAL" || a.Receipt != nil {
		t.Fatalf("no approval yet must not allow: %s", a.Raw)
	}
	if got := r.execute("order-1", "hello", &intent.Receipt{}); got.Status != http.StatusForbidden || len(r.records()) != 0 {
		t.Fatalf("an empty receipt must not write: %s", got.Raw)
	}
	rc := r.approved("order-1", "hello")
	got := r.execute("order-1", "hello", rc)
	if got.Status != http.StatusOK || !got.Executed {
		t.Fatalf("approved action must run: %s", got.Raw)
	}
	b, err := os.ReadFile(filepath.Join(r.dir, "records", "order-1.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatalf("record not written as approved: %q %v", b, err)
	}
	if again := r.execute("order-1", "hello", rc); again.Status != http.StatusForbidden || !strings.Contains(again.Error, "already used") {
		t.Fatalf("replayed receipt must be refused: %s", again.Raw)
	}
	if a := r.authorize("order-1", "hello"); a.Verdict != "REQUIRE_APPROVAL" {
		t.Fatalf("the approval is single use: %s", a.Raw)
	}
}

func TestModifiedRequestsAreRefusedBeforeAnySideEffect(t *testing.T) {
	r := newRig(t)
	rc := r.approved("order-2", "approved text")

	cases := map[string]result{
		"different payload": r.execute("order-2", "approved text, plus more", rc),
		"different key":     r.execute("order-3", "approved text", rc),
	}
	forged := *rc
	forged.Nonce = "0000"
	cases["edited receipt"] = r.execute("order-2", "approved text", &forged)
	other := *rc
	other.PolicyDigest = strings.Repeat("0", 64)
	cases["edited policy digest"] = r.execute("order-2", "approved text", &other)

	for name, got := range cases {
		if got.Status != http.StatusForbidden || got.Executed {
			t.Errorf("%s: want 403, got %s", name, got.Raw)
		}
	}
	if len(r.records()) != 0 {
		t.Fatalf("a refused request wrote %v", r.records())
	}
	// The untouched receipt still works, so the refusals above did not spend it.
	if got := r.execute("order-2", "approved text", rc); got.Status != http.StatusOK {
		t.Fatalf("original receipt should still run: %s", got.Raw)
	}
}

func TestApprovalForAnotherPayloadDoesNotCover(t *testing.T) {
	r := newRig(t)
	r.approve("order-4", "payload A")
	if a := r.authorize("order-4", "payload B"); a.Verdict != "REQUIRE_APPROVAL" {
		t.Fatalf("approval for A must not allow B: %s", a.Raw)
	}
	if a := r.authorize("order-5", "payload A"); a.Verdict != "REQUIRE_APPROVAL" {
		t.Fatalf("approval for key order-4 must not allow order-5: %s", a.Raw)
	}
}

func TestExpiredApprovalAndExpiredReceiptAreRefused(t *testing.T) {
	r := newRig(t)
	k, _ := readKey(filepath.Join(r.dir, "keys", "owner.key"))
	old := intent.SignApproval(k, recordAction("order-6", "x"), recordRole, time.Now().Add(-time.Hour), 10*time.Minute)
	if _, err := drop(r.dir, intent.Envelope{Type: "approval", Approval: &old}, old.ID); err != nil {
		t.Fatal(err)
	}
	if a := r.authorize("order-6", "x"); a.Verdict == "ALLOW" {
		t.Fatalf("an expired approval must not allow: %s", a.Raw)
	}

	rc := r.approved("order-7", "y")
	r.g.app.guard = intent.NewGuard(r.g.app.eng.ReceiptKey(), func() time.Time { return time.Now().Add(time.Hour) })
	if got := r.execute("order-7", "y", rc); got.Status != http.StatusForbidden || len(r.records()) != 0 {
		t.Fatalf("an expired receipt must not run: %s", got.Raw)
	}
}

func TestConcurrentDuplicatesHappenAtMostOnce(t *testing.T) {
	r := newRig(t)

	// One approval, many simultaneous authorize calls: one ALLOW.
	r.approve("order-8", "z")
	var allowed atomic.Int32
	var receipt atomic.Pointer[intent.Receipt]
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a := r.authorize("order-8", "z"); a.Verdict == "ALLOW" {
				allowed.Add(1)
				receipt.Store(a.Receipt)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("one approval must authorize exactly once, got %d", allowed.Load())
	}

	// One receipt, many simultaneous execute calls: one write.
	var ok atomic.Int32
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := r.execute("order-8", "z", receipt.Load()); got.Status == http.StatusOK {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || len(r.records()) != 1 {
		t.Fatalf("receipt must run once, ran %d times, %d records", ok.Load(), len(r.records()))
	}
}

func TestGatewayRejectsBadCallers(t *testing.T) {
	r := newRig(t)
	for name, tok := range map[string]string{"no token": "", "wrong token": "nope"} {
		if got := r.post("/v1/authorize", tok, map[string]any{"key": "a", "payload": "b"}); got.Status != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", name, got.Status)
		}
	}
	for _, key := range []string{"../etc/passwd", "A", "", "a/b", strings.Repeat("a", 65)} {
		if got := r.authorize(key, "b"); got.Status != http.StatusBadRequest {
			t.Errorf("key %q: want 400, got %d", key, got.Status)
		}
	}
	if got := r.post("/v1/authorize", r.tok, map[string]any{"key": "a", "payload": "b", "tool": "db.delete"}); got.Status != http.StatusBadRequest {
		t.Errorf("an unknown field must be refused, got %d", got.Status)
	}
	if got := r.authorize("a", strings.Repeat("x", maxPayloadSize+1)); got.Status != http.StatusBadRequest {
		t.Errorf("an oversize payload must be refused, got %d", got.Status)
	}
}

func TestExistingRecordIsNotOverwritten(t *testing.T) {
	r := newRig(t)
	if got := r.execute("dup", "first", r.approved("dup", "first")); got.Status != http.StatusOK {
		t.Fatal(got.Raw)
	}
	got := r.execute("dup", "second", r.approved("dup", "second"))
	if got.Status != http.StatusInternalServerError || !strings.Contains(got.Raw, "already exists") || !strings.Contains(got.Raw, "spent") {
		t.Fatalf("a failing write must say the receipt is spent: %s", got.Raw)
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "records", "dup.txt")); string(b) != "first" {
		t.Fatalf("existing record was changed: %q", b)
	}
}

// ---- OpenPGP ----------------------------------------------------------------

const secretText = "card 4111-1111-1111-1111 and a session cookie"

func newPGPRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	if err := initDirQuiet(dir); err != nil {
		t.Fatal(err)
	}
	if err := pgpInit(dir); err != nil {
		t.Fatal(err)
	}
	return startRig(t, dir)
}

func encryptFor(t *testing.T, dir, plain string) string {
	t.Helper()
	_, pub := pgpPaths(dir)
	pk, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := pgpEncrypt(string(pk), plain)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func TestOpenPGPPayloadIsOpaqueToTheCourierAndDecryptedOnlyAfterTheReceipt(t *testing.T) {
	r := newPGPRig(t)
	ct := encryptFor(t, r.dir, secretText)
	if strings.Contains(ct, "4111") {
		t.Fatal("ciphertext leaks the plaintext")
	}
	rc := r.approved("pay-1", ct)
	got := r.execute("pay-1", ct, rc)
	if got.Status != http.StatusOK {
		t.Fatalf("approved ciphertext must run: %s", got.Raw)
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "records", "pay-1.txt")); string(b) != secretText {
		t.Fatalf("record should hold the decrypted text, got %q", b)
	}
	// Everything n8n sees in a response is free of the plaintext.
	if strings.Contains(got.Raw, "4111") {
		t.Fatalf("response leaked plaintext: %s", got.Raw)
	}
}

func TestOpenPGPSubstitutionAndTamperingFailBeforeDecryption(t *testing.T) {
	r := newPGPRig(t)
	ct := encryptFor(t, r.dir, secretText)
	rc := r.approved("pay-2", ct)

	// Same plaintext, encrypted again: a different message, so a different hash.
	if got := r.execute("pay-2", encryptFor(t, r.dir, secretText), rc); got.Status != http.StatusForbidden {
		t.Errorf("a re-encrypted copy must not ride on the approval: %s", got.Raw)
	}
	// A different secret to the same key.
	if got := r.execute("pay-2", encryptFor(t, r.dir, "something else"), rc); got.Status != http.StatusForbidden {
		t.Errorf("a substituted payload must be refused: %s", got.Raw)
	}
	// One flipped character in the armored body.
	i := strings.Index(ct, "\n\n") + 10
	flipped := ct[:i] + string(rune(ct[i]^1)) + ct[i+1:]
	if got := r.execute("pay-2", flipped, rc); got.Status != http.StatusForbidden {
		t.Errorf("a tampered message must be refused: %s", got.Raw)
	}
	if len(r.records()) != 0 {
		t.Fatalf("a refused request wrote %v", r.records())
	}
	if got := r.execute("pay-2", ct, rc); got.Status != http.StatusOK {
		t.Fatalf("the approved message should still run: %s", got.Raw)
	}
}

func TestOpenPGPWrongKeyFailsWithoutWriting(t *testing.T) {
	r := newPGPRig(t)
	other := t.TempDir()
	if err := pgpInit(other); err != nil {
		t.Fatal(err)
	}
	ct := encryptFor(t, other, secretText) // encrypted to a key this gateway does not hold
	rc := r.approved("pay-3", ct)
	got := r.execute("pay-3", ct, rc)
	if got.Status != http.StatusInternalServerError || !strings.Contains(got.Raw, "not encrypted to this gateway") {
		t.Fatalf("a wrong key must fail clearly: %s", got.Raw)
	}
	if len(r.records()) != 0 {
		t.Fatalf("a failed decrypt wrote %v", r.records())
	}
}

func TestOpenPGPWithoutAKeyIsRefusedNotStored(t *testing.T) {
	r := newRig(t) // no pgp-init
	ct := encryptFor(t, func() string {
		d := t.TempDir()
		if err := pgpInit(d); err != nil {
			t.Fatal(err)
		}
		return d
	}(), secretText)
	got := r.execute("pay-4", ct, r.approved("pay-4", ct))
	if got.Status != http.StatusInternalServerError || !strings.Contains(got.Raw, "no private key") || len(r.records()) != 0 {
		t.Fatalf("ciphertext must never be stored as if it were the record: %s", got.Raw)
	}
}

// ---- Taint Barrier ----------------------------------------------------------

const liveKey = "acme-prod-payments-9f8e7d6c5b4a39281706"

func withTaint(t *testing.T, dir string) {
	t.Helper()
	cfg := `{"policy":{"version":1,"labels":{"prod-secret":{"allow_hosts":["vault.internal"]}}},
	  "values":[{"label":"prod-secret","name":"payments-key","value":"` + liveKey + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, "server", "taint.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBarrierStopsALabeledSecretEvenWhenApprovedAndEncrypted(t *testing.T) {
	dir := t.TempDir()
	if err := initDirQuiet(dir); err != nil {
		t.Fatal(err)
	}
	if err := pgpInit(dir); err != nil {
		t.Fatal(err)
	}
	withTaint(t, dir)
	r := startRig(t, dir)

	// Plaintext: refused at authorize, before it can spend an approval.
	r.approve("leak-1", "key is "+liveKey)
	if a := r.authorize("leak-1", "key is "+liveKey); a.Verdict != "BLOCK" || strings.Contains(a.Raw, liveKey) {
		t.Fatalf("a plaintext secret must be blocked without echoing it: %s", a.Raw)
	}

	// Encrypted: the courier cannot see it, so the gateway screens after decrypting.
	ct := encryptFor(t, dir, "key is "+liveKey)
	rc := r.approved("leak-2", ct)
	got := r.execute("leak-2", ct, rc)
	if got.Status != http.StatusForbidden || !strings.Contains(got.Raw, "taint barrier") || strings.Contains(got.Raw, liveKey) {
		t.Fatalf("an approved, encrypted secret must still be blocked: %s", got.Raw)
	}
	if len(r.records()) != 0 {
		t.Fatalf("a blocked record was written: %v", r.records())
	}

	// A clean message goes through the same path.
	clean := encryptFor(t, dir, "order 42 shipped")
	if got := r.execute("ok-1", clean, r.approved("ok-1", clean)); got.Status != http.StatusOK {
		t.Fatalf("a clean encrypted record must run: %s", got.Raw)
	}
}
