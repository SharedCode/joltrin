//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/sharedcode/joltrin/v5/inmemory"
)

// newSeededBtree builds a fresh in-memory B-Tree pre-loaded with the given
// key/value pairs, used to swap in restored state for both the ledger and
// the agent-memory B-Trees.
func newSeededBtree(seed map[string]string) inmemory.BtreeInterface[string, string] {
	bt := inmemory.NewBtree[string, string](true)
	for k, v := range seed {
		bt.Add(k, v)
	}
	return bt
}

// opfsStateFile is the single JSON snapshot this demo persists to OPFS. It is
// not a general-purpose sop.FileIO-over-OPFS implementation: it is just the
// four file operations this demo actually needs (write, read, remove, and
// feature detection), bridged to the async File System Access API in
// index.html. A real sop.FileIO backed by OPFS's synchronous
// createSyncAccessHandle would need to run inside a dedicated Worker; this
// demo intentionally does not do that (see the comment above the JS bridge
// in index.html for why).
const opfsStateFile = "sop-demo-state.json"

// demoSnapshot is the full persisted state of the WASM instance: the
// financial ledger, the vector index, and both agent-memory B-Trees. It gets
// written to OPFS after every mutation and read back once at boot.
type demoSnapshot struct {
	Ledger        map[string]string `json:"ledger"`
	VectorDocs    []VectorDocument  `json:"vectorDocs"`
	AgentFrames   map[string]string `json:"agentFrames"`
	AgentSessions map[string]string `json:"agentSessions"`
	SessionSeq    int               `json:"sessionSeq"`
	AgentSeq      int               `json:"agentSeq"`
}

// awaitPromise blocks the calling goroutine until a JS Promise settles,
// returning its resolved value or an error built from the rejection reason.
// This is the standard syscall/js pattern for bridging async JS APIs into
// Go's synchronous call style: the goroutine parks on a channel receive,
// which yields to the JS event loop until one of the two callbacks fires.
func awaitPromise(promise js.Value) (js.Value, error) {
	resultCh := make(chan js.Value, 1)
	errCh := make(chan error, 1)

	then := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			resultCh <- args[0]
		} else {
			resultCh <- js.Undefined()
		}
		return nil
	})
	defer then.Release()

	catch := js.FuncOf(func(this js.Value, args []js.Value) any {
		reason := "unknown OPFS error"
		if len(args) > 0 {
			reason = args[0].Call("toString").String()
		}
		errCh <- fmt.Errorf("opfs: %s", reason)
		return nil
	})
	defer catch.Release()

	promise.Call("then", then).Call("catch", catch)

	select {
	case v := <-resultCh:
		return v, nil
	case err := <-errCh:
		return js.Undefined(), err
	}
}

// opfsAvailable reports whether the browser exposes navigator.storage.getDirectory.
func opfsAvailable() bool {
	fn := js.Global().Get("opfsSupported")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return false
	}
	return fn.Invoke().Bool()
}

// opfsWriteFile writes data to a single flat file in the origin's private
// file system root via the async File System Access API bridge.
func opfsWriteFile(name string, data []byte) error {
	jsBytes := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(jsBytes, data)
	promise := js.Global().Call("opfsWriteFile", name, jsBytes)
	_, err := awaitPromise(promise)
	return err
}

// opfsReadFile reads a single flat file from the origin's private file
// system root via the async File System Access API bridge.
func opfsReadFile(name string) ([]byte, error) {
	promise := js.Global().Call("opfsReadFile", name)
	v, err := awaitPromise(promise)
	if err != nil {
		return nil, err
	}
	length := v.Get("length").Int()
	out := make([]byte, length)
	js.CopyBytesToGo(out, v)
	return out, nil
}

// opfsRemoveFile deletes a single flat file from the origin's private file
// system root, ignoring a "does not exist" style rejection.
func opfsRemoveFile(name string) error {
	promise := js.Global().Call("opfsRemoveFile", name)
	_, err := awaitPromise(promise)
	return err
}

