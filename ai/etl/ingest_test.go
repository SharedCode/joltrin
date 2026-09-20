package etl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIngestConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}
	return path
}

func TestIngestAgent_MissingConfigFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	err := IngestAgent(context.Background(), path, "", "")
	if err == nil {
		t.Fatal("expected an error for a missing config file")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected a 'failed to load config' error, got: %v", err)
	}
}

func TestIngestAgent_InvalidJSONReturnsError(t *testing.T) {
	path := writeIngestConfig(t, `{not valid json`)

	err := IngestAgent(context.Background(), path, "", "")
	if err == nil {
		t.Fatal("expected an error for invalid config JSON")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected a 'failed to load config' error, got: %v", err)
	}
}

func TestIngestAgent_UnknownTargetAgentIDReturnsError(t *testing.T) {
	path := writeIngestConfig(t, `{"id":"root-agent","name":"Root"}`)

	err := IngestAgent(context.Background(), path, "", "no-such-agent")
	if err == nil {
		t.Fatal("expected an error for a target agent ID absent from both the inline agents list and the root config")
	}
	if !strings.Contains(err.Error(), "agent 'no-such-agent' not found in configuration file") {
		t.Errorf("expected an 'agent not found' error, got: %v", err)
	}
}

func TestIngestAgent_UnknownEmbedderDependencyReturnsError(t *testing.T) {
	path := writeIngestConfig(t, `{
		"id": "root-agent",
		"name": "Root",
		"embedder": {"type": "agent", "agent_id": "missing-dep"}
	}`)

	err := IngestAgent(context.Background(), path, "", "")
	if err == nil {
		t.Fatal("expected an error when the embedder's agent dependency can't be resolved")
	}
	if !strings.Contains(err.Error(), "dependency agent 'missing-dep' not found in inline agents or as a file") {
		t.Errorf("expected a 'dependency agent not found' error, got: %v", err)
	}
}
