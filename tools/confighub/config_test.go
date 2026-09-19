package confighub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPath_PrefersExplicitPath(t *testing.T) {
	got := ResolveConfigPath("/tmp/custom.json")
	if got != "/tmp/custom.json" {
		t.Fatalf("ResolveConfigPath() = %q, want %q", got, "/tmp/custom.json")
	}
}

func TestFindExistingConfigFile_FindsConfigInWorkingDir(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	got := FindExistingConfigFile("")
	want, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		t.Fatal(err)
	}
	gotClean, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotClean != want {
		t.Fatalf("FindExistingConfigFile() = %q, want %q", gotClean, want)
	}
}

func TestLoadConfig_ResolvesRelativePaths(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
		"databases": [
			{
				"name": "db1",
				"path": "./data",
				"stores_folders": ["./stores"],
				"erasure_configs": [
					{"key": "zone1", "base_paths": ["./ec1", "./ec2"]}
				]
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got := cfg.Databases[0].Path; got != filepath.Join(tmp, "data") {
		t.Fatalf("LoadConfig() database path = %q, want %q", got, filepath.Join(tmp, "data"))
	}
	if got := cfg.Databases[0].StoresFolders[0]; got != filepath.Join(tmp, "stores") {
		t.Fatalf("LoadConfig() store folder = %q, want %q", got, filepath.Join(tmp, "stores"))
	}
	// ErasureConfigs.BasePaths went through the same resolveDatabaseConfig
	// code path as Path/StoresFolders above but had no test coverage of
	// its own before this - the reflection-based walk treats it as a
	// third, separately-handled field.
	basePaths := cfg.Databases[0].ErasureConfigs[0].BasePaths
	if basePaths[0] != filepath.Join(tmp, "ec1") || basePaths[1] != filepath.Join(tmp, "ec2") {
		t.Fatalf("LoadConfig() erasure base paths = %v, want [%q %q]",
			basePaths, filepath.Join(tmp, "ec1"), filepath.Join(tmp, "ec2"))
	}
}

// TestResolveConfigRelativePath covers the shared helper directly: an
// absolute path passes through unchanged, an empty path stays empty rather
// than resolving to configDir itself, and surrounding whitespace is
// trimmed before either check - all three DatabaseConfig fields that carry
// filesystem paths (Path, StoresFolders entries, ErasureConfigs[].BasePaths
// entries) go through this one function via resolveDatabaseConfig, so a
// bug here would affect all three the same way.
func TestResolveConfigRelativePath(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		configDir string
		want      string
	}{
		{"relative path joins configDir", "./data", "/cfg/dir", filepath.Join("/cfg/dir", "data")},
		{"absolute path passes through unchanged", "/abs/data", "/cfg/dir", "/abs/data"},
		{"empty path stays empty, not configDir", "", "/cfg/dir", ""},
		{"whitespace is trimmed before resolving", "  ./data  ", "/cfg/dir", filepath.Join("/cfg/dir", "data")},
		{"whitespace-only path stays empty", "   ", "/cfg/dir", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveConfigRelativePath(c.path, c.configDir); got != c.want {
				t.Errorf("resolveConfigRelativePath(%q, %q) = %q, want %q", c.path, c.configDir, got, c.want)
			}
		})
	}
}
