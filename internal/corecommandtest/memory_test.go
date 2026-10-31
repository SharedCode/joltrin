package corecommandtest

import "testing"

func TestMemoryCommandAliases(t *testing.T) {
	t.Run("quickstart", func(t *testing.T) {
		checkAliases(t, "quickstart", aliasCase{name: "in-memory demo", contains: []string{"Joltrin quickstart", "added 5 items", "ordered scan:"}})
	})
	t.Run("agent_memory", func(t *testing.T) {
		checkAliases(t, "agent_memory", aliasCase{name: "in-memory demo", contains: []string{"DEMO SUCCESS", "Rank #1", "zero external daemons"}})
	})
}
