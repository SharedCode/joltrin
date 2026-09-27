package teststubs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// MustMarshalPriorityPayload marshals a registry payload slice (test helper).
func MustMarshalPriorityPayload(payload []sop.RegistryPayload[sop.Handle]) []byte {
	b, _ := json.Marshal(payload)
	return b
}

// PriorityLogFileExists returns true if tid .plg file exists under base/active/log.
func PriorityLogFileExists(base string, tid sop.UUID) bool {
	active := filepath.Join(base, "active", "log")
	fi, err := os.Stat(filepath.Join(active, tid.String()+".plg"))
	return err == nil && !fi.IsDir()
}

// FilePriorityLog is a simple filesystem-backed priority log for tests.
// It stores one file per transaction ID with raw payload bytes.
type FilePriorityLog struct {
	BaseDir string
	Enabled bool
}

func (f *FilePriorityLog) IsEnabled() bool { return f.Enabled }
func (f *FilePriorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error {
	if err := os.MkdirAll(f.logDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.logDir(), tid.String()+".plg"), payload, 0o644)
}
func (f *FilePriorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (f *FilePriorityLog) Remove(ctx context.Context, tid sop.UUID) error {
	_ = os.Remove(filepath.Join(f.logDir(), tid.String()+".plg"))
	return nil
}
func (f *FilePriorityLog) LogCommitChanges(context.Context, []sop.StoreInfo, []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// GetBatch returns up to batchSize tids with dummy handle payload to satisfy rollback path.
func (f *FilePriorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	if !f.Enabled {
		return nil, nil
	}
	dirs, err := os.ReadDir(f.logDir())
	if err != nil {
		return nil, nil
	}
	res := []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{}
	for _, de := range dirs {
		if de.IsDir() || filepath.Ext(de.Name()) != ".plg" {
			continue
		}
		idStr := de.Name()[:len(de.Name())-4]
		if tid, err := sop.ParseUUID(idStr); err == nil {
			res = append(res, sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{
				{IDs: []sop.Handle{{LogicalID: tid, Version: 1}}},
			}})
		}
		if len(res) >= batchSize {
			break
		}
	}
	if len(res) == 0 {
		return nil, nil
	}
	return res, nil
}

func (f *FilePriorityLog) logDir() string { return filepath.Join(f.BaseDir, "active", "log") }

// AgeAll rewrites mtime in the log directory backwards by d (to force age eligibility).
func (f *FilePriorityLog) AgeAll(d time.Duration) {
	entries, _ := os.ReadDir(f.logDir())
	past := time.Now().Add(-d)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fp := filepath.Join(f.logDir(), e.Name())
		_ = os.Chtimes(fp, past, past)
	}
}
