// Command demo-agents is a WASM build of verify's barrier certificate,
// the same engine backing tools/mcpserver and tools/a2aagent, running
// entirely client-side. This is not a mockup of the MCP/A2A servers (those
// are real network protocols and cannot run inside a static GitHub Pages
// site with no backend); it is the actual verification logic those servers
// call, wired to buttons instead of protocol calls, so the barrier itself
// is something a visitor can click through live rather than only read
// about.
//go:build js && wasm

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

var (
	workflow *verify.Workflow
	trace    *verify.Trace
)

type stepResult struct {
	Step    string   `json:"step"`
	Blocked bool     `json:"blocked"`
	Reason  string   `json:"reason,omitempty"`
	Trace   []string `json:"trace"`
	// BlockedBy, MissingState and EstablishedBy are the same structured
	// fields tools/mcpserver returns from execute_step, so the page can show
	// what an agent actually receives, not only the message text.
	BlockedBy     string   `json:"blocked_by,omitempty"`
	MissingState  string   `json:"missing_state,omitempty"`
	EstablishedBy []string `json:"established_by_steps,omitempty"`
}

func snapshotTrace() []string {
	out := make([]string, len(trace.Executed))
	for i, id := range trace.Executed {
		out[i] = string(id)
	}
	return out
}

func jsExecuteStep(this js.Value, args []js.Value) any {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return errResult("missing step id")
	}
	step := verify.StepID(args[0].String())

	res := stepResult{Step: string(step)}
	if err := workflow.CheckSafety(trace, step); err != nil {
		res.Blocked = true
		res.Reason = err.Error()
		var v *verify.Violation
		if errors.As(err, &v) {
			res.BlockedBy = v.Rule
			res.MissingState = string(v.MissingState)
			for _, id := range workflow.StepsThatEstablish(v.MissingState) {
				res.EstablishedBy = append(res.EstablishedBy, string(id))
			}
		}
	} else {
		_ = workflow.Commit(trace, step)
	}
	res.Trace = snapshotTrace()

	go persistTrace()

	b, _ := json.Marshal(res)
	return string(b)
}

func jsReset(this js.Value, args []js.Value) any {
	trace = verify.NewTrace()
	go func() {
		if opfsAvailable() {
			_ = opfsRemoveFile(agentsStateFile)
		}
	}()
	b, _ := json.Marshal(stepResult{Trace: snapshotTrace()})
	return string(b)
}

func jsRunbook(this js.Value, args []js.Value) any {
	type stepInfo struct {
		ID          string   `json:"id"`
		Requires    []string `json:"requires"`
		Establishes []string `json:"establishes"`
	}
	type ruleInfo struct {
		Name      string `json:"name"`
		Forbidden string `json:"forbidden"`
		Requires  string `json:"requires"`
	}
	steps := make([]stepInfo, 0, len(workflow.Steps))
	for id, s := range workflow.Steps {
		si := stepInfo{ID: string(id)}
		for _, r := range s.Requires {
			si.Requires = append(si.Requires, string(r))
		}
		for _, e := range s.Establishes {
			si.Establishes = append(si.Establishes, string(e))
		}
		steps = append(steps, si)
	}
	rules := make([]ruleInfo, 0, len(workflow.Safety))
	for _, r := range workflow.Safety {
		rules = append(rules, ruleInfo{Name: r.Name, Forbidden: string(r.Forbidden), Requires: string(r.Requires)})
	}
	b, _ := json.Marshal(map[string]any{"steps": steps, "safety": rules, "trace": snapshotTrace()})
	return string(b)
}

func errResult(msg string) string {
	b, _ := json.Marshal(map[string]any{"error": msg})
	return string(b)
}

func main() {
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		panic(err)
	}
	workflow = wf
	trace = verify.NewTrace()

	// Register canonical Joltrin functions, plus Engram and SOP aliases for backwards compatibility.
	runbookFn := js.FuncOf(jsRunbook)
	execStepFn := js.FuncOf(jsExecuteStep)
	resetFn := js.FuncOf(jsReset)
	opfsStatusFn := js.FuncOf(jsOPFSStatus)

	js.Global().Set("joltrinAgentsRunbook", runbookFn)
	js.Global().Set("engramAgentsRunbook", runbookFn)
	js.Global().Set("sopAgentsRunbook", runbookFn)
	js.Global().Set("joltrinAgentsExecuteStep", execStepFn)
	js.Global().Set("engramAgentsExecuteStep", execStepFn)
	js.Global().Set("sopAgentsExecuteStep", execStepFn)
	js.Global().Set("joltrinAgentsReset", resetFn)
	js.Global().Set("engramAgentsReset", resetFn)
	js.Global().Set("sopAgentsReset", resetFn)
	js.Global().Set("joltrinAgentsOPFSStatus", opfsStatusFn)
	js.Global().Set("engramAgentsOPFSStatus", opfsStatusFn)
	js.Global().Set("sopAgentsOPFSStatus", opfsStatusFn)

	hydrateFromOPFS()

	js.Global().Set("__JOLTRIN_AGENTS_WASM_READY__", js.ValueOf(true))
	js.Global().Set("__ENGRAM_AGENTS_WASM_READY__", js.ValueOf(true))
	js.Global().Set("__SOP_AGENTS_WASM_READY__", js.ValueOf(true))
	if doc := js.Global().Get("document"); !doc.IsUndefined() && !doc.IsNull() {
		evtJoltrin := js.Global().Get("CustomEvent").New("joltrin-agents-wasm-ready")
		doc.Call("dispatchEvent", evtJoltrin)
		evtEngram := js.Global().Get("CustomEvent").New("engram-agents-wasm-ready")
		doc.Call("dispatchEvent", evtEngram)
		evtSop := js.Global().Get("CustomEvent").New("sop-agents-wasm-ready")
		doc.Call("dispatchEvent", evtSop)
	}

	fmt.Println("joltrin-agents: verification barrier WASM kernel ready")

	c := make(chan struct{})
	<-c
}
