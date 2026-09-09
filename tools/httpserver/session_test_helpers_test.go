package main

import (
	"sync"
	"testing"
)

func withIsolatedSessionStore(t *testing.T) {
	t.Helper()

	oldConfig := config
	oldFacade := tokenFacade

	tmpDir := t.TempDir()
	config = Config{
		SystemDB: &DatabaseConfig{
			Name: SystemDBName,
			Path: tmpDir,
			Mode: "standalone",
		},
	}
	tokenFacade = nil
	tokenFacadeOnce = sync.Once{}

	t.Cleanup(func() {
		config = oldConfig
		tokenFacade = oldFacade
		if oldFacade == nil {
			tokenFacadeOnce = sync.Once{}
		}
	})
}
