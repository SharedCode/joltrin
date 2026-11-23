package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/sharedcode/joltrin/v5/governance"
	"github.com/sharedcode/joltrin/v5/taint"
	"github.com/sharedcode/joltrin/v5/tools/mcpserver"
)

const taintUsage = `usage:
  sop-mcp-server taint check [--json] [--observe] [--audit FILE] --policy POLICY.json --values VALUES.json CALL.json
  sop-mcp-server taint honeytoken [KIND]

check asks the taint barrier whether one agent tool call may run. CALL.json is
{"tool": "...", "destination": "https://...", "args": {...}}. Use "-" to read it
from stdin. VALUES.json lists what to protect:
[{"label": "secret", "name": "payments", "value": "..."},
 {"label": "honeytoken", "name": "decoy", "value": "..."}]
A "honeytoken" label is a planted fake that always blocks. The values file holds
raw secrets, so it must not be readable by other users.

  allowed   exit 0
  blocked   exit 1, with a reason code and no value in the output
  usage     exit 2
  --observe report what would be blocked and exit 0 (log only)
  --audit   append each decision to a hash-chained JSON Lines file (unsigned)

honeytoken prints a new fake credential to plant in an agent's context. Kinds: ` + "aws_access_key_id, aws_secret_access_key, github_token, generic_api_key" + `.
`

// taintValue is one entry of the values file.
type taintValue struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// runTaint implements "sop-mcp-server taint".
func runTaint(args []string, in io.Reader, out, errw io.Writer) int {
	if isHelp(args) {
		fmt.Fprint(out, taintUsage)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(errw, taintUsage)
		return 2
	}
	switch args[0] {
	case "honeytoken":
		kind := taint.KindAWSAccessKeyID
		if len(args) > 1 {
			kind = args[1]
		}
		tok, err := taint.GenerateHoneytoken(kind)
		if err != nil {
			fmt.Fprintf(errw, "sop-mcp-server taint: %v\nKinds: %s\n", err, strings.Join(taint.Kinds, ", "))
			return 2
		}
		fmt.Fprintln(out, tok)
		return 0
	case "check":
		return runTaintCheck(args[1:], in, out, errw)
	}
	fmt.Fprint(errw, taintUsage)
	return 2
}

func runTaintCheck(args []string, in io.Reader, out, errw io.Writer) int {
	args, asJSON := popFlag(args, "--json")
	args, observe := popFlag(args, "--observe")
	var policyPath, valuesPath, auditPath string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--policy", "--values", "--audit":
			if i+1 >= len(args) {
				fmt.Fprint(errw, taintUsage)
				return 2
			}
			switch args[i] {
			case "--policy":
				policyPath = args[i+1]
			case "--values":
				valuesPath = args[i+1]
			default:
				auditPath = args[i+1]
			}
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if policyPath == "" || valuesPath == "" || len(rest) != 1 {
		fmt.Fprint(errw, taintUsage)
		return 2
	}

	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(errw, "sop-mcp-server taint: "+format+"\n", a...)
		return code
	}

	policyData, err := os.ReadFile(policyPath)
	if err != nil {
		return fail(2, "%v", err)
	}
	policy, err := taint.ParsePolicy(policyData)
	if err != nil {
		return fail(2, "%v", err)
	}
	reg, err := loadTaintValues(valuesPath)
	if err != nil {
		return fail(2, "%v", err)
	}

	var callData []byte
	if rest[0] == "-" {
		callData, err = io.ReadAll(io.LimitReader(in, taint.MaxArgBytes+1<<16))
	} else {
		callData, err = os.ReadFile(rest[0])
	}
	if err != nil {
		return fail(2, "%v", err)
	}
	var call taint.Call
	dec := json.NewDecoder(bytes.NewReader(callData))
	dec.UseNumber()
	if err := dec.Decode(&call); err != nil || call.Tool == "" {
		return fail(2, "the call file must be JSON with a \"tool\" field")
	}

	cfg := taint.Config{Registry: reg, Policy: policy}
	if observe {
		cfg.Mode = taint.ModeObserve
	}
	if auditPath != "" {
		rec, err := governance.OpenFileAuditRecorder(auditPath, nil)
		if err != nil {
			return fail(2, "audit file: %v", err)
		}
		defer rec.Close()
		cfg.Audit = rec
	}
	cfg.OnAlert = func(a taint.Alert) {
		fmt.Fprintf(errw, "ALERT honeytoken %s used in tool %s, sink %s\n", a.ValueID, a.Tool, a.Sink)
	}
	b, err := taint.New(cfg)
	if err != nil {
		return fail(2, "%v", err)
	}
	v, err := b.Check(context.Background(), call)
	if err != nil {
		fmt.Fprintf(errw, "sop-mcp-server taint: %v\n", err)
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	} else {
		fmt.Fprintf(out, "%s: %s", v.Outcome, v.Reason)
		for _, f := range v.Findings {
			fmt.Fprintf(out, " [%s %s via %s]", f.Label, f.ValueID, f.Variant)
		}
		fmt.Fprintln(out)
	}
	if v.Allowed {
		return 0
	}
	return 1
}

