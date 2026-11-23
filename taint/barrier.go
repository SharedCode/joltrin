package taint

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/v5/governance"
	"github.com/sharedcode/joltrin/v5/logsafe"
)

// Outcomes of a check.
const (
	OutcomeAllow      = "allow"
	OutcomeBlock      = "block"
	OutcomeHoneytoken = "honeytoken"
	// OutcomeObserved means the call would have been blocked, and was allowed
	// because the barrier is in observe mode.
	OutcomeObserved = "observed"
)

// Reasons. They are stable, machine-readable codes and never contain data.
const (
	ReasonNoMatch         = "no_match"
	ReasonSinkAllowed     = "sink_allowed"
	ReasonLabelNotAllowed = "label_not_allowed_for_sink"
	ReasonHoneytoken      = "honeytoken_tripped"
	ReasonNotInspectable  = "arguments_not_inspectable"
	ReasonAuditFailed     = "audit_unavailable"
	auditAction           = "taint.check"
	maxDepth              = 64
	maxLogField           = 128
	tainted               = "[tainted]"
	redactedKeyContext    = "audit-pii"
)

// Call is one agent tool call to check.
type Call struct {
	// Tool is the tool name.
	Tool string `json:"tool"`
	// Destination is where the call sends data: a URL, host[:port] or an
	// absolute file path. Leave it empty for a call that sends nowhere.
	Destination string `json:"destination,omitempty"`
	// Args are the call arguments: any JSON-shaped value.
	Args any `json:"args,omitempty"`
	// Actor identifies the agent or session in the audit log.
	Actor string `json:"actor,omitempty"`
}

// Finding is one labeled value found in a call.
type Finding struct {
	Match
	// Allowed says whether the sink was allowed for this finding's label.
	Allowed bool `json:"allowed"`
}

// Receipt is the audit event for one decision. It carries the hash chain link
// and, when the audit sink signs, an Ed25519 signature over the hash. Verify
// it offline with VerifyReceipt or VerifyExport.
type Receipt struct {
	Event governance.AuditEvent `json:"event"`
}

