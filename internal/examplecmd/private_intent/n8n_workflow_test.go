package privateintent

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const workflowPath = "../../../examples/private_intent/n8n/private-intent-record.workflow.json"

// The workflow is a file people import into n8n. These checks keep it valid and
// keep secrets and surprise destinations out of it.
func TestN8nWorkflowExportIsSafeAndWired(t *testing.T) {
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Nodes []struct {
			Name        string                    `json:"name"`
			Type        string                    `json:"type"`
			Parameters  map[string]any            `json:"parameters"`
			Credentials map[string]map[string]any `json:"credentials"`
		} `json:"nodes"`
		Connections map[string]struct {
			Main [][]struct {
				Node string `json:"node"`
			} `json:"main"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range wf.Nodes {
		if names[n.Name] {
			t.Errorf("duplicate node name %q", n.Name)
		}
		names[n.Name] = true
	}
	for from, c := range wf.Connections {
		if !names[from] {
			t.Errorf("connection from unknown node %q", from)
		}
		for _, outs := range c.Main {
			for _, o := range outs {
				if !names[o.Node] {
					t.Errorf("%q connects to unknown node %q", from, o.Node)
				}
			}
		}
	}
	for _, n := range wf.Nodes {
		if n.Type != "n8n-nodes-base.httpRequest" {
			continue
		}
		url, _ := n.Parameters["url"].(string)
		if !strings.HasPrefix(url, "http://127.0.0.1:8787/v1/") {
			t.Errorf("%s calls %q: the example may only call the local gateway", n.Name, url)
		}
		if c := n.Credentials["httpHeaderAuth"]; c == nil || c["name"] != "Joltrin gateway" {
			t.Errorf("%s must use the named Header Auth credential, not an inline token", n.Name)
		}
		if h, ok := n.Parameters["headerParameters"]; ok {
			t.Errorf("%s sets headers inline: %v", n.Name, h)
		}
	}
	text := string(raw)
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)bearer\s+[0-9a-f]{16,}`),
		regexp.MustCompile(`[0-9a-f]{64}`),
		regexp.MustCompile(`BEGIN PGP (PRIVATE|PUBLIC)`),
		regexp.MustCompile(`(?i)"(token|password|apiKey|secret)"\s*:`),
	} {
		if bad.MatchString(text) {
			t.Errorf("workflow export matches secret pattern %s", bad)
		}
	}
}
