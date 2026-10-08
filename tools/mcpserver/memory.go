package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sharedcode/joltrin/v5/tools/blocklog"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// This file is the optional memory: the server remembers which steps were
// blocked in earlier runs and tells the next agent, in two places. The MCP
// server instructions reach any MCP client when a session starts. A markdown
// file can be added to a CLAUDE.md or AGENTS.md for agents that read one. Both
// carry the same short lines. It is off unless WithMemory is used, and it is
// advisory: the barrier checks every call exactly as before.

const (
	maxLessonsPerWorkflow = 5
	maxLessons            = 10
)

// evidenceRule is the one rule about claims that goes with every set of
// lessons. A block for missing state is what an agent hits when it acts as if a
// step ran that did not, so the lessons say what counts as proof.
const evidenceRule = "A step is done only if execute_step reported it committed in this run. Do not say a step ran, or that its state holds, without that result. If you are unsure, say so and check."

type config struct {
	// versions caches workflowVersion by workflow, since the fingerprint is
	// needed on every call with memory on and a registered workflow is not
	// changed afterwards.
	versions sync.Map // *verify.Workflow -> string

	log         *blocklog.Log
	lessonsPath string
	// writeMu keeps two requests from writing the lessons file at once. They
	// share one temporary file name, so overlapping writes could leave the
	// file truncated or mixed.
	writeMu sync.Mutex
}

// Option configures New.
type Option func(*config)

// WithMemory makes the server record each block, once per run, in log, and
// tell later agents about them. The lessons appear in the server instructions
// returned when a client connects, and, if lessonsPath is not empty, in a
// markdown file that is rewritten whenever a new block is recorded. A failure
// to write either never changes what the barrier does.
func WithMemory(log *blocklog.Log, lessonsPath string) Option {
	return func(c *config) {
		c.log = log
		c.lessonsPath = lessonsPath
	}
}

// workflowVersion fingerprints a runbook's steps and safety rules, so recorded
// blocks stop applying once the runbook changes.
func workflowVersion(wf *verify.Workflow) string {
	b, _ := json.Marshal(describeWorkflow(wf))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// version is workflowVersion, computed once per workflow.
func (c *config) version(wf *verify.Workflow) string {
	if v, ok := c.versions.Load(wf); ok {
		return v.(string)
	}
	v := workflowVersion(wf)
	c.versions.Store(wf, v)
	return v
}

// shortID stands in for the caller-supplied trace id in the log. The id is
// free text from the client, so only a short hash is kept: enough to tell runs
// apart, and it keeps each entry a fixed size.
func shortID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:6])
}

// recordBlock stores a block in the log and refreshes the lessons file when it
// was new. Errors are dropped on purpose: memory must never affect the barrier.
func (c *config) recordBlock(store *runbookstore.Store, workflow, traceID, step string, wf *verify.Workflow, v *verify.Violation) {
	if c.log == nil {
		return
	}
	added, _ := c.log.Record(blocklog.Entry{
		Workflow:      workflow,
		Version:       c.version(wf),
		TraceID:       shortID(traceID),
		Step:          verify.StepID(step),
		BlockedBy:     v.Rule,
		MissingState:  v.MissingState,
		EstablishedBy: wf.StepsThatEstablish(v.MissingState),
	})
	if added {
		c.writeLessons(store)
	}
}

// recordRun notes that a run called execute_step, the total each rule's block
// count is read against. Errors are dropped on purpose, like recordBlock.
func (c *config) recordRun(workflow, traceID string, wf *verify.Workflow) {
	if c.log == nil {
		return
	}
	_, _ = c.log.RecordRun(workflow, c.version(wf), shortID(traceID))
}

// recordRecovery notes that a step committed, which counts as a recovery only
// if that step was blocked earlier in the same run.
func (c *config) recordRecovery(workflow, traceID, step string, wf *verify.Workflow) {
	if c.log == nil {
		return
	}
	_, _ = c.log.RecordRecovery(workflow, c.version(wf), shortID(traceID), verify.StepID(step))
}

// lessonFor is what earlier runs learned about this block, or "" when memory is
// off or no earlier run was blocked the same way. It matches the step, the rule
// and the missing state.
func (c *config) lessonFor(workflow, step string, wf *verify.Workflow, v *verify.Violation) string {
	if c.log == nil {
		return ""
	}
	s, ok := c.log.Summary(workflow, c.version(wf), verify.StepID(step), v.Rule, v.MissingState)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(lessonLine(workflow, wf, s), "- ")
}

func (c *config) writeLessons(store *runbookstore.Store) {
	if c.log == nil || c.lessonsPath == "" {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	body := lessonsFile(lessonLines(store, c.log))
	if err := os.MkdirAll(filepath.Dir(c.lessonsPath), 0o755); err != nil {
		return
	}
	tmp := c.lessonsPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.lessonsPath)
}

// instructions is the text sent to a client when it connects, or "" when
// there is nothing to say yet.
func instructions(store *runbookstore.Store, log *blocklog.Log) string {
	lines := lessonLines(store, log)
	if len(lines) == 0 {
		return ""
	}
	return "Lessons recorded by this server from earlier runs. They are advisory: the barrier still checks every call, and a blocked step returns the missing state and the steps that would establish it.\n" +
		evidenceRule + "\n" +
		strings.Join(lines, "\n")
}

