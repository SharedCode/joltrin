// Package k8sgate is a Kubernetes validating admission webhook that puts the
// verify barrier in front of destructive cluster operations.
package k8sgate

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

const (
	// maxBodyBytes stays above the 1.5 MiB the API server accepts for one
	// object, so a real review is never cut off.
	maxBodyBytes   = 2 << 20
	maxPolicyBytes = 1 << 20
)

// TraceAnnotation names the annotation that carries a trace id.
const TraceAnnotation = "joltrin.dev/trace-id"

// GateLabel is the label that routes an object to the gate. The webhook
// configuration selects on it, so removing it would take the object out of
// the gate's reach.
const GateLabel = "joltrin.dev/gate"

// Rule maps one Kubernetes operation to a runbook step.
type Rule struct {
	Group     string `json:"group"`
	Resource  string `json:"resource"`
	Operation string `json:"operation"`
	Workflow  string `json:"workflow"`
	Step      string `json:"step"`
}

// Policy is the set of rules the gate enforces.
type Policy struct {
	Rules []Rule `json:"rules"`
}

// Gate serves the admission and commit endpoints.
type Gate struct {
	store       *runbookstore.Store
	policy      Policy
	commitToken string
	warnOnly    bool
	metrics     *metrics
	log         *slog.Logger
}

// Option configures a Gate.
type Option func(*Gate)

// WithWarnOnly makes the gate allow every request and report what it would
// have blocked as an admission warning.
func WithWarnOnly() Option { return func(g *Gate) { g.warnOnly = true } }

// WithLogger sends one structured audit record per admission decision to l.
func WithLogger(l *slog.Logger) Option { return func(g *Gate) { g.log = l } }

// WithCommitToken requires this bearer token on the commit endpoint.
func WithCommitToken(token string) Option { return func(g *Gate) { g.commitToken = token } }

// New builds a Gate over the runbooks in store.
func New(store *runbookstore.Store, p Policy, opts ...Option) (*Gate, error) {
	if len(p.Rules) == 0 {
		return nil, errors.New("k8sgate: the policy has no rules")
	}
	for i, r := range p.Rules {
		if r.Resource == "" || r.Operation == "" {
			return nil, fmt.Errorf("k8sgate: rule %d needs both a resource and an operation", i)
		}
		wf, ok := store.Workflow(r.Workflow)
		if !ok {
			return nil, fmt.Errorf("k8sgate: rule %d names workflow %q, which is not registered", i, r.Workflow)
		}
		if _, ok := wf.Steps[verify.StepID(r.Step)]; !ok {
			return nil, fmt.Errorf("k8sgate: rule %d names step %q, which workflow %q does not have", i, r.Step, r.Workflow)
		}
	}
	g := &Gate{store: store, policy: p, metrics: newMetrics()}
	for _, o := range opts {
		o(g)
	}
	return g, nil
}

// Handler returns the HTTP handler for the gate.
func (g *Gate) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /metrics", g.serveMetrics)
	mux.HandleFunc("POST /validate", g.validate)
	mux.HandleFunc("POST /commit", g.commit)
	return mux
}

type admissionReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Request    *admissionRequest `json:"request,omitempty"`
	Response   *admissionResult  `json:"response,omitempty"`
}

type admissionRequest struct {
	UID       string `json:"uid"`
	Operation string `json:"operation"`
	Resource  struct {
		Group    string `json:"group"`
		Resource string `json:"resource"`
	} `json:"resource"`
	OldObject objectMeta `json:"oldObject"`
	Object    objectMeta `json:"object"`
}

type objectMeta struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
		Labels      map[string]string `json:"labels"`
	} `json:"metadata"`
}

type admissionResult struct {
	UID      string   `json:"uid"`
	Allowed  bool     `json:"allowed"`
	Warnings []string `json:"warnings,omitempty"`
	Status   *struct {
		Message string `json:"message"`
	} `json:"status,omitempty"`
}

