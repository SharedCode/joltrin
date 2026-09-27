package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// ReinstateFailedDrives can be invoked after replacing the failed drives so they can be synchronized with
// the current Active targets' Stores' contents.
//
// High-level flow:
//  1. Enable commit-change logging and persist replication status
//  2. Copy store repositories and registry segment files to passive targets
//  3. Fast-forward by reading commit logs and applying to passive stores/registries until none remain
//  4. Turn replication back on and sync replication status to L2 cache
//  5. Run fast-forward again to catch any late logs
func (r *replicationTracker) ReinstateFailedDrives(ctx context.Context) error {
	if !r.replicate {
		return fmt.Errorf("replicationTracker.replicate flag is off, ReinstateFailedDrives is valid only if this is on")
	}
	if !r.ReplicationTrackedDetails.FailedToReplicate {
		return fmt.Errorf("replicationTracker.FailedToReplicate is false, ReinstateFailedDrives is valid only if this is true")
	}

	if err := r.startLoggingCommitChanges(ctx); err != nil {
		return err
	}
	if err := r.copyStores(ctx); err != nil {
		return err
	}
	// Consume all Commit logs.
	for {
		if fileFound, err := r.fastForward(ctx); err != nil {
			return err
		} else if !fileFound {
			break
		}
	}
	// Turn on Replication.
	if err := r.turnOnReplication(ctx); err != nil {
		return err
	}
	// Check fast forward log one last time.
	for {
		if fileFound, err := r.fastForward(ctx); err != nil {
			return err
		} else if !fileFound {
			return nil
		}
	}
}

// startLoggingCommitChanges enables logging of commit changes and persists the replication status
// both on disk and in the L2 cache.
func (r *replicationTracker) startLoggingCommitChanges(ctx context.Context) error {
	globalReplicationDetailsLocker.Lock()
	defer globalReplicationDetailsLocker.Unlock()

	GlobalReplicationDetails.LogCommitChanges = true
	r.LogCommitChanges = true

	// Update the replication status details.
	if err := r.writeReplicationStatus(ctx, r.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		return err
	}
	return r.syncWithL2Cache(ctx, true)
}

// copyStores copies the active store repository and registry files to the passive folders.
func (r *replicationTracker) copyStores(ctx context.Context) error {
	if sr, err := NewStoreRepository(ctx, r, nil, r.l2Cache, 0); err != nil {
		return err
	} else {
		// Copy Store Repositories & Registries to passive folders being reinstated for replication.
		return sr.CopyToPassiveFolders(ctx)
	}
}

// fastForward reads commit logs, applies changes to passive targets, and deletes each processed log.
// Returns true if any logs were found and processed.
func (r *replicationTracker) fastForward(ctx context.Context) (bool, error) {
	// Read the transaction commit logs then sync the passive stores/registries w/ the values from active stores/regs.
	//   - In case StoreRepository exists in target, use the StoreRepository timestamp to determine if target needs to get updated.
	//     If missing in passive target then add one from active.
	//   - In case Handle exists in target, use Handle version to determine if target needs to get updated.
	//     If missing in passive target then add one from active.
	// Delete after the log file is completely processed.
	// Repeat this until all commit logs files are done processing (all deleted).

	fn := r.formatActiveFolderEntity(commitChangesLogFolder)

	files, err := getFilesSortedDescByModifiedTime(ctx, fn, logFileExtension, nil)
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, nil
	}

	// Set to false the FailedToReplicate so we can issue a successful Replicate call on StoreRepository & Registry.
	r.ReplicationTrackedDetails.FailedToReplicate = false
	fio := NewFileIO()
	ms := NewManageStoreFolder(fio)
	sr, err := NewStoreRepository(ctx, r, ms, r.l2Cache, 0)
	if err != nil {
		return false, err
	}

	regHashMod := 0
	if regHashMod, err = sr.GetRegistryHashModValue(ctx); err != nil {
		return false, err
	}

	reg := NewRegistry(true, regHashMod, r, r.l2Cache)
	var foundAndProcessed bool
	// Get the oldest first.
	for i := range files {
		filename := files[i].Name()
		foundAndProcessed = true

		ffn := r.formatActiveFolderEntity(fmt.Sprintf("%s%c%s", commitChangesLogFolder, os.PathSeparator, filename))
		ba, err := fio.ReadFile(ctx, ffn)
		if err != nil {
			return false, err
		}

		var logData sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]

		err = encoding.DefaultMarshaler.Unmarshal(ba, &logData)
		if err != nil {
			return false, err
		}

		if logData.First != nil {
			// Ensure Store Repo has the latest Count so we don't need to worry about potential race condition
			// between transaction doing commit logs vs. replication to the reinstated drives.
			for i := range logData.First {
				if sis, _ := sr.getFromCache(ctx, logData.First[i].Name); len(sis) == 1 {
					logData.First[i].Count = sis[0].Count
				}
			}
			if err := sr.Replicate(ctx, logData.First); err != nil {
				return false, err
			}
		}
		if err := reg.Replicate(ctx, logData.Second[0], logData.Second[1], logData.Second[2], logData.Second[3]); err != nil {
			return false, err
		}

		if err := fio.Remove(ctx, ffn); err != nil {
			return false, err
		}
	}

	return foundAndProcessed, nil
}

// turnOnReplication finalizes reinstatement: clears failure flags, updates status on disk and in L2 cache.
func (r *replicationTracker) turnOnReplication(ctx context.Context) error {
	globalReplicationDetailsLocker.Lock()
	defer globalReplicationDetailsLocker.Unlock()

	GlobalReplicationDetails.FailedToReplicate = false
	GlobalReplicationDetails.LogCommitChanges = false

	r.ReplicationTrackedDetails = *GlobalReplicationDetails
	// Update the replication status details.
	if err := r.writeReplicationStatus(ctx, r.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		return err
	}
	return r.syncWithL2Cache(ctx, true)
}
