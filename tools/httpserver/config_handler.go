package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	log "log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/fs"
	"github.com/sharedcode/joltrin/internal/logsafe"
	"github.com/sharedcode/joltrin/tools/confighub"
)

// handleListEnvironments returns a list of JSON config files in the current directory.
func handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir(".")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read directory: %v", err), http.StatusInternalServerError)
		return
	}

	var sets []string
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
			// Basic heuristic: check if it has "system_db" or "databases" field?
			// For now, simpler: just list all single-level JSONs or assume naming convention?
			// Let's just list all JSONs for now, user can pick.
			sets = append(sets, f.Name())
		}
	}
	json.NewEncoder(w).Encode(sets)
}

// handleCreateEnvironment creates a new empty JSON config file and switches to it.
func handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	// Debug: Dump raw body
	bodyBytes, _ := io.ReadAll(r.Body)
	log.Debug(logsafe.V(fmt.Sprintf("RAW INIT DB PAYLOAD: %s", string(bodyBytes))))

	// Refill body for decoder
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Debug hex dump to find source of corruption
	if req.Name != "" {
		log.Debug(logsafe.V(fmt.Sprintf("DEBUG-NAME-HEX (CreateEnv): Name='%s', Hex=%x", req.Name, []byte(req.Name))))
	}

	validName, err := validateEnvironmentFilename(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Name = validName

	// Ensure .json extension
	filename := req.Name
	if filepath.Ext(filename) != ".json" {
		filename += ".json"
	}

	// Check conflict
	if _, err := os.Stat(filename); err == nil {
		http.Error(w, "Environment already exists. Please choose a different name.", http.StatusConflict)
		return
	}

	// Initialize config in MEMORY ONLY.
	// We do NOT write to disk yet to avoid creating empty "abandoned" files if the user cancels the wizard.
	config = Config{
		Port:       8080,
		Databases:  []DatabaseConfig{},
		ConfigFile: filename,
	}
	modelCatalog = defaultModelCatalog()

	// Reload agents (will be empty)
	if err := initAgents(context.Background()); err != nil {
		http.Error(w, fmt.Sprintf("Failed to initialize agents: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Environment prepared (in-memory) and active",
		"file":    filename,
	})
}

// handleSwitchEnvironment loads a specific JSON config file into memory.
func handleSwitchEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Filename string `json:"filename"`
	}
	// Debug: Dump raw body
	bodyBytes, _ := io.ReadAll(r.Body)
	log.Debug(logsafe.V(fmt.Sprintf("RAW INIT DB PAYLOAD: %s", string(bodyBytes))))

	// Refill body for decoder
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	validFilename, err := validateEnvironmentFilename(req.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Filename = validFilename

	// Verify it exists
	if _, err := os.Stat(req.Filename); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if err := loadConfig(req.Filename); err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Re-initialize agents to pick up new database configuration
	if err := initAgents(context.Background()); err != nil {
		http.Error(w, fmt.Sprintf("Failed to initialize agents: %v", err), http.StatusInternalServerError)
		return
	}

	// Force update ConfigFile tracker
	config.ConfigFile = req.Filename
	if _, err := loadModelCatalog(req.Filename); err != nil {
		http.Error(w, fmt.Sprintf("Failed to load model catalog: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Switched to " + req.Filename})
}

// handleDeleteEnvironment deletes a configuration file and optionally its data.
func handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Filename       string `json:"filename"`
		DeleteData     bool   `json:"delete_data"` // Legacy
		DeleteSystemDB bool   `json:"delete_system_db"`
		DeleteUserDBs  bool   `json:"delete_user_dbs"`
	}
	// Debug: Dump raw body
	bodyBytes, _ := io.ReadAll(r.Body)
	log.Debug(logsafe.V(fmt.Sprintf("RAW DELETE ENV PAYLOAD: %s", string(bodyBytes))))

	// Refill body for decoder
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	validFilename, err := validateEnvironmentFilename(req.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Filename = validFilename

	// 1. Read config to find paths
	// If any delete flag is true
	shouldDeleteSystem := req.DeleteSystemDB || req.DeleteData
	shouldDeleteUsers := req.DeleteUserDBs || req.DeleteData

	if shouldDeleteSystem || shouldDeleteUsers {
		f, err := os.Open(req.Filename)
		if err == nil {
			var targetConfig Config
			if err := json.NewDecoder(f).Decode(&targetConfig); err == nil {
				// System DB
				if shouldDeleteSystem && targetConfig.SystemDB != nil && targetConfig.SystemDB.Path != "" {
					if err := database.Remove(r.Context(), targetConfig.SystemDB.Path); err != nil {
						log.Warn(logsafe.V(fmt.Sprintf("Failed to remove SystemDB path %s: %v", targetConfig.SystemDB.Path, err)))
					}
				}
				// User DBs
				if shouldDeleteUsers {
					for _, db := range targetConfig.Databases {
						if db.Path != "" {
							if err := database.Remove(r.Context(), db.Path); err != nil {
								log.Warn(logsafe.V(fmt.Sprintf("Failed to remove UserDB path %s: %v", db.Path, err)))
							}
						}
					}
				}
			}
			f.Close()
		}
	}

	// 2. Delete Config File
	if err := os.Remove(req.Filename); err != nil && !os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("Failed to remove config file: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Environment deleted successfully"})
}

