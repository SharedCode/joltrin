package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/obfuscation"
)

func TestCopilotAgent_Execute_Deobfuscation(t *testing.T) {
	// Setup
	agent := &CopilotAgent{
		registry: NewRegistry(),
		databases: map[string]sop.DatabaseOptions{
			"testdb": {EnableObfuscation: true},
		},
	}

	// We'll use a known string.
	original := "my_secret_table"
	// We manually obfuscate it to simulate input from LLM
	obfuscated := obfuscation.GlobalObfuscator.Obfuscate(original, "STORE")

	// If obfuscator is no-op (e.g. in test environment if not initialized),
	// we can't strictly verify the fix, but we can verify the code path runs.
	// However, GlobalObfuscator usually works if imported.

	args := map[string]any{
		"table": obfuscated,
	}

	ctx := context.Background()
	payload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// Execute
	// We expect "unknown tool" error, but lastToolCall should be set.
	agent.Execute(ctx, "some_tool", args)

	if agent.lastToolCall == nil {
		t.Fatal("lastToolCall should be set")
	}

	val, ok := agent.lastToolCall.Args["table"].(string)
	if !ok {
		t.Fatal("table arg missing or not string")
	}

	// If Obfuscate changes string:
	// original != obfuscated.
	// If we de-obfuscate first: val == original.
	// If we save first: val == obfuscated.

	if original != obfuscated {
		if val != original {
			t.Errorf("Expected de-obfuscated value '%s', got '%s'. The lastToolCall should contain the de-obfuscated arguments.", original, val)
		}
	} else {
		t.Log("Obfuscator is no-op, skipping strict check")
	}
}

func TestCopilotAgent_Execute_Deobfuscation_Nested(t *testing.T) {
	// Setup
	agent := &CopilotAgent{
		registry: NewRegistry(),
		databases: map[string]sop.DatabaseOptions{
			"testdb": {EnableObfuscation: true},
		},
	}

	// We'll use a known string.
	original := "my_secret_table"
	// We manually obfuscate it to simulate input from LLM
	obfuscated := obfuscation.GlobalObfuscator.Obfuscate(original, "STORE")

	args := map[string]any{
		"tables": []any{obfuscated, "other_table"},
		"config": map[string]any{
			"target": obfuscated,
		},
	}

	ctx := context.Background()
	payload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// Execute
	agent.Execute(ctx, "some_tool", args)

	if agent.lastToolCall == nil {
		t.Fatal("lastToolCall should be set")
	}

	// Check Slice
	tables, ok := agent.lastToolCall.Args["tables"].([]any)
	if !ok {
		t.Fatal("tables arg missing or not slice")
	}

	if original != obfuscated {
		if tables[0] != original {
			t.Errorf("Expected de-obfuscated slice item '%s', got '%s'", original, tables[0])
		}
	}

	// Check Map
	config, ok := agent.lastToolCall.Args["config"].(map[string]any)
	if !ok {
		t.Fatal("config arg missing or not map")
	}

	if original != obfuscated {
		if config["target"] != original {
			t.Errorf("Expected de-obfuscated map value '%s', got '%s'", original, config["target"])
		}
	}
}
