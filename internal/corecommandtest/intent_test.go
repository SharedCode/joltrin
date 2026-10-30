package corecommandtest

import "testing"

func TestPrivateIntentCommandAliases(t *testing.T) {
	checkAliases(t, "private_intent",
		aliasCase{name: "simulated demo", args: []string{"demo"}, contains: []string{"SIMULATED delete", "All steps simulated. No real system was touched."}},
		aliasCase{name: "missing initialization directory", args: []string{"init"}, exit: 1, contains: []string{"usage: private_intent init <dir>"}},
		aliasCase{name: "unknown command", args: []string{"unsupported"}, exit: 1, contains: []string{"unknown command"}},
		aliasCase{name: "initialize local keys", args: []string{"init", "pi"}},
	)
}
