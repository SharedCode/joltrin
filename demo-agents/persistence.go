//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/sharedcode/joltrin/v5/verify"
)

// agentsStateFile is the OPFS snapshot for this demo's trace, same pattern
// as demo/persistence.go: async File System Access API, feature-detected,
// falls back to in-memory-only if OPFS isn't available.
const agentsStateFile = "sop-agents-trace.json"

type traceSnapshot struct {
	Executed []string `json:"executed"`
}

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

func opfsAvailable() bool {
	fn := js.Global().Get("opfsSupported")
	if fn.IsUndefined() || fn.Type() != js.TypeFunction {
		return false
	}
	return fn.Invoke().Bool()
}

func opfsWriteFile(name string, data []byte) error {
	jsBytes := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(jsBytes, data)
	_, err := awaitPromise(js.Global().Call("opfsWriteFile", name, jsBytes))
	return err
}

func opfsReadFile(name string) ([]byte, error) {
	v, err := awaitPromise(js.Global().Call("opfsReadFile", name))
	if err != nil {
		return nil, err
	}
	out := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(out, v)
	return out, nil
}

func opfsRemoveFile(name string) error {
	_, err := awaitPromise(js.Global().Call("opfsRemoveFile", name))
	return err
}

// persistTrace writes the current trace to OPFS in the background. Called
// after every step so a reload picks up exactly where the visitor left off.
func persistTrace() {
	if !opfsAvailable() {
		return
	}
	ba, err := json.Marshal(traceSnapshot{Executed: snapshotTrace()})
	if err != nil {
		return
	}
	if err := opfsWriteFile(agentsStateFile, ba); err != nil {
		js.Global().Get("console").Call("warn", "sop-agents: failed to persist trace: "+err.Error())
	}
}

// hydrateFromOPFS restores a previously persisted trace, if any, replaying
// each step through the real barrier check so an invalid or hand-edited
// snapshot can never silently grant a state the checker wouldn't otherwise
// allow.
func hydrateFromOPFS() {
	if !opfsAvailable() {
		js.Global().Set("__SOP_AGENTS_OPFS_STATUS__", js.ValueOf("unsupported"))
		return
	}
	ba, err := opfsReadFile(agentsStateFile)
	if err != nil {
		js.Global().Set("__SOP_AGENTS_OPFS_STATUS__", js.ValueOf("empty"))
		return
	}
	var snap traceSnapshot
	if err := json.Unmarshal(ba, &snap); err != nil {
		js.Global().Set("__SOP_AGENTS_OPFS_STATUS__", js.ValueOf("empty"))
		return
	}
	fresh := verify.NewTrace()
	for _, id := range snap.Executed {
		step := verify.StepID(id)
		if err := workflow.CheckSafety(fresh, step); err != nil {
			// Persisted state no longer replays cleanly (e.g. the runbook
			// definition changed); don't apply a partial/invalid trace.
			js.Global().Set("__SOP_AGENTS_OPFS_STATUS__", js.ValueOf("empty"))
			return
		}
		_ = workflow.Commit(fresh, step)
	}
	trace = fresh
	js.Global().Set("__SOP_AGENTS_OPFS_STATUS__", js.ValueOf("restored"))
}

func jsOPFSStatus(this js.Value, args []js.Value) any {
	status := js.Global().Get("__SOP_AGENTS_OPFS_STATUS__")
	if status.IsUndefined() {
		return "empty"
	}
	return status.String()
}
