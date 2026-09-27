package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

var (
	lockSectorRetryTimeoutDuration = 3 * 60 * time.Second
)

var zeroSector = bytes.Repeat([]byte{0}, sop.HandleSizeInBytes)

// updateFileRegion marshals each handle and writes it into the correct position within its block.
func (hm *hashmap) updateFileRegion(ctx context.Context, fileRegionDetails []fileRegionDetails) error {
	m := encoding.NewHandleMarshaler()
	buffer := make([]byte, 0, sop.HandleSizeInBytes)
	for _, frd := range fileRegionDetails {
		ba2, _ := m.Marshal(frd.handle, buffer)
		if err := hm.updateFileBlockRegion(ctx, frd.dio, frd.blockOffset, int(frd.handleInBlockOffset), ba2); err != nil {
			return err
		}
	}
	return nil
}

// markDeleteFileRegion zeroes out the handle-sized region inside a block to mark deletion.
// This results in visually clean zeroed sectors and keeps logic simple.
func (hm *hashmap) markDeleteFileRegion(ctx context.Context, fileRegionDetails []fileRegionDetails) error {
	// Study whether we want to zero out only the "Logical ID" part. For now, zero out entire Handle block
	// which could aid in cleaner deleted blocks(as marked w/ all zeroes). Negligible difference in IO.
	for _, frd := range fileRegionDetails {

		log.Debug(fmt.Sprintf("marking deleted file %s, sector offset %v, offset in block %v", frd.dio.filename, frd.blockOffset, frd.handleInBlockOffset))
		if err := hm.updateFileBlockRegion(ctx, frd.dio, frd.blockOffset, int(frd.handleInBlockOffset), zeroSector); err != nil {
			return err
		}
	}
	return nil
}

func (hm *hashmap) lockFileBlockRegionWithRetry(ctx context.Context, dio *fileDirectIO, offset int64) (*sop.LockKey, error) {
	var lk *sop.LockKey
	var err error
	var ok bool

	startTime := sop.Now()
	var tid sop.UUID
	for {
		ok, tid, lk, err = hm.lockFileBlockRegion(ctx, dio, offset)
		if err != nil {
			return nil, err
		}
		if ok {
			return lk, nil
		}
		if err := sop.TimedOut(ctx, "lockFileBlockRegionWithRetry", startTime, lockSectorRetryTimeoutDuration); err != nil {
			// If the context is canceled or the operation's context deadline was exceeded, return the raw error
			// so callers treat it as a normal timeout/cancellation and NOT a failover trigger.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			// Otherwise, convert to a lock acquisition failure to allow callers to attempt
			// stale-lock recovery (e.g., priority rollback) using the lock key in UserData.
			err = fmt.Errorf("lockFileBlockRegionWithRetry(%v) failed: %w", offset, err)
			log.Debug(err.Error())
			lk.LockID = tid
			return nil, sop.Error{
				Code:     sop.LockAcquisitionFailure,
				Err:      err,
				UserData: lk,
			}
		}
		sop.RandomSleep(ctx)
	}
}

// updateFileBlockRegion acquires a cache-backed lock for the target block region, reads the block,
// merges the handle data, writes back, and finally releases the lock. Retries acquiring the lock
// until timeout to avoid deadlocks across writers.
func (hm *hashmap) updateFileBlockRegion(ctx context.Context, dio *fileDirectIO, blockOffset int64, handleInBlockOffset int, handleData []byte) error {
	// Lock the block file region.
	lk, err := hm.lockFileBlockRegionWithRetry(ctx, dio, blockOffset)
	if err != nil {
		return err
	}
	defer hm.unlockFileBlockRegion(ctx, lk)

	alignedBuffer := dio.createAlignedBlock()

	if err := hm.readAndRestoreBlock(ctx, dio, blockOffset, alignedBuffer); err != nil {
		return err
	}

	return hm.writeBlockRegionPayload(ctx, dio, blockOffset, handleInBlockOffset, handleData, alignedBuffer)
}

