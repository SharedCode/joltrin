package agent

import (
	"encoding/json"
	"strings"

	"github.com/sharedcode/joltrin/ai"
)

// HydrateToolCallsFromMockText bridges old baseline text-based JSON tool calls
// to the new Native ToolCall format. This ensures that MockGenerators can run
// seamlessly under NativeReActEngine.
func HydrateToolCallsFromMockText(text string) []ai.ToolCall {
	clean := strings.TrimSpace(text)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	var call struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(clean), &call); err == nil && call.Tool != "" {
		return []ai.ToolCall{{Name: call.Tool, Args: call.Args}}
	}
	return nil
}