// Verdict is the result of Check. It never contains a protected value.
type Verdict struct {
	Allowed  bool      `json:"allowed"`
	Outcome  string    `json:"outcome"`
	Reason   string    `json:"reason"`
	Sink     string    `json:"sink,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
	Receipt  *Receipt  `json:"receipt,omitempty"`
}

// Alert is raised when a honeytoken appears in a call.
type Alert struct {
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	ValueID   string    `json:"value_id"`
	Tool      string    `json:"tool"`
	Sink      string    `json:"sink"`
	EventHash string    `json:"event_hash,omitempty"`
}

// Config builds a Barrier.
type Config struct {
	// Registry holds the labeled values and honeytokens. Required.
	Registry *Registry
	// Policy is the per-label allowlist. Required. Mode in the policy is used
	// unless Mode below is set.
	Policy *Policy
	// Mode overrides the policy mode when not empty.
	Mode Mode
	// Audit receives one hash-chained event per decision. Optional. Give it a
	// governance sink built with an Ed25519 signer to get signed receipts.
	// Events are masked with governance.PIIRedactor before they are recorded,
	// so the hash and signature cover the masked text.
	Audit governance.AuditSink
	// OnAlert is called, after the audit write, for each honeytoken trip.
	OnAlert func(Alert)
	// Now is the clock, for tests.
	Now func() time.Time
}

// Barrier checks tool calls against a Registry and a Policy. It is safe for
// concurrent use.
type Barrier struct {
	reg     *Registry
	policy  *Policy
	mode    Mode
	audit   governance.AuditSink
	redact  governance.PIIRedactor
	onAlert func(Alert)
	now     func() time.Time
}

// New returns a Barrier.
func New(cfg Config) (*Barrier, error) {
	if cfg.Registry == nil || cfg.Policy == nil {
		return nil, errors.New("taint: Registry and Policy are required")
	}
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	mode := cfg.Mode
	if mode == "" {
		mode = cfg.Policy.Mode
	}
	if mode == "" {
		mode = ModeEnforce
	}
	if mode != ModeEnforce && mode != ModeObserve {
		return nil, fmt.Errorf("taint: mode must be enforce or observe, got %q", mode)
	}
	b := &Barrier{reg: cfg.Registry, policy: cfg.Policy, mode: mode, onAlert: cfg.OnAlert, now: cfg.Now}
	if b.now == nil {
		b.now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Audit != nil {
		mac := hmac.New(sha256.New, cfg.Registry.key)
		mac.Write([]byte(redactedKeyContext))
		b.audit = cfg.Audit
		b.redact = governance.PIIRedactor{Key: mac.Sum(nil)}
	}
	return b, nil
}

// Mode reports the mode in force.
func (b *Barrier) Mode() Mode { return b.mode }

// Check decides one tool call, records the decision and returns the verdict.
// In enforce mode a call that is not allowed has Allowed false and must not
// run. A blocked call is a normal result, not an error. Check returns an error
// only when the audit write fails, and in enforce mode the verdict is then
// also blocked.
func (b *Barrier) Check(ctx context.Context, call Call) (Verdict, error) {
	s := classify(call.Tool, call.Destination)

	views, ok := collect(call)
	var matches []Match
	reason := ReasonNoMatch
	if !ok {
		reason = ReasonNotInspectable
	} else {
		matches = b.reg.ScanViews(views...)
	}

	v := Verdict{Sink: b.safe(s.label)}
	var trip *Match
	for i := range matches {
		m := matches[i]
		f := Finding{Match: m}
		if m.Honeytoken {
			if trip == nil {
				trip = &matches[i]
			}
		} else {
			f.Allowed = b.policy.allows(m.Label, s)
		}
		v.Findings = append(v.Findings, f)
	}

	denied := false
	switch {
	case !ok:
		denied = true
	case trip != nil:
		denied = true
		reason = ReasonHoneytoken
	case len(matches) > 0:
		reason = ReasonSinkAllowed
		for _, f := range v.Findings {
			if !f.Allowed {
				denied = true
				reason = ReasonLabelNotAllowed
				break
			}
		}
	}

	v.Reason = reason
	switch {
	case !denied:
		v.Allowed, v.Outcome = true, OutcomeAllow
	case b.mode == ModeObserve:
		v.Allowed, v.Outcome = true, OutcomeObserved
	case trip != nil:
		v.Outcome = OutcomeHoneytoken
	default:
		v.Outcome = OutcomeBlock
	}
	if trip != nil && b.mode == ModeObserve {
		v.Outcome = OutcomeObserved
	}

	var recErr error
	if b.audit != nil {
		ev := b.event(call, v, trip != nil, denied)
		b.redact.RedactEvent(ev)
		if recErr = b.audit.Record(ctx, ev); recErr == nil {
			v.Receipt = &Receipt{Event: *ev}
		} else if b.mode == ModeEnforce && v.Allowed {
			// A decision that cannot be recorded is not allowed in enforce mode.
			v.Allowed, v.Outcome, v.Reason = false, OutcomeBlock, ReasonAuditFailed
		}
	}
	if trip != nil && b.onAlert != nil {
		a := Alert{Time: b.now(), Kind: ReasonHoneytoken, ValueID: trip.ValueID, Tool: b.safe(call.Tool), Sink: v.Sink}
		if v.Receipt != nil {
			a.EventHash = v.Receipt.Event.Hash
		}
		b.onAlert(a)
	}
	if recErr != nil {
		return v, errors.New("taint: audit write failed")
	}
	return v, nil
}

func (b *Barrier) event(call Call, v Verdict, honey, denied bool) *governance.AuditEvent {
	dec := governance.DecisionAllow
	switch {
	case honey:
		dec = governance.DecisionViolation
	case denied && b.mode == ModeEnforce:
		dec = governance.DecisionDeny
	}
	var labels, values, variants []string
	seenL, seenV, seenVar := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range v.Findings {
		if !seenL[f.Label] {
			seenL[f.Label] = true
			labels = append(labels, f.Label)
		}
		if !seenV[f.ValueID] {
			seenV[f.ValueID] = true
			values = append(values, b.safe(f.ValueID))
		}
		if !seenVar[f.Variant] {
			seenVar[f.Variant] = true
			variants = append(variants, f.Variant)
		}
	}
	sort.Strings(labels)
	sort.Strings(values)
	sort.Strings(variants)
	actor := call.Actor
	if actor == "" {
		actor = "agent"
	}
	meta := map[string]string{
		"tool":    b.safe(call.Tool),
		"sink":    v.Sink,
		"mode":    string(b.mode),
		"outcome": v.Outcome,
	}
	if len(labels) > 0 {
		meta["labels"] = strings.Join(labels, ",")
		meta["values"] = strings.Join(values, ",")
		meta["variants"] = strings.Join(variants, ",")
	}
	if honey {
		meta["honeytoken"] = "true"
	}
	return &governance.AuditEvent{
		Timestamp: b.now(),
		ActorID:   b.safe(actor),
		Action:    auditAction,
		Resource:  "tool:" + b.safe(call.Tool),
		Decision:  dec,
		Reason:    v.Reason,
		Metadata:  meta,
	}
}

// safe makes a string fit for the audit log and for a verdict: newlines are
// escaped, anything that matches a registered value is replaced, and the
// result is cut to a short length. Tool names and hosts come from the model
// and can carry a secret.
func (b *Barrier) safe(s string) string {
	if len(b.reg.Scan(s)) > 0 {
		return tainted
	}
	s = logsafe.S(s)
	if len(s) > maxLogField {
		s = s[:maxLogField]
		if len(b.reg.Scan(s)) > 0 {
			return tainted
		}
	}
	return s
}

// flat is a call flattened for scanning. all holds the tool, the destination
// and every key and scalar, in key order. vals holds only the scalars. Pieces
// are joined with no separator, so a value split across arguments is still
// contiguous in vals even when keys sit between the pieces in all.
type flat struct {
	all, vals strings.Builder
}

func (f *flat) key(s string) { f.all.WriteString(s) }
func (f *flat) val(s string) { f.all.WriteString(s); f.vals.WriteString(s) }

// collect returns the views to scan. It returns false when the call is too
// large or too deep.
func collect(call Call) ([]string, bool) {
	var f flat
	f.key(call.Tool)
	f.key(call.Destination)
	if !walk(&f, call.Args, 0) || f.all.Len() > MaxArgBytes {
		return nil, false
	}
	return []string{f.all.String(), f.vals.String()}, true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func walk(f *flat, v any, depth int) bool {
	if depth > maxDepth || f.all.Len() > MaxArgBytes {
		return false
	}
	switch t := v.(type) {
	case nil, bool:
	case string:
		f.val(t)
	case json.Number:
		f.val(t.String())
	case float64:
		f.val(strconv.FormatFloat(t, 'f', -1, 64))
	case int:
		f.val(strconv.Itoa(t))
	case int64:
		f.val(strconv.FormatInt(t, 10))
	case []string:
		for _, e := range t {
			f.val(e)
		}
	case []any:
		for _, e := range t {
			if !walk(f, e, depth+1) {
				return false
			}
		}
	case map[string]any:
		for _, k := range sortedKeys(t) {
			f.key(k)
			if !walk(f, t[k], depth+1) {
				return false
			}
		}
	case map[string]string:
		for _, k := range sortedKeys(t) {
			f.key(k)
			f.val(t[k])
		}
	case []byte:
		f.val(string(t))
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return false
		}
		var generic any
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&generic); err != nil {
			return false
		}
		return walk(f, generic, depth+1)
	}
	return true
}

// ErrBadReceipt is returned when a receipt does not verify.
var ErrBadReceipt = errors.New("taint: receipt does not verify")

// VerifyReceipt checks one receipt offline: the hash matches the event and the
// Ed25519 signature over the hash verifies under pub. It does not check the
// link to the previous event; VerifyExport does.
func VerifyReceipt(pub ed25519.PublicKey, r Receipt) error {
	ev := r.Event
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("taint: ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	if ev.Hash == "" || ev.Hash != ev.ComputeHash(ev.PrevHash) {
		return fmt.Errorf("%w: hash mismatch", ErrBadReceipt)
	}
	if !ed25519.Verify(pub, []byte(ev.Hash), ev.Signature) {
		return fmt.Errorf("%w: bad signature", ErrBadReceipt)
	}
	return nil
}

// VerifyExport reads the JSON written by MemoryAuditRecorder.ExportJSON and
// checks the whole chain offline: every link, every hash and every signature.
// An unsigned event is a failure.
func VerifyExport(r io.Reader, pub ed25519.PublicKey) error {
	var events []governance.AuditEvent
	if err := json.NewDecoder(r).Decode(&events); err != nil {
		return fmt.Errorf("taint: export is not valid JSON: %w", err)
	}
	prev := ""
	for i := range events {
		ev := events[i]
		if ev.PrevHash != prev {
			return fmt.Errorf("%w: event %d breaks the chain", governance.ErrTamperedAuditChain, i)
		}
		if err := VerifyReceipt(pub, Receipt{Event: ev}); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		prev = ev.Hash
	}
	return nil
}