// loadTaintValues reads a values file into a registry. On Unix it refuses a
// file that group or others can read, since it holds raw secrets.
func loadTaintValues(path string) (*taint.Registry, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by other users; run chmod 600 on it", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var vals []taintValue
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&vals); err != nil {
		return nil, errors.New("values file must be a JSON list of {label, name, value}")
	}
	reg, err := taint.NewRegistry()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		var err error
		if v.Label == taint.HoneytokenLabel {
			_, err = reg.RegisterHoneytoken(v.Name, v.Value)
		} else {
			_, err = reg.Register(v.Label, v.Name, v.Value)
		}
		if err != nil {
			return nil, fmt.Errorf("values entry %d: %v", i, err)
		}
	}
	return reg, nil
}

// taintOptions turns SOP_TAINT_POLICY, SOP_TAINT_VALUES and SOP_TAINT_AUDIT
// into the check_tool_call tool. With none set it adds nothing. SOP_MODE=observe
// puts the barrier in observe mode.
func taintOptions(getenv func(string) string, errw io.Writer) ([]mcpserver.Option, func(), error) {
	policyPath, valuesPath := getenv("SOP_TAINT_POLICY"), getenv("SOP_TAINT_VALUES")
	if policyPath == "" && valuesPath == "" {
		return nil, func() {}, nil
	}
	if policyPath == "" || valuesPath == "" {
		return nil, func() {}, errors.New("SOP_TAINT_POLICY and SOP_TAINT_VALUES must be set together")
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("SOP_TAINT_POLICY: %w", err)
	}
	policy, err := taint.ParsePolicy(data)
	if err != nil {
		return nil, func() {}, fmt.Errorf("SOP_TAINT_POLICY: %w", err)
	}
	reg, err := loadTaintValues(valuesPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("SOP_TAINT_VALUES: %w", err)
	}
	cfg := taint.Config{Registry: reg, Policy: policy, OnAlert: func(a taint.Alert) {
		fmt.Fprintf(errw, "sop-mcp-server: ALERT honeytoken %s used in tool %s, sink %s\n", a.ValueID, a.Tool, a.Sink)
	}}
	if getenv("SOP_MODE") == "observe" {
		cfg.Mode = taint.ModeObserve
	}
	closeFn := func() {}
	if p := getenv("SOP_TAINT_AUDIT"); p != "" {
		rec, err := governance.OpenFileAuditRecorder(p, nil)
		if err != nil {
			return nil, func() {}, fmt.Errorf("SOP_TAINT_AUDIT: %w", err)
		}
		cfg.Audit = rec
		closeFn = func() { rec.Close() }
	}
	b, err := taint.New(cfg)
	if err != nil {
		closeFn()
		return nil, func() {}, err
	}
	return []mcpserver.Option{mcpserver.WithTaint(b)}, closeFn, nil
}