func (g *Gate) validate(w http.ResponseWriter, r *http.Request) {
	var in admissionReview
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Request == nil {
		http.Error(w, "not an AdmissionReview", http.StatusBadRequest)
		return
	}
	req := in.Request
	res := &admissionResult{UID: req.UID, Allowed: true}
	var traceID string
	if req.Operation == "UPDATE" {
		if msg := g.protectionRemoved(req); msg != "" {
			traceID = req.OldObject.Metadata.Annotations[TraceAnnotation]
			g.deny(res, msg)
			g.finish(w, in, res, traceID, true)
			return
		}
		g.finish(w, in, res, traceID, false)
		return
	}
	rule, ok := g.match(req)
	if !ok {
		g.finish(w, in, res, traceID, false)
		return
	}
	traceID = req.OldObject.Metadata.Annotations[TraceAnnotation]
	if traceID == "" {
		g.deny(res, "the object has no "+TraceAnnotation+" annotation, so the gate cannot tell which run this "+req.Operation+" belongs to")
		g.finish(w, in, res, traceID, true)
		return
	}
	if err := runbookstore.CheckID("trace id", traceID); err != nil {
		g.deny(res, err.Error())
		g.finish(w, in, res, traceID, true)
		return
	}
	wf, _ := g.store.Workflow(rule.Workflow)
	// A read-only check must not create a trace. A trace the store does not
	// hold is an empty one, which is what an unrecorded run is.
	trace, ok := g.store.LookupTrace(traceID)
	if !ok {
		trace = verify.NewTrace()
	}
	if err := wf.CheckSafety(trace, verify.StepID(rule.Step)); err != nil {
		g.deny(res, err.Error())
	}
	g.finish(w, in, res, traceID, true)
}

func (g *Gate) deny(res *admissionResult, msg string) {
	if g.warnOnly {
		res.Warnings = append(res.Warnings, "joltrin would block this: "+msg)
		return
	}
	res.Allowed = false
	res.Status = &struct {
		Message string `json:"message"`
	}{Message: msg}
}

// maxLogValue bounds each request-supplied value in an audit record.
const maxLogValue = 256

// logSafe makes a request-supplied value safe to put in a log record: line
// breaks and other control characters are replaced, and long values are cut.
func logSafe(v string) string {
	v = strings.ReplaceAll(strings.ReplaceAll(v, "\n", " "), "\r", " ")
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, v)
	if r := []rune(v); len(r) > maxLogValue {
		return string(r[:maxLogValue]) + "..."
	}
	return v
}

// finish records the decision in the metrics and the audit log, then replies.
func (g *Gate) finish(w http.ResponseWriter, in admissionReview, res *admissionResult, traceID string, matched bool) {
	result, reason := "unmatched", ""
	if matched {
		switch {
		case !res.Allowed:
			result = "denied"
			if res.Status != nil {
				reason = res.Status.Message
			}
		case len(res.Warnings) > 0:
			result, reason = "warned", res.Warnings[0]
		default:
			result = "allowed"
		}
	}
	req := in.Request
	g.metrics.admit(req.Operation, req.Resource.Resource, result)
	if g.log != nil {
		g.log.Info("admission", "result", result, "operation", logSafe(req.Operation),
			"resource", logSafe(req.Resource.Resource), "trace_id", logSafe(traceID), "uid", logSafe(req.UID), "reason", logSafe(reason))
	}
	g.respond(w, in, res)
}

func (g *Gate) respond(w http.ResponseWriter, in admissionReview, res *admissionResult) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(admissionReview{APIVersion: in.APIVersion, Kind: in.Kind, Response: res})
}

// covers reports whether the policy has a rule for the request's resource.
func (g *Gate) covers(req *admissionRequest) bool {
	for _, r := range g.policy.Rules {
		if r.Group == req.Resource.Group && r.Resource == req.Resource.Resource {
			return true
		}
	}
	return false
}

// protectionRemoved explains why an UPDATE must be refused: it takes the gate
// label off a protected object, or changes or clears its trace id. Either one
// would let the next DELETE skip the check. It returns "" for any other update.
func (g *Gate) protectionRemoved(req *admissionRequest) string {
	if !g.covers(req) || req.OldObject.Metadata.Labels[GateLabel] != "enabled" {
		return ""
	}
	if req.Object.Metadata.Labels[GateLabel] != "enabled" {
		return "the " + GateLabel + " label cannot be removed or changed from a protected object, because that takes it out of the gate's reach"
	}
	if old := req.OldObject.Metadata.Annotations[TraceAnnotation]; old != "" && req.Object.Metadata.Annotations[TraceAnnotation] != old {
		return "the " + TraceAnnotation + " annotation cannot be changed or removed from a protected object, because it names the run the gate checks"
	}
	return ""
}

