package confighub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// ErasureConfigEntry defines a single EC zone configuration.
type ErasureConfigEntry struct {
	Key          string   `json:"key"`
	DataChunks   int      `json:"data_chunks"`
	ParityChunks int      `json:"parity_chunks"`
	BasePaths    []string `json:"base_paths"`
}

// DatabaseConfig holds configuration for a single SOP database.
type DatabaseConfig struct {
	Name              string               `json:"name"`
	Path              string               `json:"path"`
	StoresFolders     []string             `json:"stores_folders,omitempty"`
	Mode              string               `json:"mode"`
	RedisURL          string               `json:"redis"`
	IsSystem          bool                 `json:"is_system,omitempty"`
	Warning           string               `json:"warning,omitempty"`
	ErasureConfigs    []ErasureConfigEntry `json:"erasure_configs,omitempty"`
	EnableObfuscation bool                 `json:"enable_obfuscation,omitempty"`
}

// Config holds the runtime configuration loaded from disk.
type Config struct {
	Port                   int              `json:"port"`
	Databases              []DatabaseConfig `json:"databases"`
	PageSize               int              `json:"pageSize"`
	SystemDB               *DatabaseConfig  `json:"system_db,omitempty"`
	RootPassword           string           `json:"root_password,omitempty"`
	ProductionMode         bool             `json:"production_mode,omitempty"`
	SessionTokenTTLMinutes int              `json:"session_token_ttl_minutes,omitempty"`
	SessionSecret          string           `json:"session_secret,omitempty"`
	AuthProviderName       string           `json:"auth_provider,omitempty"`
	Users                  []any            `json:"users,omitempty"`
}

func ResolveConfigPath(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "config.json")
	}
	return "config.json"
}

func FindExistingConfigFile(explicit string) string {
	seen := map[string]struct{}{}
	for _, candidate := range CandidateConfigPaths(explicit) {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func CandidateConfigPaths(explicit string) []string {
	if p := strings.TrimSpace(explicit); p != "" {
		return []string{p}
	}
	if cwd, err := os.Getwd(); err == nil {
		return []string{filepath.Join(cwd, "config.json")}
	}
	return []string{"config.json"}
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("config path cannot be empty")
	}

	var cfg Config
	if err := loadConfigInto(path, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadConfigInto(path string, cfg any) error {
	if cfg == nil {
		return fmt.Errorf("config target cannot be nil")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return err
	}

	value := reflect.ValueOf(cfg)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return fmt.Errorf("config target must be a non-nil pointer")
	}
	resolveConfigRelativePaths(value.Elem(), filepath.Dir(path))
	return nil
}

func resolveConfigRelativePaths(value reflect.Value, configDir string) {
	if !value.IsValid() {
		return
	}

	for value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}

	if value.Kind() != reflect.Struct {
		return
	}

	if field := value.FieldByName("SystemDB"); field.IsValid() && field.CanAddr() {
		resolveDatabaseConfig(field, configDir)
	}

	if field := value.FieldByName("Databases"); field.IsValid() && field.Kind() == reflect.Slice {
		for i := 0; i < field.Len(); i++ {
			resolveDatabaseConfig(field.Index(i), configDir)
		}
	}
}

func resolveDatabaseConfig(value reflect.Value, configDir string) {
	for value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct || !value.CanAddr() {
		return
	}

	if pathField := value.FieldByName("Path"); pathField.IsValid() && pathField.CanSet() && pathField.Kind() == reflect.String {
		pathField.SetString(resolveConfigRelativePath(pathField.String(), configDir))
	}

	if storesField := value.FieldByName("StoresFolders"); storesField.IsValid() && storesField.Kind() == reflect.Slice {
		for i := 0; i < storesField.Len(); i++ {
			item := storesField.Index(i)
			if item.Kind() == reflect.String {
				item.SetString(resolveConfigRelativePath(item.String(), configDir))
			}
		}
	}

	if ecField := value.FieldByName("ErasureConfigs"); ecField.IsValid() && ecField.Kind() == reflect.Slice {
		for i := 0; i < ecField.Len(); i++ {
			entry := ecField.Index(i)
			if basePaths := entry.FieldByName("BasePaths"); basePaths.IsValid() && basePaths.Kind() == reflect.Slice {
				for j := 0; j < basePaths.Len(); j++ {
					item := basePaths.Index(j)
					if item.Kind() == reflect.String {
						item.SetString(resolveConfigRelativePath(item.String(), configDir))
					}
				}
			}
		}
	}
}

// resolveConfigRelativePath joins path onto configDir when path is relative,
// leaving an absolute path (or an empty one) unchanged. Used for every
// filesystem path a loaded config can carry - the single Path field, each
// StoresFolders entry, and each ErasureConfigEntry.BasePaths entry - so a
// relative value in the config file resolves against the config file's own
// directory rather than the process's current working directory.
func resolveConfigRelativePath(path, configDir string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(configDir, path)
}

func SaveConfig(path string, cfg Config) error {
	if path == "" {
		path = ResolveConfigPath("")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "    ")
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}
