package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withCleanConfig(t *testing.T) {
	t.Helper()
	saved := config
	config = Config{}
	t.Cleanup(func() { config = saved })
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestBootstrapRoot_CreatesConfigWithRootUser(t *testing.T) {
	withCleanConfig(t)
	path := filepath.Join(t.TempDir(), "config.json")
	created, err := bootstrapRootFromEnv(envOf(map[string]string{"JOLTRIN_ROOT_PASSWORD": "a-long-enough-password"}), path)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if strings.Contains(string(data), "a-long-enough-password") {
		t.Fatal("the plain text password must never be written to the config file")
	}
	if !strings.Contains(string(data), "root") {
		t.Fatal("expected a root user in the written config")
	}
}

func TestBootstrapRoot_NoopWhenUnsetOrPlaceholder(t *testing.T) {
	for _, v := range []string{"", "  ", "unset"} {
		withCleanConfig(t)
		path := filepath.Join(t.TempDir(), "config.json")
		created, err := bootstrapRootFromEnv(envOf(map[string]string{"JOLTRIN_ROOT_PASSWORD": v}), path)
		if err != nil || created {
			t.Fatalf("value %q: created=%v err=%v", v, created, err)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("value %q must not create a config file", v)
		}
	}
}

func TestBootstrapRoot_NeverTouchesAnExistingConfig(t *testing.T) {
	withCleanConfig(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"existing":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := bootstrapRootFromEnv(envOf(map[string]string{"JOLTRIN_ROOT_PASSWORD": "a-long-enough-password"}), path)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != `{"existing":true}` {
		t.Fatalf("existing config was modified: %s", got)
	}
}

func TestBootstrapRoot_RejectsShortPassword(t *testing.T) {
	withCleanConfig(t)
	path := filepath.Join(t.TempDir(), "config.json")
	created, err := bootstrapRootFromEnv(envOf(map[string]string{"JOLTRIN_ROOT_PASSWORD": "short"}), path)
	if err == nil || created {
		t.Fatalf("expected an error for a short password, created=%v err=%v", created, err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("a rejected password must not create a config file")
	}
}

func TestBootstrapRoot_SeededPasswordCanAuthenticate(t *testing.T) {
	withCleanConfig(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := bootstrapRootFromEnv(envOf(map[string]string{"JOLTRIN_ROOT_PASSWORD": "a-long-enough-password"}), path); err != nil {
		t.Fatal(err)
	}
	ok, user, err := config.Authenticate("root", "a-long-enough-password")
	if err != nil || !ok || user == nil {
		t.Fatalf("root should authenticate with the seeded password: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := config.Authenticate("root", "wrong-password-entirely"); ok {
		t.Fatal("a wrong password must not authenticate")
	}
}
