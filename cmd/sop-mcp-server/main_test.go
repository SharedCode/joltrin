package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewStoreDefaultsToTheExampleRunbook(t *testing.T) {
	store, names, err := newStore("")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "db-maintenance" || strings.Join(store.WorkflowNames(), ",") != "db-maintenance" {
		t.Errorf("names=%v store=%v", names, store.WorkflowNames())
	}
}

func TestNewStoreWithAFileServesOnlyThatFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runbooks.json")
	doc := `{"workflows":{"deploy":{"steps":[{"id":"run_tests","establishes":["tests_passed"]},{"id":"deploy","requires":["tests_passed"]}]}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	store, names, err := newStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "deploy" {
		t.Errorf("names = %v", names)
	}
	if _, ok := store.Workflow("db-maintenance"); ok {
		t.Error("the example runbook must not be offered next to the user's own")
	}
}

func TestNewStoreRefusesABadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runbooks.json")
	os.WriteFile(path, []byte(`{"workflows":{"w":{"steps":[{"id":"a","requires":["typo"],"establishes":["x"]}]}}}`), 0o600)
	if _, _, err := newStore(path); err == nil || !strings.Contains(err.Error(), `requires state "typo"`) {
		t.Errorf("want the typo reported, got %v", err)
	}
	if _, _, err := newStore(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("a missing file must be an error")
	}
}

func TestObservabilityEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	var errw bytes.Buffer

	opts, closeAll, err := observability(env(nil), &errw)
	if err != nil || len(opts) != 0 {
		t.Fatalf("nothing set: opts=%d err=%v", len(opts), err)
	}
	closeAll()

	for _, bad := range []string{"0", "-1", "many"} {
		if _, _, err := observability(env(map[string]string{"SOP_RETRY_LIMIT": bad}), &errw); err == nil {
			t.Errorf("SOP_RETRY_LIMIT=%q should be refused", bad)
		}
	}

	for _, bad := range []string{"off", "Observe", "dry-run"} {
		if _, _, err := observability(env(map[string]string{"SOP_MODE": bad}), &errw); err == nil {
			t.Errorf("SOP_MODE=%q should be refused", bad)
		}
	}
	opts, closeAll, err = observability(env(map[string]string{"SOP_MODE": "enforce"}), &errw)
	if err != nil || len(opts) != 0 {
		t.Fatalf("SOP_MODE=enforce is the default: opts=%d err=%v", len(opts), err)
	}
	closeAll()
	var modeErr bytes.Buffer
	opts, closeAll, err = observability(env(map[string]string{"SOP_MODE": "observe"}), &modeErr)
	if err != nil || len(opts) != 1 || !strings.Contains(modeErr.String(), "NOTHING IS BLOCKED") {
		t.Fatalf("SOP_MODE=observe should add one option and warn: opts=%d err=%v warn=%q", len(opts), err, modeErr.String())
	}
	closeAll()

	logPath := filepath.Join(t.TempDir(), "decisions.jsonl")
	opts, closeAll, err = observability(env(map[string]string{
		"SOP_RETRY_LIMIT":  "3",
		"SOP_DECISION_LOG": logPath,
		"SOP_METRICS_ADDR": "127.0.0.1:0",
	}), &errw)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	if len(opts) != 3 {
		t.Fatalf("got %d options, want 3", len(opts))
	}
	if !strings.Contains(errw.String(), "metrics on http://127.0.0.1:") {
		t.Errorf("startup line missing: %q", errw.String())
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("decision log file was not created: %v", err)
	}
	if strings.Contains(errw.String(), "not loopback") {
		t.Error("a loopback address should not warn")
	}

	// The server answers with the Prometheus text format.
	start := strings.Index(errw.String(), "http://")
	url := strings.Fields(errw.String()[start:])[0]
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "# TYPE joltrin_step_duration_seconds histogram") {
		t.Errorf("metrics endpoint: status %d body %q", resp.StatusCode, body)
	}

	var wide bytes.Buffer
	_, closeWide, err := observability(env(map[string]string{"SOP_METRICS_ADDR": "0.0.0.0:0"}), &wide)
	if err != nil {
		t.Fatal(err)
	}
	closeWide()
	if !strings.Contains(wide.String(), "not loopback") {
		t.Errorf("an all-interfaces address should warn: %q", wide.String())
	}

	if _, _, err := observability(env(map[string]string{"SOP_METRICS_ADDR": "not an address"}), &errw); err == nil {
		t.Error("a bad SOP_METRICS_ADDR should be refused")
	}
}
