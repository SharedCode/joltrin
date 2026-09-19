package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/ai"
)

func TestListToolsOutput(t *testing.T) {
	agent := NewCopilotAgent(Config{}, nil, nil)
	ctx := context.Background()
	payload := &ai.SessionPayload{
		CurrentDB: "system",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	agent.registerTools(ctx)

	output, err := agent.Execute(ctx, "list_tools", nil)
	if err != nil {
		t.Fatalf("list_tools failed: %v", err)
	}

	fmt.Println("----- LIST TOOLS OUTPUT START -----")
	fmt.Println(output)
	fmt.Println("----- LIST TOOLS OUTPUT END -----")
}
