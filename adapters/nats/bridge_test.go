package nats

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/sharedcode/joltrin/ai/verify"
)

// startTestServer starts an in-process NATS server on a random free port
// and returns a connected client, so these tests need no external NATS
// instance and nothing listening beyond the test process itself. Both are
// shut down via t.Cleanup.
func startTestServer(t *testing.T) *nats.Conn {
	t.Helper()

	opts := &server.Options{
		Host:           "127.0.0.1",
		Port:           -1, // random free port
		NoLog:          true,
		NoSigs:         true,
		MaxControlLine: 4096,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start embedded NATS server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not become ready")
	}
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect to embedded NATS server: %v", err)
	}
	t.Cleanup(nc.Close)

	return nc
}

func TestSubjectFor(t *testing.T) {
	cases := []struct {
		workflow string
		want     string
	}{
		{"", DefaultSubject},
		{"prod-db-rollout", "joltrin.verify.prod-db-rollout.decision"},
	}
	for _, c := range cases {
		if got := SubjectFor(c.workflow); got != c.want {
			t.Errorf("SubjectFor(%q) = %q, want %q", c.workflow, got, c.want)
		}
	}
}

// TestBarrierDecisionJSON pins the wire shape a subscriber in any language
// depends on: it needs no NATS connection at all, just the struct.
func TestBarrierDecisionJSON(t *testing.T) {
	ev := BarrierDecision{
		Time:         time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Workflow:     "prod-db-rollout",
		Step:         "drop_prod_db",
		Allowed:      false,
		Rule:         "no-drop-without-validated-backup",
		MissingState: "backup_validated",
		Reason:       `step "drop_prod_db" would establish forbidden state "prod_db_dropped" without required state "backup_validated" first (rule: no-drop-without-validated-backup)`,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got BarrierDecision
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.Time.Equal(ev.Time) || got.Workflow != ev.Workflow || got.Step != ev.Step ||
		got.Allowed != ev.Allowed || got.Rule != ev.Rule ||
		got.MissingState != ev.MissingState || got.Reason != ev.Reason {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, ev)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	if _, ok := raw["replayed"]; ok {
		t.Errorf("replayed should be omitted when false, got %v", raw["replayed"])
	}
}

func dropProdWorkflow(t *testing.T) *verify.Workflow {
	t.Helper()
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
		},
		[]verify.SafetyRule{
			{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	return wf
}

// collectDecisions subscribes to subject and returns a function that waits
// for exactly n BarrierDecision events (or fails the test on timeout).
func collectDecisions(t *testing.T, nc *nats.Conn, subject string, n int) func() []BarrierDecision {
	t.Helper()

	var (
		mu   sync.Mutex
		got  []BarrierDecision
		done = make(chan struct{})
	)
	sub, err := Subscribe(nc, subject, func(ev BarrierDecision) {
		mu.Lock()
		got = append(got, ev)
		reached := len(got) >= n
		mu.Unlock()
		if reached {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	return func() []BarrierDecision {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %d decisions, got %d", n, len(got))
		}
		mu.Lock()
		defer mu.Unlock()
		out := make([]BarrierDecision, len(got))
		copy(out, got)
		return out
	}
}

// TestVerifyBridge_PublishesRealBarrierOutcomes runs the exact blocked-then-
// allowed sequence examples/verify_barrier demonstrates against ai/verify
// directly, through a VerifyBridge instead, and checks the published events
// match what the barrier actually decided.
func TestVerifyBridge_PublishesRealBarrierOutcomes(t *testing.T) {
	nc := startTestServer(t)
	wf := dropProdWorkflow(t)
	bridge := NewVerifyBridge(wf, nc, "prod-db-rollout")

	if want := "joltrin.verify.prod-db-rollout.decision"; bridge.Subject() != want {
		t.Fatalf("Subject() = %q, want %q", bridge.Subject(), want)
	}

	wait := collectDecisions(t, nc, bridge.Subject(), 3)
	trace := verify.NewTrace()

	// 1) Blocked: dropping prod before any backup exists.
	if err := bridge.CheckAndCommit(trace, "drop_prod_db"); err == nil {
		t.Fatal("expected drop_prod_db to be blocked, got nil error")
	} else if !verify.IsViolation(err) {
		t.Fatalf("expected a *verify.Violation, got %v", err)
	}

	// 2) Allowed: taking the backup has no precondition.
	if err := bridge.CheckAndCommit(trace, "take_backup"); err != nil {
		t.Fatalf("take_backup: unexpected error: %v", err)
	}

	// 3) Blocked: drop still forbidden, backup taken but not validated yet.
	if err := bridge.CheckAndCommit(trace, "drop_prod_db"); err == nil {
		t.Fatal("expected drop_prod_db to still be blocked, got nil error")
	}

	events := wait()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	if events[0].Allowed {
		t.Errorf("event 0: Allowed = true, want false (blocked drop)")
	}
	// drop_prod_db itself Requires backup_validated, so an empty trace
	// blocks on that precondition before the safety rule is even reached;
	// see ai/verify.Workflow.checkSafetyLocked's ordering.
	if events[0].Rule != "precondition" {
		t.Errorf("event 0: Rule = %q, want %q", events[0].Rule, "precondition")
	}
	if events[0].MissingState != "backup_validated" {
		t.Errorf("event 0: MissingState = %q, want %q", events[0].MissingState, "backup_validated")
	}

	if !events[1].Allowed {
		t.Errorf("event 1: Allowed = false, want true (take_backup)")
	}
	if events[1].Step != "take_backup" {
		t.Errorf("event 1: Step = %q, want %q", events[1].Step, "take_backup")
	}

	if events[2].Allowed {
		t.Errorf("event 2: Allowed = true, want false (still-blocked drop)")
	}
	for i, ev := range events {
		if ev.Workflow != "prod-db-rollout" {
			t.Errorf("event %d: Workflow = %q, want %q", i, ev.Workflow, "prod-db-rollout")
		}
	}

	// The barrier's own trace is unaffected by any of this: only
	// take_backup actually committed.
	if got := trace.ExecutedSteps(); len(got) != 1 || got[0] != "take_backup" {
		t.Fatalf("trace.ExecutedSteps() = %v, want [take_backup]", got)
	}
}

// TestVerifyBridge_PublishesSafetyRuleViolation exercises the other branch
// of checkSafetyLocked: a step with no failing precondition of its own,
// blocked purely by a SafetyRule on the state it would establish. Confirms
// publish's errors.As extraction of Rule/MissingState from *verify.Violation
// covers the safety-rule case, not just the precondition case
// TestVerifyBridge_PublishesRealBarrierOutcomes already covers.
func TestVerifyBridge_PublishesSafetyRuleViolation(t *testing.T) {
	nc := startTestServer(t)
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "grant_admin", Establishes: []verify.State{"admin_granted"}},
			{ID: "log_grant", Establishes: []verify.State{"grant_logged"}},
		},
		[]verify.SafetyRule{
			{Name: "no-admin-without-audit-log", Forbidden: "admin_granted", Requires: "grant_logged"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	bridge := NewVerifyBridge(wf, nc, "admin-grant")
	trace := verify.NewTrace()

	wait := collectDecisions(t, nc, bridge.Subject(), 1)
	if err := bridge.CheckAndCommit(trace, "grant_admin"); err == nil {
		t.Fatal("expected grant_admin to be blocked by the safety rule, got nil error")
	}

	events := wait()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Allowed {
		t.Error("Allowed = true, want false")
	}
	if events[0].Rule != "no-admin-without-audit-log" {
		t.Errorf("Rule = %q, want %q", events[0].Rule, "no-admin-without-audit-log")
	}
	if events[0].MissingState != "grant_logged" {
		t.Errorf("MissingState = %q, want %q", events[0].MissingState, "grant_logged")
	}
}

// TestVerifyBridge_Idempotent confirms a replayed CheckAndCommitIdempotent
// call still publishes an event, with Replayed set, matching the real
// outcome from the original call rather than being recomputed.
func TestVerifyBridge_Idempotent(t *testing.T) {
	nc := startTestServer(t)
	wf := dropProdWorkflow(t)
	bridge := NewVerifyBridge(wf, nc, "")
	trace := verify.NewTrace()

	wait := collectDecisions(t, nc, bridge.Subject(), 2)

	replayed, err := bridge.CheckAndCommitIdempotent(trace, "take_backup", "retry-key-1")
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if replayed {
		t.Fatal("first call: replayed = true, want false")
	}

	replayed, err = bridge.CheckAndCommitIdempotent(trace, "take_backup", "retry-key-1")
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if !replayed {
		t.Fatal("second call: replayed = false, want true")
	}

	events := wait()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Replayed {
		t.Error("event 0: Replayed = true, want false")
	}
	if !events[1].Replayed {
		t.Error("event 1: Replayed = false, want true")
	}
	if !events[0].Allowed || !events[1].Allowed {
		t.Errorf("both events should be Allowed: %+v, %+v", events[0], events[1])
	}

	// take_backup committed exactly once despite two calls.
	if got := trace.ExecutedSteps(); len(got) != 1 {
		t.Fatalf("trace.ExecutedSteps() = %v, want exactly one commit", got)
	}
}

// TestVerifyBridge_PublishFailureDoesNotChangeDecision confirms that a
// broken connection to NATS never turns an allowed step into a blocked one
// or vice versa: the barrier's own return value is untouched by a publish
// failure, only OnPublishError observes it.
func TestVerifyBridge_PublishFailureDoesNotChangeDecision(t *testing.T) {
	nc := startTestServer(t)
	wf := dropProdWorkflow(t)
	bridge := NewVerifyBridge(wf, nc, "prod-db-rollout")

	var publishErrs int
	bridge.OnPublishError(func(error) { publishErrs++ })

	nc.Close() // force every subsequent publish to fail

	trace := verify.NewTrace()
	if err := bridge.CheckAndCommit(trace, "take_backup"); err != nil {
		t.Fatalf("take_backup should still succeed with NATS down: %v", err)
	}
	if err := bridge.CheckAndCommit(trace, "drop_prod_db"); err == nil {
		t.Fatal("drop_prod_db should still be blocked with NATS down")
	}

	if publishErrs != 2 {
		t.Fatalf("OnPublishError called %d times, want 2", publishErrs)
	}
}
