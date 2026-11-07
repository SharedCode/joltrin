package governance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrAuditTruncated is returned when the log no longer ends at the anchor hash.
var ErrAuditTruncated = errors.New("governance: audit log is shorter than the anchor (events removed from the end)")

// FileAuditRecorder is a MemoryAuditRecorder that also appends every event to a
// JSON Lines file, so the chain survives a restart. Open verifies the chain it
// reads back and refuses to start on a log that fails verification, including a
// half-written last line. It does not rotate, encrypt or ship the file; see
// docs/SECURITY_CONTROLS.md.
type FileAuditRecorder struct {
	*MemoryAuditRecorder
	fmu  sync.Mutex
	file *os.File
}

// OpenFileAuditRecorder opens or creates the log at path with mode 0600 and
// replays it line by line. The events stay in memory after the replay, like
// MemoryAuditRecorder. The path is the caller's own configuration, as with
// os.OpenFile. A path with a ".." element is refused as a guard against
// traversal; resolve it yourself first if you need one. The signer, if any, signs new events only.
func OpenFileAuditRecorder(path string, signer AuditSigner) (*FileAuditRecorder, error) {
	return OpenFileAuditRecorderAnchored(path, signer, "")
}

// OpenFileAuditRecorderAnchored is OpenFileAuditRecorder plus a check that the
// log still ends at anchorHash, a value from LastHash() that you stored
// somewhere an attacker with write access to the log cannot reach. If the
// newest events were cut off the end of the file, the last hash no longer
// matches and the open fails with ErrAuditTruncated. An empty anchor skips the
// check, which is what OpenFileAuditRecorder does. Only files are opened: a
// directory, device or pipe at path is refused. An existing file is replayed
// before it is opened for writing, so a file that is not a valid audit chain,
// such as a system log, fails to open and is left untouched.
func OpenFileAuditRecorderAnchored(path string, signer AuditSigner, anchorHash string) (*FileAuditRecorder, error) {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return nil, fmt.Errorf("governance: audit log path %q must not contain \"..\"", path)
		}
	}
	if fi, err := os.Stat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("governance: audit log %s is not a regular file", path)
	}
	mem := NewMemoryAuditRecorder(signer)

	if rf, err := os.Open(path); err == nil {
		defer rf.Close()
		sc := bufio.NewScanner(rf)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			var ev AuditEvent
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
				return nil, fmt.Errorf("governance: audit log %s line %d: %w", path, line, err)
			}
			mem.events = append(mem.events, &ev)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("governance: read audit log %s: %w", path, err)
		}
		if err := mem.VerifyIntegrity(); err != nil {
			return nil, err
		}
		if n := len(mem.events); n > 0 {
			mem.lastHash = mem.events[n-1].Hash
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if anchorHash != "" && mem.lastHash != anchorHash {
		return nil, fmt.Errorf("%w: log ends at %q, anchor is %q", ErrAuditTruncated, mem.lastHash, anchorHash)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &FileAuditRecorder{MemoryAuditRecorder: mem, file: f}, nil
}

// Record adds the event to the chain and writes it to disk before returning.
// If the write fails the event is removed from memory again, so memory never
// holds an event the file does not.
func (r *FileAuditRecorder) Record(ctx context.Context, ev *AuditEvent) error {
	r.fmu.Lock()
	defer r.fmu.Unlock()

	prevHash := func() string {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.lastHash
	}()

	if err := r.MemoryAuditRecorder.Record(ctx, ev); err != nil {
		return err
	}

	r.mu.RLock()
	stored := r.events[len(r.events)-1]
	r.mu.RUnlock()

	line, err := json.Marshal(stored)
	if err == nil {
		_, err = r.file.Write(append(line, '\n'))
	}
	if err == nil {
		err = r.file.Sync()
	}
	if err != nil {
		r.mu.Lock()
		r.events = r.events[:len(r.events)-1]
		r.lastHash = prevHash
		r.mu.Unlock()
		return fmt.Errorf("governance: write audit log: %w", err)
	}
	return nil
}

// LastHash returns the hash of the newest event, or "" for an empty log. Store
// it outside the log's reach and pass it to OpenFileAuditRecorderAnchored.
func (r *FileAuditRecorder) LastHash() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastHash
}

// Close closes the recorder and the file.
func (r *FileAuditRecorder) Close() error {
	r.fmu.Lock()
	defer r.fmu.Unlock()
	_ = r.MemoryAuditRecorder.Close()
	return r.file.Close()
}
