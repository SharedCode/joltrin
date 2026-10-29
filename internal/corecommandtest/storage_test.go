package corecommandtest

import "testing"

func TestLocalStorageCommandAliases(t *testing.T) {
	t.Run("relations_demo", func(t *testing.T) {
		checkAliases(t, "relations_demo", aliasCase{name: "disposable local database", contains: []string{"Successfully created stores with Relations and IndexSpecs!"}})
	})
	t.Run("swarm_standalone", func(t *testing.T) {
		// Help compiles both entry points and checks flag parity without running the heavy workload.
		checkAliases(t, "swarm_standalone", aliasCase{name: "help preserves flags", args: []string{"-h"}, contains: []string{"-maxJitter", "Maximum random jitter"}})
	})
}
