package main

import (
	"fmt"
	"os"
	"strings"
)

// minBootstrapPasswordLen is the shortest root password accepted from the
// environment. The setup wizard only requires a non-empty password because a
// person types it; this one comes from a secret store, so it can be strong.
const minBootstrapPasswordLen = 16

// bootstrapRootFromEnv creates the root admin user from JOLTRIN_ROOT_PASSWORD
// when no config file exists yet, then writes the config so the server starts
// in login mode instead of waiting for a first-run wizard.
//
// Hosted deployments need this: the first-run wizard only accepts loopback
// callers, and a container on a distroless image has no shell to run it from.
// It does nothing once a config file exists, so it can never replace or reset
// an existing root password. The Azure deployment stores the literal "unset"
// in Key Vault when no secret was supplied; that counts as not set.
func bootstrapRootFromEnv(getenv func(string) string, configPath string) (bool, error) {
	password := strings.TrimSpace(getenv("JOLTRIN_ROOT_PASSWORD"))
	if password == "" || password == "unset" {
		return false, nil
	}
	if _, err := os.Stat(configPath); err == nil {
		return false, nil
	}
	if len(password) < minBootstrapPasswordLen {
		return false, fmt.Errorf("JOLTRIN_ROOT_PASSWORD must be at least %d characters", minBootstrapPasswordLen)
	}
	if err := ensureRootUserFromPassword(password); err != nil {
		return false, fmt.Errorf("seed root user: %w", err)
	}
	config.ConfigFile = configPath
	if err := saveConfig(); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}
	return true, nil
}