// snapshotState captures the current ledger, vector index, and agent-memory
// state into a single serializable struct. Callers must hold the relevant
// locks or accept the same eventual-consistency the rest of this demo does.
func snapshotState() demoSnapshot {
	var ledger map[string]string
	var vectorDocs []VectorDocument
	func() {
		db.mu.RLock()
		defer db.mu.RUnlock()
		ledger = make(map[string]string, 8)
		for k, v := range db.btree.All() {
			ledger[k] = v
		}
		vectorDocs = make([]VectorDocument, len(db.vectorStore))
		copy(vectorDocs, db.vectorStore)
	}()

	var frames map[string]string
	var sessions map[string]string
	var sessionSeq int
	var agentSeq int
	func() {
		agentEngine.mu.RLock()
		defer agentEngine.mu.RUnlock()
		frames = make(map[string]string, 16)
		for k, v := range agentEngine.frames.All() {
			frames[k] = v
		}
		sessions = make(map[string]string, 8)
		for k, v := range agentEngine.sessions.All() {
			sessions[k] = v
		}
		sessionSeq = agentEngine.sessionSeq
		agentSeq = agentEngine.agentSeq
	}()

	return demoSnapshot{
		Ledger:        ledger,
		VectorDocs:    vectorDocs,
		AgentFrames:   frames,
		AgentSessions: sessions,
		SessionSeq:    sessionSeq,
		AgentSeq:      agentSeq,
	}
}

// restoreState replaces the in-memory ledger, vector index, and agent-memory
// B-Trees with the contents of a previously persisted snapshot. Called once,
// shortly after boot, only if a snapshot was actually found in OPFS.
func restoreState(snap demoSnapshot) {
	func() {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.btree = newSeededBtree(snap.Ledger)
		db.vectorStore = snap.VectorDocs
	}()

	func() {
		agentEngine.mu.Lock()
		defer agentEngine.mu.Unlock()
		agentEngine.frames = newSeededBtree(snap.AgentFrames)
		agentEngine.sessions = newSeededBtree(snap.AgentSessions)
		agentEngine.sessionSeq = snap.SessionSeq
		agentEngine.agentSeq = snap.AgentSeq
	}()
}

// persistState serializes the current state and writes it to OPFS in the
// background. Fire-and-forget by design: persistence is additive to this
// demo's correctness story (the B-Tree transaction itself already committed
// in-memory before this is called), so a write failure here is logged, not
// surfaced as a transaction failure. Every call site runs this in its own
// goroutine specifically so OPFS write latency never inflates the
// microsecond-level transaction timing the demo reports.
func persistState() {
	if !opfsAvailable() {
		return
	}
	snap := snapshotState()
	ba, err := json.Marshal(snap)
	if err != nil {
		js.Global().Get("console").Call("warn", "sop: failed to marshal OPFS snapshot: "+err.Error())
		return
	}
	if err := opfsWriteFile(opfsStateFile, ba); err != nil {
		js.Global().Get("console").Call("warn", "sop: failed to persist state to OPFS: "+err.Error())
	}
}

// hydrateFromOPFS attempts to load a previously persisted snapshot and swap
// it in over the default seed data. Called once from main(), after the
// synchronous init() seeding has already run, so a browser without OPFS (or
// a first-ever visit with nothing persisted yet) behaves exactly as before
// this feature existed.
func hydrateFromOPFS() {
	if !opfsAvailable() {
		js.Global().Set("__SOP_OPFS_STATUS__", js.ValueOf("unsupported"))
		return
	}
	ba, err := opfsReadFile(opfsStateFile)
	if err != nil {
		// Nothing persisted yet (or it's unreadable) -- keep the seeded defaults.
		js.Global().Set("__SOP_OPFS_STATUS__", js.ValueOf("empty"))
		return
	}
	var snap demoSnapshot
	if err := json.Unmarshal(ba, &snap); err != nil {
		js.Global().Get("console").Call("warn", "sop: failed to parse OPFS snapshot: "+err.Error())
		js.Global().Set("__SOP_OPFS_STATUS__", js.ValueOf("empty"))
		return
	}
	restoreState(snap)
	js.Global().Set("__SOP_OPFS_STATUS__", js.ValueOf("restored"))
}

// jsOPFSStatus reports whether OPFS is supported and whether state was
// actually restored from it on this load, for the UI badge.
func jsOPFSStatus(this js.Value, args []js.Value) any {
	status := js.Global().Get("__SOP_OPFS_STATUS__")
	if status.IsUndefined() {
		return "empty"
	}
	return status.String()
}

// jsOPFSReset deletes the persisted snapshot so the next reload starts from
// the seeded demo defaults again.
func jsOPFSReset(this js.Value, args []js.Value) any {
	if !opfsAvailable() {
		return false
	}
	if err := opfsRemoveFile(opfsStateFile); err != nil {
		return false
	}
	return true
}
