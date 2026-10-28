package k8sgate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

func dbStore(t *testing.T) *runbookstore.Store {
	t.Helper()
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	s := runbookstore.New()
	if err := s.RegisterWorkflow("db-maintenance", wf); err != nil {
		t.Fatal(err)
	}
	return s
}

var pvcDelete = Rule{Resource: "persistentvolumeclaims", Operation: "DELETE", Workflow: "db-maintenance", Step: "drop_prod_db"}

func newGate(t *testing.T, opts ...Option) (*Gate, *runbookstore.Store) {
	t.Helper()
	s := dbStore(t)
	g, err := New(s, Policy{Rules: []Rule{pvcDelete}}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return g, s
}

// review builds an AdmissionReview body for a request on the given resource.
func review(op, group, resource string, annotations map[string]string) string {
	obj := map[string]any{"metadata": map[string]any{"annotations": annotations}}
	r := map[string]any{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]any{
			"uid":       "uid-1",
			"operation": op,
			"name":      "orders-db-data",
			"namespace": "prod",
			"resource":  map[string]any{"group": group, "version": "v1", "resource": resource},
			"oldObject": obj,
		},
	}
	b, _ := json.Marshal(r)
	return string(b)
}

type verdict struct {
	Code     int
	Allowed  bool
	UID      string
	Message  string
	Warnings []string
}

