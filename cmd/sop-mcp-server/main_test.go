package main

import (
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