// handleSaveConfig implementation moved to config_save_handler.go

// handleInitDatabase initializes a database folder structure and writes configuration.
func handleInitDatabase(w http.ResponseWriter, r *http.Request) {
	log.Debug("TRACE: handleInitDatabase called")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name            string   `json:"name"`
		Path            string   `json:"path"`
		Type            string   `json:"type"`
		Connection      string   `json:"connection"`
		PopulateDemo    bool     `json:"populate_demo"`
		RegistryHashMod int      `json:"registry_hash_mod"`
		StoresFolders   []string `json:"stores_folders"`
		UseSharedDB     bool     `json:"use_shared_db"`
		ErasureConfigs  []struct {
			Key          string   `json:"key"`
			DataChunks   int      `json:"data_chunks"`
			ParityChunks int      `json:"parity_chunks"`
			BasePaths    []string `json:"base_paths"`
		} `json:"erasure_configs"`
	}

	// Debug: Dump raw body
	bodyBytes, _ := io.ReadAll(r.Body)
	log.Debug(logsafe.V(fmt.Sprintf("RAW INIT DB PAYLOAD: %s", string(bodyBytes))))

	// Refill body for decoder
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Debug hex dump to find source of corruption
	if req.Path != "" {
		log.Debug(logsafe.V(fmt.Sprintf("DEBUG-PATH-HEX (InitDatabase): Path='%s', Hex=%x", req.Path, []byte(req.Path))))
	}

	// Sanitize paths
	req.Path = sanitizePath(req.Path)
	for i, sf := range req.StoresFolders {
		req.StoresFolders[i] = sanitizePath(sf)
	}
	for i := range req.ErasureConfigs {
		for j, bp := range req.ErasureConfigs[i].BasePaths {
			req.ErasureConfigs[i].BasePaths[j] = sanitizePath(bp)
		}
	}

	// VALIDATE ERASURE CODING ISOLATION
	// Ensure strict isolation and validity of EC paths before touching the disk.
	checkErasureIsolation := func(basePaths []string, data, parity int, context string) error {
		if len(basePaths) != data+parity {
			return fmt.Errorf("%s: BasePaths count (%d) must equal DataChunks + ParityChunks (%d)", context, len(basePaths), data+parity)
		}
		seen := make(map[string]struct{})
		for _, p := range basePaths {
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			if _, exists := seen[abs]; exists {
				return fmt.Errorf("%s: BasePaths must be unique for isolation. Duplicate path found: %s", context, p)
			}
			seen[abs] = struct{}{}
		}
		return nil
	}

	for _, ec := range req.ErasureConfigs {
		if err := checkErasureIsolation(ec.BasePaths, ec.DataChunks, ec.ParityChunks, fmt.Sprintf("Erasure Config (Key: %s)", ec.Key)); err != nil {
			log.Error(logsafe.V(fmt.Sprintf("TRACE: Validation Failed (Erasure): %v", err)))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	// VALIDATION START
	{
		log.Debug("TRACE: Starting Safety Validation")
		newPaths := []string{}
		// Deduplication logic for User DB (same as System DB fix)
		seenPaths := make(map[string]struct{})

		addPath := func(p string) {
			if p == "" {
				return
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			if _, exists := seenPaths[abs]; !exists {
				seenPaths[abs] = struct{}{}
				newPaths = append(newPaths, p)
			}
		}

		if req.Path != "" {
			addPath(req.Path)
		}
		for _, sf := range req.StoresFolders {
			addPath(sf)
		}
		for _, ec := range req.ErasureConfigs {
			for _, bp := range ec.BasePaths {
				addPath(bp)
			}
		}

		alreadyConfigured := collectAllConfiguredPaths(req.Name)
		if err := validatePathSafety(newPaths, alreadyConfigured); err != nil {
			log.Error(logsafe.V(fmt.Sprintf("TRACE: Validation Failed (PathSafety): %v", err)))
			http.Error(w, fmt.Sprintf("Path Safety Error: %v", err), http.StatusBadRequest)
			return
		}

		// Check Write Permissions for ALL paths provided
		if err := validateWritePermissions(newPaths); err != nil {
			log.Error(logsafe.V(fmt.Sprintf("TRACE: Validation Failed (WritePermissions): %v", err)))
			http.Error(w, fmt.Sprintf("Write Permission Error: %v", err), http.StatusBadRequest)
			return
		}
		log.Debug("TRACE: Safety Validation Passed")
	}
	// VALIDATION END

	if req.Path == "" || req.Name == "" {
		http.Error(w, "Database path and name are required", http.StatusBadRequest)
		return
	}

	// Construct Options
	storeFolders := []string{req.Path}
	seen := make(map[string]bool)
	seen[req.Path] = true
	if len(req.StoresFolders) > 0 {
		for _, folder := range req.StoresFolders {
			if !seen[folder] {
				storeFolders = append(storeFolders, folder)
				seen[folder] = true
			}
		}
	}

	if req.RegistryHashMod == 0 {
		req.RegistryHashMod = fs.MinimumModValue
	}

	log.Info(logsafe.V(fmt.Sprintf("InitUserDB: Path='%s', Name='%s', StoresFolders=%v", req.Path, req.Name, storeFolders)))

	options := sop.DatabaseOptions{
		StoresFolders:        storeFolders,
		RegistryHashModValue: req.RegistryHashMod,
	}

	if len(req.ErasureConfigs) > 0 {
		options.ErasureConfig = make(map[string]sop.ErasureCodingConfig)
		for _, ec := range req.ErasureConfigs {
			// Log the key properly, marking empty string clearly
			keyLog := ec.Key
			if keyLog == "" {
				keyLog = "<EMPTY_STRING>"
			}
			log.Info(logsafe.V(fmt.Sprintf("Processing Erasure Config: Key='%s', Data=%d, Parity=%d, Paths=%v", keyLog, ec.DataChunks, ec.ParityChunks, ec.BasePaths)))

			// Sanitize key if it comes in as explicitly quoted empty string
			finalKey := ec.Key
			if finalKey == `""` {
				finalKey = ""
			}

			options.ErasureConfig[finalKey] = sop.ErasureCodingConfig{
				DataShardsCount:             ec.DataChunks,
				ParityShardsCount:           ec.ParityChunks,
				BaseFolderPathsAcrossDrives: ec.BasePaths,
			}
		}
	}

	if req.Type == "clustered" {
		options.Type = sop.Clustered
		options.CacheType = sop.Redis
		redisUrl := req.Connection
		if redisUrl == "" {
			redisUrl = "localhost:6379"
		}
		options.RedisConfig = &sop.RedisCacheConfig{
			Address: redisUrl,
		}
	} else {
		options.Type = sop.Standalone
	}

	// ErasureConfig is already populated above before this block

	// Check for existing DB files to determine state (Shared vs New)
	dbOptionsPath := filepath.Join(req.Path, "dboptions.json")
	// Note: User DBs don't strictly have reghashmod.txt at root necessarily if it's purely a store,
	// but standard init creates it. We'll check dboptions.json as the primary indicator.
	_, errOpts := os.Stat(dbOptionsPath)
	hasDBOptions := !os.IsNotExist(errOpts)

	// Relaxation for User DB Reuse: Check "db" subfolder if root is missing options and user requested reuse
	if req.UseSharedDB && !hasDBOptions {
		subDBPath := filepath.Join(req.Path, "db")
		if _, err := os.Stat(filepath.Join(subDBPath, "dboptions.json")); err == nil {
			req.Path = subDBPath
			hasDBOptions = true
			log.Info(logsafe.V(fmt.Sprintf("Relaxed User DB Path: '%s' -> '%s'", filepath.Dir(req.Path), req.Path)))

			// Also update the storeFolders if they were just set to default root
			// Logic above was: storeFolders = []string{req.Path}
			// But req.Path changed.
			// Re-synch only if it was indeed just the path.
			// However, storeFolders might contain other drives.
			// For simplicity in this common case (single folder user DB), we assume the user meant the subfolder for the main store.
			// But strictly speaking, if we shift the path, we might need to shift the first element of StoreFolders.
			if len(storeFolders) > 0 && storeFolders[0] == filepath.Dir(req.Path) {
				storeFolders[0] = req.Path
				options.StoresFolders = storeFolders
			}
		}
	}

	shouldSetup := true

	if req.UseSharedDB {
		// User wants to reuse existing DB
		if hasDBOptions {
			shouldSetup = false
			log.Info(logsafe.V(fmt.Sprintf("Shared User DB detected at '%s'. Reusing...", req.Path)))

			// Check Write Permissions
			testFile := filepath.Join(req.Path, ".sop_write_test")
			if f, err := os.Create(testFile); err != nil {
				http.Error(w, fmt.Sprintf("User DB path exists but is not writable: %v", err), http.StatusBadRequest)
				return
			} else {
				f.Close()
				os.Remove(testFile)
			}
			// We don't necessarily load options here because we are about to just AddDatabase to the runtime,
			// which happens via config. However, database.Setup checks are skipped.
		} else {
			http.Error(w, fmt.Sprintf("Shared User DB selected but 'dboptions.json' is missing in '%s'.", req.Path), http.StatusBadRequest)
			return
		}
	} else {
		// New DB requested
		if hasDBOptions {
			http.Error(w, fmt.Sprintf("Destination path '%s' already contains a 'dboptions.json'. Enable 'Shared Mode' to reuse it or choose a clean path.", req.Path), http.StatusBadRequest)
			return
		}
	}
	if entries, err := os.ReadDir(req.Path); err == nil && len(entries) > 0 {
		http.Error(w, fmt.Sprintf("Destination path '%s' is not empty. Cannot create a fresh database here as it may corrupt existing data.", req.Path), http.StatusBadRequest)
		return
	}

	// 1. Setup Database (Creates folders, writes dboptions.json)
	// This uses the official SOP setup routine.
	ctx := r.Context()
	if shouldSetup {
		if _, err := os.Stat(req.Path); err == nil {
			log.Warn(logsafe.V(fmt.Sprintf("TRACE: WARNING - Target path '%s' ALREADY EXISTS before Setup! (Possible race or external creation)", req.Path)))
		} else {
			log.Debug(logsafe.V(fmt.Sprintf("TRACE: Target path '%s' does not exist. Safe to create.", req.Path)))
		}

		log.Debug(logsafe.V(fmt.Sprintf("TRACE: Executing database.Setup for UserDB at '%s'", req.Path)))
		if _, err := database.Setup(ctx, options); err != nil {
			// If the database is already setup (e.g. valid retry), legitimate warning but we can proceed
			// This branch might not be reached given the checks above, but kept for robustness against race/parallel
			if !strings.Contains(err.Error(), "already setup") {
				http.Error(w, fmt.Sprintf("Failed to setup database: %v", err), http.StatusInternalServerError)
				return
			}
			log.Warn(logsafe.V(fmt.Sprintf("Database setup check: %v. Proceeding to populate/init...", err)))
		}
	}

	// 2. Initialize Registry (Trigger reghashmod.txt creation)
	// We use the provided options to initialize the database instance.

	if req.PopulateDemo && shouldSetup {
		if err := PopulateDemoData(ctx, options); err != nil {
			http.Error(w, fmt.Sprintf("Failed to populate demo data: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		// Start a transaction to force initialization of the registry
		tx, err := database.BeginTransaction(ctx, options, sop.ForWriting)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to begin transaction: %v", err), http.StatusInternalServerError)
			return
		}

		comparer := func(a, b string) int {
			return cmp.Compare(a, b)
		}

		// Open a dummy store to force repository initialization
		_, err = database.NewBtree[string, string](ctx, options, "system_check", tx, comparer)
		if err != nil {
			tx.Rollback(ctx)
			http.Error(w, fmt.Sprintf("Failed to initialize database registry: %v", err), http.StatusInternalServerError)
			return
		}

		// We don't add anything.
		if err := tx.Commit(ctx); err != nil {
			http.Error(w, fmt.Sprintf("Failed to commit initialization transaction: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Add to global config and save
	newDB := DatabaseConfig{
		Name:          req.Name,
		Path:          req.Path,
		StoresFolders: req.StoresFolders,
		Mode:          "standalone", // Default to standalone for now
	}

	// Persist Erasure Coding settings
	var ecs []ErasureConfigEntry
	for _, val := range req.ErasureConfigs {
		ecs = append(ecs, ErasureConfigEntry{
			Key:          val.Key,
			DataChunks:   val.DataChunks,
			ParityChunks: val.ParityChunks,
			BasePaths:    val.BasePaths,
		})
	}
	newDB.ErasureConfigs = ecs

	if req.Type == "clustered" {
		newDB.Mode = "clustered"
		newDB.RedisURL = req.Connection
		if newDB.RedisURL == "" {
			newDB.RedisURL = "localhost:6379"
		}
	}

	// Check if exists
	exists := false
	for i, db := range config.Databases {
		if db.Name == newDB.Name {
			config.Databases[i] = newDB
			exists = true
			break
		}
	}
	if !exists {
		config.Databases = append(config.Databases, newDB)
	}

	// Save Config
	if config.ConfigFile != "" {
		f, err := os.Create(config.ConfigFile)
		if err == nil {
			defer f.Close()
			encoder := json.NewEncoder(f)
			encoder.SetIndent("", "    ")
			encoder.Encode(config)
		} else {
			log.Error(fmt.Sprintf("Failed to save config after db init: %v", err))
		}
	}

	// Reload agents to reflect configuration changes
	if err := initAgents(context.Background()); err != nil {
		http.Error(w, fmt.Sprintf("Failed to initialize agents: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Database initialized successfully"})
}

// handleValidatePath checks if a path is valid/writable
func handleValidatePath(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Check if exists
	info, err := os.Stat(path)
	exists := err == nil
	isDir := exists && info.IsDir()

	// Check writability (try to create a temp file)
	writable := false
	if exists && isDir {
		tmpFile := filepath.Join(path, ".write_test")
		if f, err := os.Create(tmpFile); err == nil {
			f.Close()
			os.Remove(tmpFile)
			writable = true
		}
	} else if !exists {
		// Check if parent is writable
		parent := filepath.Dir(path)
		if _, err := os.Stat(parent); err == nil {
			tmpFile := filepath.Join(parent, ".write_test")
			if f, err := os.Create(tmpFile); err == nil {
				f.Close()
				os.Remove(tmpFile)
				writable = true
			}
		}
	}

	// Check for System DB artifacts
	hasDBOptions := false
	hasRegHashMod := false
	if isDir {
		hasDBOptions, hasRegHashMod = database.IsDatabasePath(path)

		// Relaxation: Check 'system_db' subfolder
		if !hasDBOptions {
			if exists, mod := database.IsDatabasePath(filepath.Join(path, "system_db")); exists {
				hasDBOptions = true
				hasRegHashMod = mod
			}
		}

		// Debug Validation
		log.Info(logsafe.V(fmt.Sprintf("ValidatePath: Path='%s', DBOptions=%v, RegHashMod=%v", path, hasDBOptions, hasRegHashMod)))
	}

	json.NewEncoder(w).Encode(map[string]any{
		"exists":        exists,
		"isDir":         isDir,
		"writable":      writable,
		"hasDBOptions":  hasDBOptions,
		"hasRegHashMod": hasRegHashMod,
	})
}

// handleUninstallSystem removes configuration and optionally data files
func handleUninstallSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeleteData     bool `json:"delete_data"` // Legacy
		DeleteSystemDB bool `json:"delete_system_db"`
		DeleteUserDBs  bool `json:"delete_user_dbs"`
	}
	// Debug: Dump raw body
	bodyBytes, _ := io.ReadAll(r.Body)
	log.Debug(logsafe.V(fmt.Sprintf("RAW UNINSTALL PAYLOAD: %s", string(bodyBytes))))

	// Refill body for decoder
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	shouldDeleteSystem := req.DeleteSystemDB || req.DeleteData
	shouldDeleteUsers := req.DeleteUserDBs || req.DeleteData

	if shouldDeleteSystem {
		http.Error(w, "Deleting the System DB is disabled because it stores session records and runtime metadata for the current environment.", http.StatusBadRequest)
		return
	}

	// 1. Delete actual data folders if requested
	if shouldDeleteSystem || shouldDeleteUsers {
		// System DB
		if shouldDeleteSystem && config.SystemDB != nil && config.SystemDB.Path != "" {
			if err := safeRemoveAll(config.SystemDB.Path); err != nil {
				log.Error(logsafe.V(fmt.Sprintf("Failed to remove system db path %s: %v", config.SystemDB.Path, err)))
				// Continue anyway to try cleaning up others
			}
		}
		// User DBs
		if shouldDeleteUsers {
			for _, db := range config.Databases {
				if db.Path != "" {
					if err := safeRemoveAll(db.Path); err != nil {
						log.Error(logsafe.V(fmt.Sprintf("Failed to remove db path %s: %v", db.Path, err)))
					}
				}
			}
		}
	}

	// 2. Clear Config Object (in memory)
	config = Config{}

	// 3. Remove Config File (on disk)
	// We check the default locations (config.json) or whatever was passed flag.
	// But in handleSaveConfig we used config.ConfigFile
	configFile := config.ConfigFile
	if configFile == "" {
		configFile = "config.json"
	}
	if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("Failed to remove config file: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "System uninstalled successfully"})
}

func resolveConfigRelativePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	configFile := config.ConfigFile
	// config.ConfigFile usually holds the full path the user provided or the one we loaded.
	// But it could be just "config.json" if they ran from the same directory lacking a full path.
	if configFile == "" {
		configFile = "config.json"
	}
	if configDir, err := filepath.Abs(filepath.Dir(configFile)); err == nil {
		return filepath.Join(configDir, p)
	}
	return p
}

// validateEnvironmentFilename ensures a client-supplied environment config
// filename is a bare filename in the working directory (where
// handleListEnvironments reads them from), never a path that could escape
// it. Environment files are always flat JSONs in ".", so there is no
// legitimate reason for this value to contain a path separator or "..".
func validateEnvironmentFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename %q", name)
	}
	if filepath.IsAbs(name) || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid filename %q: must be a bare filename with no path separators", name)
	}
	return name, nil
}

// sanitizePath removes leading/trailing whitespace and non-graphic characters
// to prevent "invisible" or garbage characters from creating bad folder names.
func sanitizePath(p string) string {
	// 1. Remove non-graphic characters (e.g. control codes, zero-width spaces)
	p = strings.Map(func(r rune) rune {
		if unicode.IsGraphic(r) {
			return r
		}
		return -1
	}, p)

	// 2. Trim whitespace
	p = strings.TrimSpace(p)

	// 3. Trim specific garbage characters user might inadvertently paste (e.g. cursor, pipe)
	// Example: "db2/|"
	p = strings.TrimRight(p, "|")

	// 4. Final trim in case stripping left whitespace
	cleanP := filepath.Clean(strings.TrimSpace(p))
	return resolveConfigRelativePath(cleanP)
}

// collectAllConfiguredPaths gathers all paths currently in use by the system and other databases,
// excluding the database currently being edited/added (by name).
func collectAllConfiguredPaths(excludeDBName string) []string {
	var paths []string

	// System DB
	// Check if we should include System DB paths.
	// We include them ONLY if we are NOT currently editing the System DB.
	includeSystemDB := (excludeDBName != SystemDBName)

	if includeSystemDB && config.SystemDB != nil && config.SystemDB.Path != "" {
		// Double check: if the configured name matches the exclusion name, skip it.
		if config.SystemDB.Name != excludeDBName {
			paths = append(paths, config.SystemDB.Path)
			paths = append(paths, config.SystemDB.StoresFolders...)
			for _, ec := range config.SystemDB.ErasureConfigs {
				paths = append(paths, ec.BasePaths...)
			}
		}
	}

	// User DBs
	for _, db := range config.Databases {
		if db.Name == excludeDBName {
			continue
		}
		if db.Path != "" {
			paths = append(paths, db.Path)
		}
		paths = append(paths, db.StoresFolders...)
		for _, ec := range db.ErasureConfigs {
			paths = append(paths, ec.BasePaths...)
		}
	}
	return paths
}

// validatePathSafety checks for conflicts between a set of new paths and existing system paths.
// Policy: Strict Isolation.
// We do NOT allow different databases to share the same physical paths (e.g. EC drives).
// Sharing drives creates a Single Point of Failure (SPOF) where one drive failure
// could panic/degrade multiple databases simultaneously.
// This function detects:
// 1. Internal conflicts (e.g. store folder inside db folder in the new set)
// 2. External conflicts (e.g. new db folder inside existing system db folder)
func validatePathSafety(newPaths []string, existingPaths []string) error {
	// Clean and filter new paths
	cleanNew := make([]string, 0, len(newPaths))
	for _, np := range newPaths {
		if np == "" {
			continue
		}
		abs, err := filepath.Abs(np)
		if err != nil {
			return fmt.Errorf("invalid path '%s': %v", np, err)
		}
		cleanNew = append(cleanNew, abs)
	}

	// Clean and filter existing paths
	cleanExisting := make([]string, 0, len(existingPaths))
	for _, ep := range existingPaths {
		if ep == "" {
			continue
		}
		abs, err := filepath.Abs(ep)
		if err == nil {
			cleanExisting = append(cleanExisting, abs)
		}
	}

	// 1. Check External Conflicts (new vs existing)
	for _, np := range cleanNew {
		for _, ep := range cleanExisting {
			if conflict, msg := isPathConflict(np, ep); conflict {
				return fmt.Errorf("conflict with existing path: %s", msg)
			}
		}
	}

	// 2. Check Internal Conflicts (within the new set)
	for i := 0; i < len(cleanNew); i++ {
		for j := i + 1; j < len(cleanNew); j++ {
			if conflict, msg := isPathConflict(cleanNew[i], cleanNew[j]); conflict {
				return fmt.Errorf("internal path conflict: %s", msg)
			}
		}
	}

	return nil
}

func isPathConflict(pathA, pathB string) (bool, string) {
	// Case-Insensitive Check for robustness (Mac/Windows)
	pathALower := strings.ToLower(pathA)
	pathBLower := strings.ToLower(pathB)

	if pathALower == pathBLower {
		return true, fmt.Sprintf("paths are identical: '%s'", pathA)
	}

	// Ensure trailing separator for prefix check to avoid /tmp matching /tmp2
	pathASep := pathALower
	if !strings.HasSuffix(pathASep, string(os.PathSeparator)) {
		pathASep += string(os.PathSeparator)
	}
	pathBSep := pathBLower
	if !strings.HasSuffix(pathBSep, string(os.PathSeparator)) {
		pathBSep += string(os.PathSeparator)
	}

	// Check if A is inside B
	if strings.HasPrefix(pathASep, pathBSep) {
		return true, fmt.Sprintf("'%s' is inside '%s'", pathA, pathB)
	}
	// Check if B is inside A
	if strings.HasPrefix(pathBSep, pathASep) {
		return true, fmt.Sprintf("'%s' is inside '%s'", pathB, pathA)
	}

	return false, ""
}

func sanitizedConfigForDisk(cfg Config) Config {
	sanitized := cfg

	// The legacy root-password field is no longer the runtime credential source.
	// Persist only the seeded user records and clear any raw password fallback.
	sanitized.RootPassword = ""

	return sanitized
}

// saveConfig writes the current configuration to the file specified in config.ConfigFile.
func saveConfig() error {
	if config.ConfigFile == "" {
		config.ConfigFile = confighub.ResolveConfigPath("")
	}

	payload, err := json.Marshal(sanitizedConfigForDisk(config))
	if err != nil {
		return fmt.Errorf("failed to encode config for save: %w", err)
	}

	var shared confighub.Config
	if err := json.Unmarshal(payload, &shared); err != nil {
		return fmt.Errorf("failed to convert config for save: %w", err)
	}

	return confighub.SaveConfig(config.ConfigFile, shared)
}

// validateWritePermissions checks if the application can write to the specified paths
// by attempting to create a temporary file in the target directory or its first existing ancestor.
func validateWritePermissions(paths []string) error {
	for _, p := range paths {
		if p == "" {
			continue
		}
		// Find first existing ancestor
		dir := p
		for {
			info, err := os.Stat(dir)
			if err == nil {
				if !info.IsDir() {
					return fmt.Errorf("path '%s' exists but is not a directory", dir)
				}
				// Try creating a temp file in this directory
				testFile, err := os.CreateTemp(dir, ".sop_perm_check_")
				if err != nil {
					return fmt.Errorf("write permission denied for path '%s' (checked ancestor '%s'): %v", p, dir, err)
				}
				testFile.Close()
				os.Remove(testFile.Name())
				break
			} else if os.IsNotExist(err) {
				// Move up
				parent := filepath.Dir(dir)
				if parent == dir {
					// Hit root or cannot go up
					return fmt.Errorf("path '%s' or its ancestors do not exist or are inaccessible", p)
				}
				dir = parent
			} else {
				// Other error (permission to stat?)
				return fmt.Errorf("cannot access path '%s': %v", dir, err)
			}
		}
	}
	return nil
}