func validate(t *testing.T, g *Gate, body string) verdict {
	t.Helper()
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(body)))
	v := verdict{Code: rec.Code}
	if rec.Code != http.StatusOK {
		return v
	}
	var out struct {
		Response struct {
			UID     string `json:"uid"`
			Allowed bool   `json:"allowed"`
			Status  struct {
				Message string `json:"message"`
			} `json:"status"`
			Warnings []string `json:"warnings"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not an AdmissionReview: %v\n%s", err, rec.Body.String())
	}
	v.Allowed, v.UID, v.Message, v.Warnings = out.Response.Allowed, out.Response.UID, out.Response.Status.Message, out.Response.Warnings
	return v
}

func TestDeleteIsDeniedWhenPrerequisitesNotRecorded(t *testing.T) {
	g, _ := newGate(t)
	v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{"joltrin.dev/trace-id": "t1"}))
	if v.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", v.Code)
	}
	if v.Allowed {
		t.Fatal("DELETE was allowed with no backup recorded")
	}
	if v.UID != "uid-1" {
		t.Errorf("uid = %q, want it echoed back", v.UID)
	}
	if !strings.Contains(v.Message, "backup_validated") {
		t.Errorf("message %q should name the missing state", v.Message)
	}
}

func commit(t *testing.T, g *Gate, token, workflow, trace, step string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"workflow": workflow, "trace_id": trace, "step": step})
	req := httptest.NewRequest(http.MethodPost, "/commit", strings.NewReader(string(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestDeleteIsAllowedOnceStepsAreCommitted(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	for _, step := range []string{"take_backup", "validate_backup"} {
		if code, out := commit(t, g, "s3cret", "db-maintenance", "t1", step); code != http.StatusOK {
			t.Fatalf("commit %s = %d %v, want 200", step, code, out)
		}
	}
	v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{"joltrin.dev/trace-id": "t1"}))
	if !v.Allowed {
		t.Fatalf("DELETE denied after backup was validated: %s", v.Message)
	}
}

func TestCommitRejectsMissingOrWrongToken(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	for name, token := range map[string]string{"missing": "", "wrong": "nope"} {
		if code, _ := commit(t, g, token, "db-maintenance", "t1", "take_backup"); code != http.StatusUnauthorized {
			t.Errorf("%s token: status = %d, want 401", name, code)
		}
	}
	// The rejected calls must not have recorded anything: a later valid
	// validate_backup still needs take_backup first.
	if code, _ := commit(t, g, "s3cret", "db-maintenance", "t1", "validate_backup"); code != http.StatusConflict {
		t.Errorf("validate_backup after rejected take_backup = %d, want 409", code)
	}
}

func TestCommitIsDisabledWithoutAConfiguredToken(t *testing.T) {
	g, _ := newGate(t)
	if code, _ := commit(t, g, "anything", "db-maintenance", "t1", "take_backup"); code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when no commit token is configured", code)
	}
}

func TestRequestsNoRuleCoversAreAllowed(t *testing.T) {
	g, _ := newGate(t)
	ann := map[string]string{"joltrin.dev/trace-id": "t1"}
	for name, body := range map[string]string{
		"other resource":  review("DELETE", "", "configmaps", ann),
		"other operation": review("UPDATE", "", "persistentvolumeclaims", ann),
		"other group":     review("DELETE", "apps", "persistentvolumeclaims", ann),
	} {
		if v := validate(t, g, body); !v.Allowed {
			t.Errorf("%s: denied (%s), want allowed because no rule covers it", name, v.Message)
		}
	}
}

func TestMatchedRequestWithoutTraceIDIsDenied(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", nil))
	if v.Allowed {
		t.Fatal("DELETE was allowed with no trace id annotation")
	}
	if !strings.Contains(v.Message, "joltrin.dev/trace-id") {
		t.Errorf("message %q should say which annotation is missing", v.Message)
	}
}

func TestCommitWithoutTraceIDIsRejected(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	if code, _ := commit(t, g, "s3cret", "db-maintenance", "", "take_backup"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestCommitOfUnknownWorkflowOrStepIsNotFound(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	if code, _ := commit(t, g, "s3cret", "no-such-runbook", "t1", "take_backup"); code != http.StatusNotFound {
		t.Errorf("unknown workflow: status = %d, want 404", code)
	}
	if code, _ := commit(t, g, "s3cret", "db-maintenance", "t1", "no_such_step"); code != http.StatusNotFound {
		t.Errorf("unknown step: status = %d, want 404", code)
	}
}

func TestNewRejectsRulesThatReferenceNothing(t *testing.T) {
	s := dbStore(t)
	bad := map[string]Rule{
		"unknown workflow": {Resource: "persistentvolumeclaims", Operation: "DELETE", Workflow: "nope", Step: "drop_prod_db"},
		"unknown step":     {Resource: "persistentvolumeclaims", Operation: "DELETE", Workflow: "db-maintenance", Step: "nope"},
		"no resource":      {Operation: "DELETE", Workflow: "db-maintenance", Step: "drop_prod_db"},
		"no operation":     {Resource: "persistentvolumeclaims", Workflow: "db-maintenance", Step: "drop_prod_db"},
	}
	for name, r := range bad {
		if _, err := New(s, Policy{Rules: []Rule{r}}); err == nil {
			t.Errorf("%s: New accepted the rule", name)
		}
	}
	if _, err := New(s, Policy{}); err == nil {
		t.Error("New accepted a policy with no rules")
	}
}

func TestWarnModeAllowsButSaysWhatWouldHaveBeenBlocked(t *testing.T) {
	g, _ := newGate(t, WithWarnOnly())
	v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{"joltrin.dev/trace-id": "t1"}))
	if !v.Allowed {
		t.Fatalf("warn mode denied the request: %s", v.Message)
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "backup_validated") {
		t.Errorf("warnings = %v, want one naming backup_validated", v.Warnings)
	}
}

func TestLoadPolicyReadsRulesAndRejectsTypos(t *testing.T) {
	p, err := LoadPolicy(strings.NewReader(`{"rules":[{"resource":"persistentvolumeclaims","operation":"DELETE","workflow":"db-maintenance","step":"drop_prod_db"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 1 || p.Rules[0].Step != "drop_prod_db" {
		t.Errorf("policy = %+v", p)
	}
	if _, err := LoadPolicy(strings.NewReader(`{"rules":[{"resorce":"x"}]}`)); err == nil {
		t.Error("a misspelled key was accepted")
	}
	if _, err := LoadPolicy(strings.NewReader(`{"rules":[]} {"rules":[]}`)); err == nil {
		t.Error("trailing content was accepted")
	}
}

func TestOversizedRequestBodyIsRejected(t *testing.T) {
	g, _ := newGate(t)
	// A well-formed review, so only its size can be the reason to refuse it.
	big := review("DELETE", "", "persistentvolumeclaims", map[string]string{"joltrin.dev/trace-id": strings.Repeat("a", 2<<20)})
	if v := validate(t, g, big); v.Code != http.StatusBadRequest && v.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 400 or 413", v.Code)
	}
}

func TestHealthz(t *testing.T) {
	g, _ := newGate(t)
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// Many requests hit one gate at once, on the same and on different traces.
// Run with -race: it fails if the store or a trace is touched without a lock.
func TestConcurrentValidateAndCommitAreSafe(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		trace := fmt.Sprintf("t%d", i%4)
		wg.Add(3)
		go func() {
			defer wg.Done()
			commit(t, g, "s3cret", "db-maintenance", trace, "take_backup")
		}()
		go func() {
			defer wg.Done()
			commit(t, g, "s3cret", "db-maintenance", trace, "validate_backup")
		}()
		go func() {
			defer wg.Done()
			validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{TraceAnnotation: trace}))
		}()
	}
	wg.Wait()
	for i := 0; i < 4; i++ {
		v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{TraceAnnotation: fmt.Sprintf("t%d", i)}))
		if !v.Allowed {
			t.Errorf("trace t%d: denied after both steps were recorded: %s", i, v.Message)
		}
	}
}

