package corecommandtest

import "testing"

func TestRunbookCommandAliases(t *testing.T) {
	t.Run("agent_team", func(t *testing.T) {
		checkAliases(t, "agent_team", aliasCase{name: "stub tool scenarios", contains: []string{"BLOCKED", "trace:", "PD-77 resolved"}})
	})
	t.Run("runbook_order_lab", func(t *testing.T) {
		checkAliases(t, "runbook_order_lab", aliasCase{name: "enumerated orders", contains: []string{"rollback_complete"}})
	})
}
