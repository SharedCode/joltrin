package etl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}
	return path
}

func TestRunWorkflow_MissingFileReturnsError(t *testing.T) {
	err := RunWorkflow(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error for a missing workflow file")
	}
	if !strings.Contains(err.Error(), "failed to open workflow file") {
		t.Errorf("expected an 'open workflow file' error, got: %v", err)
	}
}

func TestRunWorkflow_InvalidJSONReturnsError(t *testing.T) {
	path := writeWorkflowFile(t, `{not valid json`)

	err := RunWorkflow(context.Background(), path)
	if err == nil {
		t.Fatal("expected an error for invalid workflow JSON")
	}
	if !strings.Contains(err.Error(), "failed to decode workflow JSON") {
		t.Errorf("expected a 'decode workflow JSON' error, got: %v", err)
	}
}

func TestRunWorkflow_UnknownStepTypeReturnsError(t *testing.T) {
	path := writeWorkflowFile(t, `{"steps":[{"name":"s1","type":"bogus","params":{}}]}`)

	err := RunWorkflow(context.Background(), path)
	if err == nil {
		t.Fatal("expected an error for an unknown step type")
	}
	if !strings.Contains(err.Error(), "unknown step type: bogus") {
		t.Errorf("expected an 'unknown step type' error, got: %v", err)
	}
}

func TestRunWorkflow_PrepareStepRequiresURLAndOut(t *testing.T) {
	cases := []struct {
		name   string
		params string
	}{
		{"missing url", `{"out":"out.json"}`},
		{"missing out", `{"url":"http://example.com/data.csv"}`},
		{"missing both", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWorkflowFile(t, `{"steps":[{"name":"s1","type":"prepare","params":`+tc.params+`}]}`)

			err := RunWorkflow(context.Background(), path)
			if err == nil {
				t.Fatal("expected an error for a prepare step missing required params")
			}
			if !strings.Contains(err.Error(), "missing required params 'url' or 'out'") {
				t.Errorf("expected a missing-params error, got: %v", err)
			}
		})
	}
}

func TestRunWorkflow_IngestStepRequiresConfig(t *testing.T) {
	path := writeWorkflowFile(t, `{"steps":[{"name":"s1","type":"ingest","params":{}}]}`)

	err := RunWorkflow(context.Background(), path)
	if err == nil {
		t.Fatal("expected an error for an ingest step missing config")
	}
	if !strings.Contains(err.Error(), "missing required param 'config'") {
		t.Errorf("expected a missing-config error, got: %v", err)
	}
}

// This proves the dispatch actually reaches IngestAgent (not just the
// validation gate above it): a config path pointing at nothing real
// should fail inside IngestAgent itself, wrapped with the step's name.
func TestRunWorkflow_IngestStepPropagatesUnderlyingFailure(t *testing.T) {
	path := writeWorkflowFile(t, `{"steps":[{"name":"load-agent","type":"ingest","params":{"config":"/no/such/config.json"}}]}`)

	err := RunWorkflow(context.Background(), path)
	if err == nil {
		t.Fatal("expected an error for an ingest step pointing at a nonexistent config")
	}
	if !strings.Contains(err.Error(), "step 'load-agent' failed") {
		t.Errorf("expected the error to name the failing step, got: %v", err)
	}
}

func TestRunWorkflow_StopsAtFirstFailingStep(t *testing.T) {
	// Two steps: the first is a valid-looking ingest step that will fail
	// deep inside IngestAgent, the second is an unknown type that would
	// also fail if reached. If the workflow correctly stops at the first
	// failure, the error must be the ingest failure, not "unknown step
	// type", proving steps after a failure never run.
	path := writeWorkflowFile(t, `{"steps":[
		{"name":"first","type":"ingest","params":{"config":"/no/such/config.json"}},
		{"name":"second","type":"bogus","params":{}}
	]}`)

	err := RunWorkflow(context.Background(), path)
	if err == nil {
		t.Fatal("expected the first step's failure to stop the workflow")
	}
	if strings.Contains(err.Error(), "unknown step type") {
		t.Errorf("workflow ran past the first failing step: %v", err)
	}
	if !strings.Contains(err.Error(), "step 'first' failed") {
		t.Errorf("expected the first step's failure, got: %v", err)
	}
}