// /validate is read-only. If it created traces, anything that can reach the
// gate could push recorded steps out of the store by sending fresh trace ids.
func TestValidateDoesNotCreateTraces(t *testing.T) {
	g, s := newGate(t)
	for i := 0; i < 5; i++ {
		validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{TraceAnnotation: fmt.Sprintf("fresh-%d", i)}))
	}
	if n := s.TraceCount(); n != 0 {
		t.Errorf("TraceCount = %d after validate-only requests, want 0", n)
	}
}

func TestOverlongTraceIDIsRefused(t *testing.T) {
	g, _ := newGate(t, WithCommitToken("s3cret"))
	long := strings.Repeat("a", runbookstore.MaxIDLength+1)
	if code, _ := commit(t, g, "s3cret", "db-maintenance", long, "take_backup"); code != http.StatusBadRequest {
		t.Errorf("commit with a %d byte trace id = %d, want 400", len(long), code)
	}
	v := validate(t, g, review("DELETE", "", "persistentvolumeclaims", map[string]string{TraceAnnotation: long}))
	if v.Allowed {
		t.Error("DELETE allowed with an overlong trace id")
	}
}

func updateReview(oldLabels, newLabels, oldAnn, newAnn map[string]string) string {
	meta := func(l, a map[string]string) map[string]any {
		return map[string]any{"metadata": map[string]any{"labels": l, "annotations": a}}
	}
	r := map[string]any{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request": map[string]any{
			"uid":       "uid-1",
			"operation": "UPDATE",
			"resource":  map[string]any{"group": "", "version": "v1", "resource": "persistentvolumeclaims"},
			"oldObject": meta(oldLabels, oldAnn),
			"object":    meta(newLabels, newAnn),
		},
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func TestUpdateCannotRemoveTheGateLabelOrTheTraceID(t *testing.T) {
	on := map[string]string{GateLabel: "enabled"}
	trace := map[string]string{TraceAnnotation: "run-1"}
	cases := []struct {
		name                   string
		oldL, newL, oldA, newA map[string]string
		allowed                bool
	}{
		{"label removed", on, nil, trace, trace, false},
		{"label changed", on, map[string]string{GateLabel: "disabled"}, trace, trace, false},
		{"trace id removed", on, on, trace, nil, false},
		{"trace id changed to another run", on, on, trace, map[string]string{TraceAnnotation: "run-2"}, false},
		{"unrelated update keeps protection", on, on, trace, trace, true},
		{"protection added to an unlabelled object", nil, on, nil, trace, true},
		{"trace id set where none existed", on, on, nil, trace, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newGate(t)
			v := validate(t, g, updateReview(c.oldL, c.newL, c.oldA, c.newA))
			if v.Allowed != c.allowed {
				t.Fatalf("allowed = %v, want %v (%s)", v.Allowed, c.allowed, v.Message)
			}
		})
	}
}

func TestUpdateOnAnUncoveredResourceIsNotTouched(t *testing.T) {
	g, _ := newGate(t)
	body := strings.Replace(updateReview(map[string]string{GateLabel: "enabled"}, nil, nil, nil), "persistentvolumeclaims", "configmaps", 1)
	if v := validate(t, g, body); !v.Allowed {
		t.Fatalf("an update on a resource the policy does not cover was refused: %s", v.Message)
	}
}