func (hm *hashmap) lockFileBlockRegion(ctx context.Context, dio *fileDirectIO, offset int64) (bool, sop.UUID, *sop.LockKey, error) {
	tid := hm.replicationTracker.tid
	if tid == sop.NilUUID {
		tid = sop.NewUUID()
	}

	s := hm.formatLockKey(dio.filename, offset)
	lk := hm.cache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{
		{
			First:  s,
			Second: tid,
		},
	})
	ok, uuid, err := hm.cache.DualLock(ctx, LockFileRegionDuration, lk)
	return ok, uuid, lk[0], err
}

func (hm *hashmap) unlockFileBlockRegion(ctx context.Context, lk *sop.LockKey) error {
	return hm.cache.Unlock(ctx, []*sop.LockKey{lk})
}

// findAndAdd searches for a slot for the given handle and updates it atomically under a block lock.
// It uses optimistic concurrency: finds a candidate slot, locks it, verifies it's still available, and writes.
func (hm *hashmap) findAndAdd(ctx context.Context, filename string, handle sop.Handle) error {
	log.Debug("entering findAndAdd")
	if filename == "" {
		return fmt.Errorf("can't findAndAdd on empty filename")
	}
	// Lock the "Logical Slot" (the "bucket" determined by the hash) to prevent race conditions.
	// This serializes access to the same logical slot across all segment files.
	blockOffset, handleInBlockOffset := hm.getBlockOffsetAndHandleInBlockOffset(handle.LogicalID)
	s := hm.formatLockKey(filename, blockOffset+handleInBlockOffset)
	tid := hm.replicationTracker.tid
	if tid == sop.NilUUID {
		tid = sop.NewUUID()
	}
	lk := hm.cache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{
		{
			First:  s,
			Second: tid,
		},
	})

	// Retry loop to acquire the lock.
	startTime := sop.Now()
	var ownerTID sop.UUID
	for {
		var ok bool
		var err error
		ok, ownerTID, err = hm.cache.DualLock(ctx, LockFileRegionDuration, lk)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if err := sop.TimedOut(ctx, "findAndAdd lock acquisition", startTime, lockSectorRetryTimeoutDuration); err != nil {
			// If the context is canceled or the operation's context deadline was exceeded, return the raw error
			// so callers treat it as a normal timeout/cancellation and NOT a failover trigger.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			// Otherwise, convert to a lock acquisition failure to allow callers to attempt
			// stale-lock recovery (e.g., priority rollback) using the lock key in UserData.
			err = fmt.Errorf("findAndAdd lock acquisition(%v) failed: %w", s, err)
			log.Debug(err.Error())
			lk[0].LockID = ownerTID
			return sop.Error{
				Code:     sop.LockAcquisitionFailure,
				Err:      err,
				UserData: lk,
			}
		}
		sop.RandomSleep(ctx)
	}
	defer hm.cache.Unlock(ctx, lk)

	// Retry loop to acquire the lock on physical address.
	var frd fileRegionDetails
	var physicallockKey []*sop.LockKey
	startTime = sop.Now()
	for {
		var ok bool
		var err error

		// 1. Find a candidate slot (read-only, no lock yet).
		frd, err = hm.findOneFileRegion(ctx, true, filename, handle.LogicalID)
		if err != nil {
			return err
		}
		s = hm.formatLockKey(filename, frd.blockOffset+frd.handleInBlockOffset)
		// Check if nobody has a lock on it.
		if frd.handle.IsEmpty() {
			if frd.blockOffset == blockOffset && frd.handleInBlockOffset == handleInBlockOffset {
				physicallockKey = nil
				break
			}
			physicallockKey = hm.cache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{
				{
					First:  s,
					Second: tid,
				},
			})

			ok, ownerTID, err = hm.cache.DualLock(ctx, LockFileRegionDuration, physicallockKey)
			log.Debug("after DualLock call on", "address", s)
			if err != nil {
				return err
			}
			if ok {
				log.Debug("before break")
				break
			}
		}
		if err := sop.TimedOut(ctx, "findAndAdd lock acquisition", startTime, lockSectorRetryTimeoutDuration); err != nil {
			// If the context is canceled or the operation's context deadline was exceeded, return the raw error
			// so callers treat it as a normal timeout/cancellation and NOT a failover trigger.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			// Otherwise, convert to a lock acquisition failure to allow callers to attempt
			// stale-lock recovery (e.g., priority rollback) using the lock key in UserData.
			err = fmt.Errorf("findAndAdd lock acquisition(%v) failed: %w", s, err)
			log.Debug(err.Error())
			if len(physicallockKey) > 0 {
				physicallockKey[0].LockID = ownerTID
			}
			return sop.Error{
				Code:     sop.LockAcquisitionFailure,
				Err:      err,
				UserData: physicallockKey,
			}
		}
		sop.RandomSleep(ctx)
	}
	if physicallockKey != nil {
		defer hm.cache.Unlock(ctx, physicallockKey)
	}

	// 2. Write.
	m := encoding.NewHandleMarshaler()
	var buf [sop.HandleSizeInBytes]byte
	b, _ := m.Marshal(handle, buf[:0])

	return hm.updateFileBlockRegion(ctx, frd.dio, frd.blockOffset, int(frd.handleInBlockOffset), b)
}

