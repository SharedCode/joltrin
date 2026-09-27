package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestAgentMemoryWorkflow(t *testing.T) {
	store := inmemory.NewBtree[string, string](true)

	// Test 1: Checkpoint creation
	frame := AgentMemoryFrame{
		AgentID:    "agent-test-01",
		SessionID:  "session-test",
		StepIndex:  1,
		Prompt:     "Test prompt",
		Reasoning:  "Test reasoning step",
		Status:     "COMMITTED",
		VectorDims: 128,
		Embedding:  generateMockEmbedding(1, 128),
	}
	bytes, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Failed to marshal frame: %v", err)
	}

	key := fmt.Sprintf("mem:%s:step:01", frame.AgentID)
	if !store.Add(key, string(bytes)) {
		t.Fatalf("Failed to add frame to B-Tree")
	}

	// Test 2: Retrieval and verification
	if !store.Find(key, true) {
		t.Fatalf("Failed to find frame %s", key)
	}
	val := store.GetCurrentValue()
	var restored AgentMemoryFrame
	if err := json.Unmarshal([]byte(val), &restored); err != nil {
		t.Fatalf("Failed to unmarshal restored frame: %v", err)
	}
	if restored.Reasoning != "Test reasoning step" {
		t.Fatalf("Restored reasoning mismatch: got %s", restored.Reasoning)
	}

	// Test 3: Vector similarity calculation
	vec1 := generateMockEmbedding(1, 128)
	vec2 := generateMockEmbedding(1, 128)
	sim := cosineSimilarity(vec1, vec2)
	if sim < 0.9999 {
		t.Fatalf("Expected identical vector similarity ~1.0, got %f", sim)
	}
}
