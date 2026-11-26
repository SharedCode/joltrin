package privateintent

// The n8n gateway: a small HTTP front for the same intent engine and Guard the
// MCP example uses. n8n (or any workflow tool) is treated as an untrusted
// courier. It can ask for a decision and carry a receipt, but it cannot sign
// anything, and the side effect only runs inside Guard.Run on this server.
//
//	POST /v1/authorize  {"key","payload"}            -> decision, and a receipt on ALLOW
//	POST /v1/execute    {"key","payload","receipt"}  -> writes records/<key>.txt, once
//
// The action is rebuilt here from key and payload. Nothing in the request can
// name a different tool, resource or environment. The payload is bound into the
// action as its SHA-256, so an approval covers exactly one payload.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/sharedcode/joltrin/v5/intent"
	"github.com/sharedcode/joltrin/v5/taint"
)

const (
	n8nAgent       = "n8n"
	recordTool     = "record.write"
	recordRole     = "record_owner"
	maxPayloadSize = 16 << 10
	maxBodySize    = 64 << 10
	pgpHeader      = "-----BEGIN PGP MESSAGE-----"
)

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// recordAction is the one action the gateway can authorize. Payload is hashed
// as sent (plaintext, or the armored OpenPGP message), never stored in the action.
func recordAction(key, payload string) intent.Action {
	sum := sha256.Sum256([]byte(payload))
	return intent.Action{
		Tenant: tenant, Agent: n8nAgent, Tool: recordTool, Resource: "fixture/" + key, Env: "test",
		Params: map[string]string{"key": key, "payload_sha256": hex.EncodeToString(sum[:])},
	}
}

func recordRule() intent.Rule {
	return intent.Rule{
		ID: "fixture-record-write", Label: "Write a test record", Owner: "owner",
		Preference: "A workflow may write a test record only after the record owner approves that exact payload.",
		Purpose:    "low-risk demo of an approved n8n action",
		Mode:       intent.Grant, Agent: n8nAgent, Tool: recordTool, Resource: "fixture/*", Env: "test",
		Requires: []intent.Requirement{
			{Kind: intent.NeedApproval, Role: recordRole, Describe: "record owner approval"},
		},
	}
}

// approveRecord signs the record owner's approval for this exact key and payload.
func approveRecord(dir, key, payload string) (string, error) {
	k, err := readKey(filepath.Join(dir, "keys", "owner.key"))
	if err != nil {
		return "", err
	}
	ap := intent.SignApproval(k, recordAction(key, payload), recordRole, time.Now(), 10*time.Minute)
	return drop(dir, intent.Envelope{Type: "approval", Approval: &ap}, ap.ID)
}

// ---- OpenPGP ----------------------------------------------------------------

func pgpPaths(dir string) (priv, pub string) {
	return filepath.Join(dir, "server", "pgp.key.asc"), filepath.Join(dir, "keys", "pgp.pub.asc")
}

// pgpInit makes the gateway's OpenPGP key. The private half stays in server/.
// Only the public half in keys/ is meant to leave this machine.
func pgpInit(dir string) error {
	e, err := openpgp.NewEntity("joltrin-gateway", "private intent record key", "gateway@localhost", &packet.Config{DefaultCipher: packet.CipherAES256, Algorithm: packet.PubKeyAlgoEdDSA, Curve: packet.Curve25519})
	if err != nil {
		return err
	}
	priv, pub := pgpPaths(dir)
	var pb, qb bytes.Buffer
	w, err := armor.Encode(&pb, openpgp.PrivateKeyType, nil)
	if err != nil {
		return err
	}
	if err := e.SerializePrivate(w, nil); err != nil {
		return err
	}
	w.Close()
	w, err = armor.Encode(&qb, openpgp.PublicKeyType, nil)
	if err != nil {
		return err
	}
	if err := e.Serialize(w); err != nil {
		return err
	}
	w.Close()
	if err := os.MkdirAll(filepath.Dir(priv), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pub), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(priv, pb.Bytes(), 0o600); err != nil {
		return err
	}
	return os.WriteFile(pub, qb.Bytes(), 0o644)
}

func loadPGPPrivate(dir string) (openpgp.EntityList, error) {
	priv, _ := pgpPaths(dir)
	f, err := os.Open(priv)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // OpenPGP is optional
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return openpgp.ReadArmoredKeyRing(f)
}