func lessonsFile(lines []string) string {
	head := "# Joltrin lessons\n\nGenerated by sop-mcp-server from blocks in earlier runs. Do not edit it, it is rewritten when a new block is recorded. Advisory only: the barrier still checks every call.\n\n" + evidenceRule + "\n\n"
	if len(lines) == 0 {
		return head + "No blocks recorded yet.\n"
	}
	return head + strings.Join(lines, "\n") + "\n"
}

// collectLessons turns the recorded blocks into lessons, a few per runbook and
// a few overall, most frequent first. workflow limits it to one runbook when not
// empty. The instructions, LESSONS.md, and read_lessons all come from here, so
// they always agree.
func collectLessons(store *runbookstore.Store, log *blocklog.Log, workflow string) []Lesson {
	var out []Lesson
	for _, name := range store.WorkflowNames() {
		if workflow != "" && name != workflow {
			continue
		}
		wf, ok := store.Workflow(name)
		if !ok {
			continue
		}
		sums := log.Summaries(name, workflowVersion(wf))
		if len(sums) > maxLessonsPerWorkflow {
			sums = sums[:maxLessonsPerWorkflow]
		}
		for _, s := range sums {
			out = append(out, Lesson{
				Workflow:      name,
				Step:          s.Step,
				BlockedBy:     s.BlockedBy,
				MissingState:  s.MissingState,
				Runs:          s.Runs,
				RunFirst:      fixOrder(wf, s.MissingState),
				EstablishedBy: s.EstablishedBy,
				Text:          lessonLine(name, wf, s),
			})
		}
	}
	if len(out) > maxLessons {
		out = out[:maxLessons]
	}
	return out
}

// collectStats reports, per runbook, how many runs called execute_step and
// how many of them each rule blocked and recovered from. A runbook with no
// recorded runs or blocks is left out. workflow limits it to one runbook when
// not empty.
func collectStats(store *runbookstore.Store, log *blocklog.Log, workflow string) []WorkflowStats {
	out := []WorkflowStats{} // an empty list, not null
	for _, name := range store.WorkflowNames() {
		if workflow != "" && name != workflow {
			continue
		}
		wf, ok := store.Workflow(name)
		if !ok {
			continue
		}
		st := log.Stats(name, workflowVersion(wf))
		if st.Runs == 0 && len(st.Rules) == 0 {
			continue
		}
		rules := make([]RuleStat, len(st.Rules))
		for i, r := range st.Rules {
			rules[i] = RuleStat{BlockedBy: r.BlockedBy, BlockedRuns: r.BlockedRuns, RecoveredRuns: r.RecoveredRuns}
		}
		out = append(out, WorkflowStats{Workflow: name, Runs: st.Runs, Rules: rules})
	}
	return out
}

// lessonLines is the lessons as short plain lines.
func lessonLines(store *runbookstore.Store, log *blocklog.Log) []string {
	var lines []string
	for _, l := range collectLessons(store, log, "") {
		lines = append(lines, l.Text)
	}
	return lines
}

func lessonLine(workflow string, wf *verify.Workflow, s blocklog.Summary) string {
	runs := fmt.Sprintf("%d runs", s.Runs)
	if s.Runs == 1 {
		runs = "1 run"
	}
	rule := ""
	if s.BlockedBy != "precondition" {
		rule = fmt.Sprintf(" (rule: %s)", s.BlockedBy)
	}
	if order := fixOrder(wf, s.MissingState); len(order) > 0 {
		return fmt.Sprintf("- %s: before %s, run %s. It was blocked in %s for missing state %s%s.",
			workflow, s.Step, joinSteps(order, ", then "), runs, s.MissingState, rule)
	}
	if len(s.EstablishedBy) > 0 {
		return fmt.Sprintf("- %s: %s was blocked in %s for missing state %s%s. Steps that establish it: %s.",
			workflow, s.Step, runs, s.MissingState, rule, joinSteps(s.EstablishedBy, ", "))
	}
	return fmt.Sprintf("- %s: %s was blocked in %s for missing state %s%s. No step in this runbook establishes it.",
		workflow, s.Step, runs, s.MissingState, rule)
}

func joinSteps(ids []verify.StepID, sep string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, sep)
}

// fixOrder returns the steps to run, in order, to establish state, following
// each step's own requirements back to the start. It returns nil when the path
// is not clear: a state that no step, or more than one step, establishes, or a
// cycle.
func fixOrder(wf *verify.Workflow, state verify.State) []verify.StepID {
	est := wf.StepsThatEstablish(state)
	if len(est) != 1 {
		return nil
	}
	var order []verify.StepID
	visiting := map[verify.StepID]bool{}
	done := map[verify.StepID]bool{}
	var visit func(id verify.StepID) bool
	visit = func(id verify.StepID) bool {
		if done[id] {
			return true
		}
		if visiting[id] {
			return false
		}
		visiting[id] = true
		for _, req := range wf.Steps[id].Requires {
			e := wf.StepsThatEstablish(req)
			if len(e) != 1 || !visit(e[0]) {
				return false
			}
		}
		visiting[id] = false
		done[id] = true
		order = append(order, id)
		return true
	}
	if !visit(est[0]) {
		return nil
	}
	return order
}
