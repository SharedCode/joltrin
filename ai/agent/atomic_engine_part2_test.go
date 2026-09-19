package agent

import "testing"

// A prior step's result is commonly stored wrapped as {"value": {...}}
// (see the "value" field used throughout copilottools.*.go and the set-step
// schema in copilottools.script.go). resolveTemplate must fall through to
// that wrapper when the field isn't found directly on the map.
func TestResolveTemplate_FallsThroughToValueWrapper(t *testing.T) {
	e := &ScriptEngine{Context: NewScriptContext()}
	e.Context.Variables["result"] = map[string]any{
		"value": map[string]any{
			"name": "loki",
		},
	}

	got := e.resolveTemplate("{{result.name}}")
	if got != "loki" {
		t.Errorf("resolveTemplate(%q) = %v, want %q", "{{result.name}}", got, "loki")
	}
}

func TestResolveTemplate_DirectFieldTakesPriorityOverValueWrapper(t *testing.T) {
	e := &ScriptEngine{Context: NewScriptContext()}
	e.Context.Variables["result"] = map[string]any{
		"name":  "direct",
		"value": map[string]any{"name": "wrapped"},
	}

	got := e.resolveTemplate("{{result.name}}")
	if got != "direct" {
		t.Errorf("resolveTemplate(%q) = %v, want %q", "{{result.name}}", got, "direct")
	}
}

func TestResolveTemplate_MissingFieldReturnsNil(t *testing.T) {
	e := &ScriptEngine{Context: NewScriptContext()}
	e.Context.Variables["result"] = map[string]any{
		"value": map[string]any{"name": "loki"},
	}

	got := e.resolveTemplate("{{result.missing}}")
	if got != nil {
		t.Errorf("resolveTemplate(%q) = %v, want nil", "{{result.missing}}", got)
	}
}