// readAndRestoreBlock reads the block.
func (hm *hashmap) readAndRestoreBlock(ctx context.Context, dio *fileDirectIO, blockOffset int64, alignedBuffer []byte) error {
	if n, err := dio.readAt(ctx, alignedBuffer, blockOffset); n != blockSize || err != nil {
		if err == nil {
			return fmt.Errorf("only partially (n=%d) read the block at offset %v", n, blockOffset)
		}
		// If read failed, we might still want to check COW?
		// For now, propagate error as we can't verify checksum of unread data.
		return err
	}

	// Verify checksum
	if _, err := unmarshalData(alignedBuffer); err == nil {
		// Valid block. Check for stale COW and delete it.
		_ = hm.deleteCow(ctx, dio.file.Name(), blockOffset)
		return nil
	}

	// Check for COW file first
	cowData, shouldRestore, err := hm.checkCow(ctx, dio.file.Name(), blockOffset)
	if err != nil {
		return err
	}
	if shouldRestore {
		return hm.restoreFromCow(ctx, dio, blockOffset, alignedBuffer, cowData)
	}

	return nil
}

// writeBlockRegionPayload updates the buffer with handleData and writes to disk.
func (hm *hashmap) writeBlockRegionPayload(ctx context.Context, dio *fileDirectIO, blockOffset int64, handleInBlockOffset int, handleData []byte, alignedBuffer []byte) error {
	// Create COW backup
	if err := hm.createCow(ctx, dio.file.Name(), blockOffset, alignedBuffer); err != nil {
		return err
	}

	// Merge the updated Handle record & Add Checksum on the end.
	copy(alignedBuffer[handleInBlockOffset:handleInBlockOffset+sop.HandleSizeInBytes], handleData)
	marshalData(alignedBuffer[:blockSize-4], alignedBuffer)

	// Write to main file
	if n, err := dio.writeAt(ctx, alignedBuffer, blockOffset); n != blockSize || err != nil {
		if err == nil {
			return fmt.Errorf("only partially (n=%d) wrote at block offset %v", n, blockOffset)
		}
		return err
	}

	// Delete COW backup
	return hm.deleteCow(ctx, dio.file.Name(), blockOffset)
}

func (hm *hashmap) formatLockKey(filename string, offset int64) string {
	s := hm.replicationTracker.formatActiveFolderEntity(filename)
	return hm.cache.FormatLockKey(fmt.Sprintf("%s%s%v", lockFileRegionKeyPrefix, s, offset))
}