func (g *Gate) match(req *admissionRequest) (Rule, bool) {
	for _, r := range g.policy.Rules {
		if r.Group == req.Resource.Group && r.Resource == req.Resource.Resource && r.Operation == req.Operation {
			return r, true
		}
	}
	return Rule{}, false
}

type commitRequest struct {
	Workflow string `json:"workflow"`
	TraceID  string `json:"trace_id"`
	Step     string `json:"step"`
}

func (g *Gate) commit(w http.ResponseWriter, r *http.Request) {
	if g.commitToken == "" {
		g.metrics.committed("disabled")
		http.Error(w, "the commit endpoint is disabled: no commit token is configured", http.StatusForbidden)
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(g.commitToken)) != 1 {
		g.metrics.committed("unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in commitRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		g.metrics.committed("rejected")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if in.TraceID == "" {
		g.metrics.committed("rejected")
		http.Error(w, "trace_id is required", http.StatusBadRequest)
		return
	}
	if err := runbookstore.CheckID("trace_id", in.TraceID); err != nil {
		g.metrics.committed("rejected")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wf, ok := g.store.Workflow(in.Workflow)
	if !ok {
		g.metrics.committed("rejected")
		http.Error(w, "unknown workflow "+strconv.Quote(in.Workflow), http.StatusNotFound)
		return
	}
	if _, ok := wf.Steps[verify.StepID(in.Step)]; !ok {
		g.metrics.committed("rejected")
		http.Error(w, "unknown step "+strconv.Quote(in.Step), http.StatusNotFound)
		return
	}
	trace := g.store.TraceFor(in.TraceID)
	if err := wf.CheckAndCommit(trace, verify.StepID(in.Step)); err != nil {
		g.metrics.committed("conflict")
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	g.metrics.committed("ok")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"executed": true, "trace": trace.ExecutedSteps()})
}

// LoadPolicy reads a policy from JSON. Unknown fields and trailing content
// are errors, so a misspelled key is caught instead of ignored.
func LoadPolicy(r io.Reader) (Policy, error) {
	dec := json.NewDecoder(io.LimitReader(r, maxPolicyBytes))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("k8sgate: invalid policy: %w", err)
	}
	if dec.More() {
		return Policy{}, errors.New("k8sgate: unexpected content after the policy")
	}
	return p, nil
}

// Files names the inputs of a gate. Runbooks is optional and defaults to the
// built-in db-maintenance runbook. CommitTokenFile is optional and, when empty,
// leaves the commit endpoint disabled.
type Files struct {
	Runbooks        string
	Policy          string
	CommitTokenFile string
	WarnOnly        bool
	Logger          *slog.Logger
}

// FromFiles builds a Gate from files.
func FromFiles(f Files) (*Gate, error) {
	if f.Policy == "" {
		return nil, errors.New("k8sgate: a policy file is required")
	}
	store := runbookstore.New()
	if f.Runbooks == "" {
		wf, err := runbookstore.DBMaintenanceWorkflow()
		if err != nil {
			return nil, err
		}
		if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
			return nil, err
		}
	} else if _, err := runbookstore.LoadFile(store, f.Runbooks); err != nil {
		return nil, err
	}
	pf, err := os.Open(f.Policy)
	if err != nil {
		return nil, fmt.Errorf("k8sgate: %w", err)
	}
	defer pf.Close()
	policy, err := LoadPolicy(pf)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Policy, err)
	}
	var opts []Option
	if f.CommitTokenFile != "" {
		raw, err := os.ReadFile(f.CommitTokenFile)
		if err != nil {
			return nil, fmt.Errorf("k8sgate: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return nil, fmt.Errorf("k8sgate: the commit token file %s is empty", f.CommitTokenFile)
		}
		opts = append(opts, WithCommitToken(token))
	}
	if f.WarnOnly {
		opts = append(opts, WithWarnOnly())
	}
	if f.Logger != nil {
		opts = append(opts, WithLogger(f.Logger))
	}
	return New(store, policy, opts...)
}
