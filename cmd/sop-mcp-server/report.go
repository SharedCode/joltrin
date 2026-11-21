package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// reportLine is the part of a decision log line the report reads.
type reportLine struct {
	Event     string `json:"event"`
	Workflow  string `json:"workflow"`
	Trace     string `json:"trace_id"`
	Step      string `json:"step"`
	Outcome   string `json:"outcome"`
	BlockedBy string `json:"blocked_by"`
}

type reportCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// reportResult is the machine-readable form of "report --json".
type reportResult struct {
	File      string        `json:"file"`
	Decisions int           `json:"decisions"`
	Traces    int           `json:"traces"`
	Allowed   int           `json:"allowed"`
	Replayed  int           `json:"replayed"`
	Observed  int           `json:"observed"`
	Blocked   int           `json:"blocked"`
	Invalid   int           `json:"invalid"`
	Skipped   int           `json:"skipped_lines"`
	ByRule    []reportCount `json:"by_rule"`
	ByStep    []reportCount `json:"by_step"`
}

// buildReport reads a decision log written with SOP_DECISION_LOG. A line that is
// not JSON, or not an execute_step event, is counted as skipped rather than
// failing the whole report, because a log file may be shared with other output.
func buildReport(r io.Reader, name string) (reportResult, error) {
	res := reportResult{File: name}
	traces := map[string]bool{}
	rules := map[string]int{}
	steps := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var l reportLine
		if json.Unmarshal([]byte(line), &l) != nil || l.Event != "execute_step" {
			res.Skipped++
			continue
		}
		res.Decisions++
		if l.Trace != "" {
			traces[l.Trace] = true
		}
		switch l.Outcome {
		case "allowed":
			res.Allowed++
		case "replayed":
			res.Replayed++
		case "observed", "blocked":
			if l.Outcome == "observed" {
				res.Observed++
			} else {
				res.Blocked++
			}
			rules[l.BlockedBy]++
			steps[l.Step]++
		default:
			res.Invalid++
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	res.Traces = len(traces)
	res.ByRule = sortedCounts(rules)
	res.ByStep = sortedCounts(steps)
	return res, nil
}

func sortedCounts(m map[string]int) []reportCount {
	out := make([]reportCount, 0, len(m))
	for k, v := range m {
		out = append(out, reportCount{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func runReport(args []string, out, errw io.Writer) int {
	if isHelp(args) {
		fmt.Fprintln(out, "usage: sop-mcp-server report [--json] LOGFILE\nSummarize a SOP_DECISION_LOG file: steps allowed, steps the barrier blocked or would have blocked in observe mode, and which rules and steps caused them.")
		return 0
	}
	args, asJSON := popFlag(args, "--json")
	if len(args) != 1 {
		fmt.Fprintln(errw, "usage: sop-mcp-server report [--json] LOGFILE")
		return 2
	}
	f, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(errw, "sop-mcp-server report: %v\nSet SOP_DECISION_LOG to a file path and run an agent first.\n", err)
		return 1
	}
	defer f.Close()
	res, err := buildReport(f, args[0])
	if err != nil {
		fmt.Fprintf(errw, "sop-mcp-server report: %v\n", err)
		return 1
	}
	if res.Decisions == 0 {
		fmt.Fprintf(errw, "sop-mcp-server report: no execute_step decisions in %s. Check that SOP_DECISION_LOG points at this file.\n", args[0])
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(errw, "sop-mcp-server report: %v\n", err)
			return 1
		}
		return 0
	}
	writeReportText(out, res)
	return 0
}

func writeReportText(w io.Writer, r reportResult) {
	fmt.Fprintf(w, "Decisions: %d across %d traces\n", r.Decisions, r.Traces)
	fmt.Fprintf(w, "  allowed:  %d\n", r.Allowed+r.Replayed)
	if r.Observed > 0 {
		fmt.Fprintf(w, "  would have been blocked (observe mode): %d\n", r.Observed)
	}
	if r.Blocked > 0 {
		fmt.Fprintf(w, "  blocked:  %d\n", r.Blocked)
	}
	if r.Invalid > 0 {
		fmt.Fprintf(w, "  invalid:  %d\n", r.Invalid)
	}
	if len(r.ByRule) > 0 {
		fmt.Fprintln(w, "\nRules that fired:")
		for _, c := range r.ByRule {
			fmt.Fprintf(w, "  %4d  %s\n", c.Count, c.Name)
		}
		fmt.Fprintln(w, "\nSteps affected:")
		for _, c := range r.ByStep {
			fmt.Fprintf(w, "  %4d  %s\n", c.Count, c.Name)
		}
	}
	if r.Skipped > 0 {
		fmt.Fprintf(w, "\n%d lines were not decisions and were skipped.\n", r.Skipped)
	}
}