// pgpEncrypt encrypts plaintext to the public key in pubArmored. It is what a
// trusted sender (a form, a service) runs before handing the message to n8n.
func pgpEncrypt(pubArmored, plaintext string) (string, error) {
	to, err := openpgp.ReadArmoredKeyRing(strings.NewReader(pubArmored))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil {
		return "", err
	}
	w, err := openpgp.Encrypt(aw, to, nil, nil, &packet.Config{DefaultCipher: packet.CipherAES256})
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// pgpDecrypt reads the whole body before returning, so a message that fails its
// integrity check is an error and never a partial plaintext.
func pgpDecrypt(ring openpgp.EntityList, armored string) (string, error) {
	if len(ring) == 0 {
		return "", errors.New("payload is OpenPGP encrypted but this gateway has no private key (run pgp-init)")
	}
	blk, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return "", errors.New("payload is not valid OpenPGP armor")
	}
	md, err := openpgp.ReadMessage(blk.Body, ring, nil, &packet.Config{DefaultCipher: packet.CipherAES256})
	if err != nil {
		return "", errors.New("payload was not encrypted to this gateway's key")
	}
	b, err := io.ReadAll(io.LimitReader(md.UnverifiedBody, maxPayloadSize+1))
	if err != nil {
		return "", errors.New("payload failed its integrity check")
	}
	if len(b) > maxPayloadSize {
		return "", errors.New("decrypted payload is too large")
	}
	return string(b), nil
}

// ---- the gateway ------------------------------------------------------------

type gateway struct {
	app     *app
	mu      sync.Mutex // app.pull and the engine's inbox bookkeeping are not safe to call concurrently
	token   []byte
	pgp     openpgp.EntityList
	barrier *taint.Barrier // optional: nil when server/taint.json does not exist
	records string
}

// errTaint marks a payload the Taint Barrier refused to let into a record.
var errTaint = errors.New("taint barrier blocked the write")

