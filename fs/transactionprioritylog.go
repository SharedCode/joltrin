package fs

import (
	"context"
	"fmt"
	log "log/slog"
	"os"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

const (
	priorityLogFileExtension = ".plg"
	priorityLogMinAgeInMin   = 5
)

// priorityLog persists per-transaction payloads that guide prioritized replication/work.
// Files are stored under the active folder with a .plg extension and are aged before batching.
type priorityLog struct {
	replicationTracker *replicationTracker
	tid                sop.UUID
}

// IsEnabled reports whether priority logging is enabled.
func (l priorityLog) IsEnabled() bool { return true }

// Add writes the priority log payload for a transaction.
func (l priorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error {
	filename := l.replicationTracker.formatActiveFolderEntity(fmt.Sprintf("%s%c%s%s", logFolder, os.PathSeparator, tid.String(), priorityLogFileExtension))
	fio := NewFileIO()

	// Append checksum to the payload.
	blob := make([]byte, len(payload)+4)
	marshalData(payload, blob)

	// Write to file
	return fio.WriteFile(ctx, filename, blob, permission)
}

// LogCommitChanges persists commit-change metadata used when reinstating failed drives.
func (l priorityLog) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {
	return l.replicationTracker.logCommitChanges(ctx, l.tid, stores, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles)
}

// Get loads the priority log payload for a transaction, if present.
func (l priorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	filename := l.replicationTracker.formatActiveFolderEntity(fmt.Sprintf("%s%c%s%s", logFolder, os.PathSeparator, tid.String(), priorityLogFileExtension))
	fio := NewFileIO()
	if !fio.Exists(ctx, filename) {
		return nil, nil
	}
	ba, err := fio.ReadFile(ctx, filename)
	if err != nil {
		return nil, err
	}

	payload, err := unmarshalData(ba)
	if err != nil {
		return nil, err
	}

	var data []sop.RegistryPayload[sop.Handle]
	if err := encoding.DefaultMarshaler.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// GetBatch returns up to batchSize oldest priority log entries ready for processing.
// Entries are considered ready when their last-modified time is older than
// priorityLogMinAgeInMin from the current hour (capped to the hour).
func (l priorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	mh, _ := time.Parse(DateHourLayout, sop.Now().Format(DateHourLayout))
	cappedHour := mh.Add(-time.Duration(priorityLogMinAgeInMin * time.Minute))
	ignoreAge := false
	if v := ctx.Value(sop.ContextPriorityLogIgnoreAge); v != nil {
		if b, ok := v.(bool); ok && b {
			ignoreAge = true
		}
	}

	f := func(de os.DirEntry) bool {
		info, _ := de.Info()
		filename := info.Name()
		if _, err := sop.ParseUUID(filename[0 : len(filename)-len(priorityLogFileExtension)]); err != nil {
			return false
		}
		if ignoreAge {
			return true
		}
		fts := info.ModTime().Format(DateHourLayout)
		ft, _ := time.Parse(DateHourLayout, fts)
		return cappedHour.Compare(ft) >= 0
	}

	fn := l.replicationTracker.formatActiveFolderEntity(logFolder)
	fio := NewFileIO()
	if !fio.Exists(ctx, fn) {
		if err := fio.MkdirAll(ctx, fn, permission); err != nil {
			log.Warn(fmt.Sprintf("error creating %s, details: %v", fn, err))
		}
		return nil, nil
	}
	files, err := getFilesSortedDescByModifiedTime(ctx, fn, priorityLogFileExtension, f)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	if batchSize <= 0 {
		batchSize = 25
	}

	res := make([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], 0, batchSize)
	for i := 0; i < len(files) && i < batchSize; i++ {
		filename := files[i].Name()
		tid, te := sop.ParseUUID(filename[0 : len(filename)-len(priorityLogFileExtension)])
		if te != nil {
			log.Warn("file %s does not belong in this folder, details: %s", filename, te)
			continue
		}
		r, e := l.Get(ctx, tid)
		if e != nil {
			return res, e
		}
		res = append(res, sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{Key: tid, Value: r})
	}
	return res, nil
}

// ProcessNewer iterates over all priority logs newer than 5 mins and invokes the processor callback for each.
func (l priorityLog) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	cutoff := sop.Now().Add(-time.Duration(priorityLogMinAgeInMin) * time.Minute)
	f := func(de os.DirEntry) bool {
		info, _ := de.Info()
		if info.ModTime().Before(cutoff) {
			return false
		}
		filename := info.Name()
		_, err := sop.ParseUUID(filename[0 : len(filename)-len(priorityLogFileExtension)])
		return err == nil
	}

	fn := l.replicationTracker.formatActiveFolderEntity(logFolder)
	fio := NewFileIO()
	if !fio.Exists(ctx, fn) {
		return nil
	}
	files, err := getFilesSortedDescByModifiedTime(ctx, fn, priorityLogFileExtension, f)
	if err != nil || len(files) == 0 {
		return err
	}

	for i := 0; i < len(files); i++ {
		filename := files[i].Name()
		tid, te := sop.ParseUUID(filename[0 : len(filename)-len(priorityLogFileExtension)])
		if te != nil {
			log.Warn("file %s does not belong in this folder, details: %s", filename, te)
			continue
		}
		r, e := l.Get(ctx, tid)
		if e != nil {
			return e
		}
		if err := processor(tid, r); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes the priority log for a transaction, if present.
func (l priorityLog) Remove(ctx context.Context, tid sop.UUID) error {
	fio := NewFileIO()
	filename := l.replicationTracker.formatActiveFolderEntity(fmt.Sprintf("%s%c%s%s", logFolder, os.PathSeparator, tid.String(), priorityLogFileExtension))
	if fio.Exists(ctx, filename) {
		return fio.Remove(ctx, filename)
	}
	return nil
}