// taintFile is server/taint.json: which labeled secrets to watch for and where
// each label may go. The values stay on the server and are never returned.
type taintFile struct {
	Policy json.RawMessage `json:"policy"`
	Values []struct {
		Label string `json:"label"`
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"values"`
}

func loadBarrier(dir string) (*taint.Barrier, error) {
	b, err := os.ReadFile(filepath.Join(dir, "server", "taint.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tf taintFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return nil, fmt.Errorf("server/taint.json: %w", err)
	}
	pol, err := taint.ParsePolicy(tf.Policy)
	if err != nil {
		return nil, err
	}
	reg, err := taint.NewRegistry()
	if err != nil {
		return nil, err
	}
	for _, v := range tf.Values {
		if _, err := reg.Register(v.Label, v.Name, v.Value); err != nil {
			return nil, fmt.Errorf("server/taint.json value %q: %w", v.Name, err)
		}
	}
	return taint.New(taint.Config{Registry: reg, Policy: pol})
}

// screen asks the barrier whether text may be written into the record for key.
func (g *gateway) screen(key, text string) error {
	if g.barrier == nil {
		return nil
	}
	dest, err := filepath.Abs(filepath.Join(g.records, key+".txt"))
	if err != nil {
		return err
	}
	v, err := g.barrier.Check(context.Background(), taint.Call{Tool: recordTool, Destination: dest, Args: map[string]any{"record": text}, Actor: n8nAgent})
	if err != nil || !v.Allowed {
		return fmt.Errorf("%w (%s)", errTaint, v.Reason)
	}
	return nil
}

func newGateway(dir string) (*gateway, error) {
	a, err := loadEngine(dir, nil)
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(filepath.Join(dir, "server", "gateway.token"))
	if err != nil {
		return nil, fmt.Errorf("no gateway token (run init again): %w", err)
	}
	ring, err := loadPGPPrivate(dir)
	if err != nil {
		return nil, err
	}
	rec := filepath.Join(dir, "records")
	if err := os.MkdirAll(rec, 0o700); err != nil {
		return nil, err
	}
	bar, err := loadBarrier(dir)
	if err != nil {
		return nil, err
	}
	return &gateway{app: a, token: bytes.TrimSpace(tok), pgp: ring, barrier: bar, records: rec}, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type request struct {
	Key     string          `json:"key"`
	Payload string          `json:"payload"`
	Receipt *intent.Receipt `json:"receipt,omitempty"`
}

func (g *gateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/authorize", g.guarded(g.authorize))
	mux.HandleFunc("POST /v1/execute", g.guarded(g.execute))
	return mux
}

func (g *gateway) guarded(h func(http.ResponseWriter, request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), g.token) != 1 {
			reply(w, http.StatusUnauthorized, map[string]any{"error": "missing or wrong bearer token"})
			return
		}
		var req request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			reply(w, http.StatusBadRequest, map[string]any{"error": "body must be JSON with key, payload and, for execute, receipt"})
			return
		}
		if !keyPattern.MatchString(req.Key) {
			reply(w, http.StatusBadRequest, map[string]any{"error": "key must be 1-64 characters of a-z, 0-9, - or _"})
			return
		}
		if len(req.Payload) == 0 || len(req.Payload) > maxPayloadSize {
			reply(w, http.StatusBadRequest, map[string]any{"error": "payload must be 1 byte to 16 KiB"})
			return
		}
		h(w, req)
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// authorize asks the engine. On ALLOW it spends the approval it relied on and
// returns a short-lived receipt. The receipt carries no payload.
func (g *gateway) authorize(w http.ResponseWriter, req request) {
	// A plaintext payload can be screened now, so a bad one never spends an
	// approval. An encrypted one is opaque here and is screened after decryption.
	if !strings.HasPrefix(strings.TrimSpace(req.Payload), pgpHeader) {
		if err := g.screen(req.Key, req.Payload); err != nil {
			reply(w, http.StatusOK, map[string]any{"verdict": intent.Block, "summary": err.Error()})
			return
		}
	}
	g.mu.Lock()
	g.app.pull()
	d, receipt := g.app.eng.Authorize(recordAction(req.Key, req.Payload))
	g.mu.Unlock()
	out := map[string]any{"verdict": d.Verdict, "summary": d.Summary, "missing": d.Missing}
	if receipt != nil {
		out["receipt"] = receipt
	}
	reply(w, http.StatusOK, out)
}

// execute is the executor. The action is rebuilt from key and payload, and the
// write happens only inside Guard.Run, after the receipt checks out.
func (g *gateway) execute(w http.ResponseWriter, req request) {
	if req.Receipt == nil {
		reply(w, http.StatusBadRequest, map[string]any{"error": "receipt is required"})
		return
	}
	var wrote string
	err := g.app.guard.Run(recordAction(req.Key, req.Payload), req.Receipt, func() error {
		body := req.Payload
		if strings.HasPrefix(strings.TrimSpace(body), pgpHeader) {
			plain, err := pgpDecrypt(g.pgp, body)
			if err != nil {
				return err
			}
			body = plain
		}
		if err := g.screen(req.Key, body); err != nil {
			return err
		}
		var err error
		wrote, err = g.writeRecord(req.Key, body)
		return err
	})
	switch {
	case err == nil:
		reply(w, http.StatusOK, map[string]any{"executed": true, "record": wrote, "receipt_id": req.Receipt.ID})
	case errors.Is(err, intent.ErrNotAuthorized):
		reply(w, http.StatusForbidden, map[string]any{"executed": false, "error": err.Error()})
	case errors.Is(err, errTaint):
		reply(w, http.StatusForbidden, map[string]any{"executed": false, "error": err.Error(), "note": "the receipt is spent; remove the labeled secret and ask for a new approval"})
	default:
		// The receipt and the approval behind it are already spent. See docs/PRIVATE_INTENT.md.
		reply(w, http.StatusInternalServerError, map[string]any{"executed": false, "error": err.Error(), "note": "the receipt is spent; ask for a new approval to retry"})
	}
}

// writeRecord creates records/<key>.txt and refuses to replace an existing one.
// It writes a temp file and links it into place, so a reader never sees half a record.
func (g *gateway) writeRecord(key, body string) (string, error) {
	tmp, err := os.CreateTemp(g.records, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(g.records, key+".txt")
	if err := os.Link(tmp.Name(), final); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("record %q already exists", key)
		}
		return "", err
	}
	return filepath.Base(final), nil
}

func serveGateway(dir, addr string) error {
	g, err := newGateway(dir)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: addr, Handler: g.handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
	}
	fmt.Printf("Private Intent gateway on http://%s (token: %s)\n", addr, filepath.Join(dir, "server", "gateway.token"))
	return srv.ListenAndServe()
}

func encryptFile(dir, path string) error {
	_, pub := pgpPaths(dir)
	pk, err := os.ReadFile(pub)
	if err != nil {
		return fmt.Errorf("no public key (run pgp-init): %w", err)
	}
	plain, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := pgpEncrypt(string(pk), string(plain))
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
