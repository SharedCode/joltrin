// Package blocklog records the steps the barrier blocked, and summarizes them
// so the next run can start knowing what usually goes wrong.
//
// A block is recorded once per run (trace) for each step, rule and missing
// state, so an agent that retries the same call ten times in one run counts
// once. Entries are tied to a fingerprint of the runbook they were recorded
// against, and a summary only counts entries for the current fingerprint, so
// advice about a runbook that has since changed is not repeated. Entries also
// expire.
//
// Everything stored comes from the server: the workflow name, step and state
// IDs, and the rule name. Nothing an agent wrote is stored, so an agent cannot
// plant advice. It can only raise the count for a real block it really hit.
// A summary is advisory. The barrier checks every call regardless.
package blocklog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

const (
	// DefaultTTL is how long an entry counts toward a summary.
	DefaultTTL = 30 * 24 * time.Hour
	// DefaultMaxEntries bounds how many entries a Log keeps in memory and on
	// disk. The oldest are dropped first.
	DefaultMaxEntries = 10000
)

// Kinds of entry. A block has no kind, so files written before the other kinds
// existed still load as blocks.
const (
	// KindRun marks that a run called execute_step. It is the denominator for
	// how often a rule trips.
	KindRun = "run"
	// KindRecovered marks that a step blocked earlier in the same run later
	// committed.
	KindRecovered = "recovered"
)

// Entry is one recorded block, or, when Kind is set, a run or a recovery.
type Entry struct {
	Kind          string          `json:"kind,omitempty"`
	At            time.Time       `json:"at"`
	Workflow      string          `json:"workflow"`
	Version       string          `json:"version"`
	TraceID       string          `json:"trace_id"`
	Step          verify.StepID   `json:"step"`
	BlockedBy     string          `json:"blocked_by"`
	MissingState  verify.State    `json:"missing_state"`
	EstablishedBy []verify.StepID `json:"established_by_steps,omitempty"`
}

// Summary aggregates the entries for one step, rule and missing state.
type Summary struct {
	Step          verify.StepID   `json:"step"`
	BlockedBy     string          `json:"blocked_by"`
	MissingState  verify.State    `json:"missing_state"`
	EstablishedBy []verify.StepID `json:"established_by_steps,omitempty"`
	// Runs is how many separate runs hit this block.
	Runs     int       `json:"runs"`
	LastSeen time.Time `json:"last_seen"`
}

type key struct {
	kind                     string
	workflow, version, trace string
	step                     verify.StepID
	blockedBy                string
	missing                  verify.State
}

func (e Entry) key() key {
	return key{e.Kind, e.Workflow, e.Version, e.TraceID, e.Step, e.BlockedBy, e.MissingState}
}

// sumKey is what Summary looks a block up by.
type sumKey struct {
	workflow, version string
	step              verify.StepID
	blockedBy         string
	missing           verify.State
}

func (e *Entry) sumKey() sumKey {
	return sumKey{e.Workflow, e.Version, e.Step, e.BlockedBy, e.MissingState}
}

// Option configures a Log.
type Option func(*Log)

// WithTTL sets how long an entry counts. A value of zero or less keeps the
// default.
func WithTTL(d time.Duration) Option {
	return func(l *Log) {
		if d > 0 {
			l.ttl = d
		}
	}
}

// WithMaxEntries bounds the number of entries kept. A value below 1 keeps the
// default.
func WithMaxEntries(n int) Option {
	return func(l *Log) {
		if n >= 1 {
			l.maxEntries = n
		}
	}
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(l *Log) { l.now = now }
}

// Log holds recorded blocks, in memory and optionally in an append-only JSON
// lines file. It is safe for concurrent use.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	seen    map[key]struct{}
	// base is the sequence number of entries[0]; an entry's sequence number is
	// base plus its position, and never changes while it is kept. bySum lists,
	// in order, the sequence numbers of the block entries for each sumKey, so
	// Summary reads the few that match instead of scanning every entry.
	base       uint64
	bySum      map[sumKey][]uint64
	file       *os.File
	path       string
	compactErr error
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

func newLog(opts []Option) *Log {
	l := &Log{
		seen:       make(map[key]struct{}),
		bySum:      make(map[sumKey][]uint64),
		ttl:        DefaultTTL,
		maxEntries: DefaultMaxEntries,
		now:        time.Now,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// New returns a Log that keeps entries in memory only.
func New(opts ...Option) *Log { return newLog(opts) }

// Open returns a Log backed by the JSON lines file at path, creating it if it
// does not exist. Existing entries are loaded. Lines that cannot be parsed are
// skipped, and entries that have expired or exceed the cap are dropped, with
// the file rewritten when anything was dropped.
func Open(path string, opts ...Option) (*Log, error) {
	l := newLog(opts)
	dropped, err := l.load(path)
	if err != nil {
		return nil, err
	}
	if dropped {
		// Compaction is best effort. Windows refuses to replace a file another
		// process has open, and the next start tries again. The failure is
		// kept so the caller can say so, because a file that cannot be
		// compacted keeps growing.
		l.compactErr = l.rewrite(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("blocklog: open %s: %w", path, err)
	}
	l.file = f
	l.path = path
	return l, nil
}

// load reads path into memory. It reports whether any line was dropped.
func (l *Log) load(path string) (dropped bool, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blocklog: read %s: %w", path, err)
	}
	defer f.Close()

	cutoff := l.now().Add(-l.ttl)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			dropped = true
			continue
		}
		if e.At.Before(cutoff) {
			dropped = true
			continue
		}
		if _, dup := l.seen[e.key()]; dup {
			dropped = true
			continue
		}
		l.add(e)
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("blocklog: read %s: %w", path, err)
	}
	if len(l.entries) > l.maxEntries {
		dropped = true
		l.trim()
	}
	return dropped, nil
}

// rewrite replaces the file with the entries currently in memory.
func (l *Log) rewrite(path string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("blocklog: compact %s: %w", path, err)
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range l.entries {
		if err := enc.Encode(e); err != nil {
			f.Close()
			return fmt.Errorf("blocklog: compact %s: %w", path, err)
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("blocklog: compact %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("blocklog: compact %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("blocklog: compact %s: %w", path, err)
	}
	return nil
}

func (l *Log) add(e Entry) {
	if e.Kind == "" {
		sk := e.sumKey()
		l.bySum[sk] = append(l.bySum[sk], l.base+uint64(len(l.entries)))
	}
	l.entries = append(l.entries, e)
	l.seen[e.key()] = struct{}{}
}

// trim drops the oldest entries until the cap holds. A dropped block entry has
// the lowest sequence number of its sumKey, so it is the first in that list.
func (l *Log) trim() {
	for len(l.entries) > l.maxEntries {
		old := &l.entries[0]
		delete(l.seen, old.key())
		if old.Kind == "" {
			sk := old.sumKey()
			if seqs := l.bySum[sk][1:]; len(seqs) > 0 {
				l.bySum[sk] = seqs
			} else {
				delete(l.bySum, sk)
			}
		}
		l.entries = l.entries[1:]
		l.base++
	}
}

// Record stores e unless the same run already recorded the same block, and
// reports whether it was new. A failure to write the file is returned, but the
// entry still counts in memory. Callers should treat that as non-fatal:
// recording must never change what the barrier does.
func (l *Log) Record(e Entry) (added bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record(e)
}

// record is Record with l.mu held.
func (l *Log) record(e Entry) (added bool, err error) {
	if _, dup := l.seen[e.key()]; dup {
		return false, nil
	}
	if e.At.IsZero() {
		e.At = l.now()
	}
	l.add(e)
	l.trim()
	if l.file == nil {
		return true, nil
	}
	if err := l.reopenIfReplaced(); err != nil {
		return true, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return true, fmt.Errorf("blocklog: encode entry: %w", err)
	}
	if _, err := l.file.Write(append(b, '\n')); err != nil {
		return true, fmt.Errorf("blocklog: write entry: %w", err)
	}
	return true, nil
}

// reopenIfReplaced reopens the file when another server compacted it, which
// replaces the file at path and leaves this one writing to the old copy. It is
// called with l.mu held.
func (l *Log) reopenIfReplaced() error {
	cur, err := os.Stat(l.path)
	if err != nil {
		return nil // gone or unreadable: the write below reports it
	}
	held, err := l.file.Stat()
	if err == nil && os.SameFile(held, cur) {
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("blocklog: reopen %s: %w", l.path, err)
	}
	l.file.Close()
	l.file = f
	return nil
}

// RecordRun notes that a run (trace) called execute_step against workflow at
// version, and reports whether it was the first time. A run counts once however
// many calls it makes.
func (l *Log) RecordRun(workflow, version, trace string) (added bool, err error) {
	return l.Record(Entry{Kind: KindRun, Workflow: workflow, Version: version, TraceID: trace})
}

// RecordRecovery notes that step committed in a run where it had been blocked
// before, once for each recorded block of that step in that run, and returns
// how many were new. It returns 0 for a step that was never blocked in that
// run, so the caller can call it on every successful commit.
func (l *Log) RecordRecovery(workflow, version, trace string, step verify.StepID) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var blocks []Entry
	for _, e := range l.entries {
		if e.Kind == "" && e.Workflow == workflow && e.Version == version && e.TraceID == trace && e.Step == step {
			blocks = append(blocks, e)
		}
	}
	var firstErr error
	n := 0
	for _, b := range blocks {
		added, err := l.record(Entry{
			Kind: KindRecovered, Workflow: workflow, Version: version, TraceID: trace,
			Step: step, BlockedBy: b.BlockedBy, MissingState: b.MissingState,
		})
		if added {
			n++
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return n, firstErr
}

// RuleStats is how one rule behaved across runs.
type RuleStats struct {
	BlockedBy string `json:"blocked_by"`
	// BlockedRuns is how many runs this rule blocked at least once.
	BlockedRuns int `json:"blocked_runs"`
	// RecoveredRuns is how many of those runs went on to commit every step the
	// rule had blocked.
	RecoveredRuns int `json:"recovered_runs"`
}

// Stats is how often runs of one workflow were blocked, and by which rules.
type Stats struct {
	// Runs is how many runs called execute_step.
	Runs  int         `json:"runs"`
	Rules []RuleStats `json:"rules"`
}

// Stats returns run and rule totals for workflow at version within the TTL.
// Rules are ordered by how many runs they blocked, then by name.
func (l *Log) Stats(workflow, version string) Stats {
	l.mu.Lock()
	defer l.mu.Unlock()

	type ruleRun struct{ rule, trace string }
	type blockKey struct {
		rr      ruleRun
		step    verify.StepID
		missing verify.State
	}
	// counts is how many distinct blocked steps a rule had in one run, and how
	// many of them recovered. Held by value so a run costs no allocation.
	type counts struct{ blocked, recovered int }
	cutoff := l.now().Add(-l.ttl)
	runs := map[string]struct{}{}
	recovered := make(map[blockKey]bool, len(l.entries)) // true once the step recovered
	perRun := map[ruleRun]counts{}
	for _, e := range l.entries {
		if e.Workflow != workflow || e.Version != version || e.At.Before(cutoff) {
			continue
		}
		switch e.Kind {
		case KindRun:
			runs[e.TraceID] = struct{}{}
		case "":
			rr := ruleRun{e.BlockedBy, e.TraceID}
			bk := blockKey{rr, e.Step, e.MissingState}
			if _, seen := recovered[bk]; !seen {
				recovered[bk] = false
				c := perRun[rr]
				c.blocked++
				perRun[rr] = c
			}
		}
	}
	// Recoveries are matched to blocks in a second pass, so the order they
	// were recorded in does not matter.
	for _, e := range l.entries {
		if e.Kind != KindRecovered || e.Workflow != workflow || e.Version != version || e.At.Before(cutoff) {
			continue
		}
		rr := ruleRun{e.BlockedBy, e.TraceID}
		bk := blockKey{rr, e.Step, e.MissingState}
		if done, ok := recovered[bk]; ok && !done {
			recovered[bk] = true
			c := perRun[rr]
			c.recovered++
			perRun[rr] = c
		}
	}

	byRule := map[string]*RuleStats{}
	for rr, c := range perRun {
		rs := byRule[rr.rule]
		if rs == nil {
			rs = &RuleStats{BlockedBy: rr.rule}
			byRule[rr.rule] = rs
		}
		rs.BlockedRuns++
		if c.recovered == c.blocked {
			rs.RecoveredRuns++
		}
	}
	out := Stats{Runs: len(runs), Rules: make([]RuleStats, 0, len(byRule))}
	for _, rs := range byRule {
		out.Rules = append(out.Rules, *rs)
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		a, b := out.Rules[i], out.Rules[j]
		if a.BlockedRuns != b.BlockedRuns {
			return a.BlockedRuns > b.BlockedRuns
		}
		return a.BlockedBy < b.BlockedBy
	})
	return out
}

// Summaries returns the blocks recorded for workflow at version within the
// TTL, most frequent first, then most recent, then by step ID.
func (l *Log) Summaries(workflow, version string) []Summary {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.ttl)
	type group struct{ step, blockedBy, missing string }
	byGroup := map[group]*Summary{}
	for _, e := range l.entries {
		if e.Kind != "" || e.Workflow != workflow || e.Version != version || e.At.Before(cutoff) {
			continue
		}
		g := group{string(e.Step), e.BlockedBy, string(e.MissingState)}
		s := byGroup[g]
		if s == nil {
			s = &Summary{Step: e.Step, BlockedBy: e.BlockedBy, MissingState: e.MissingState}
			byGroup[g] = s
		}
		s.Runs++
		if !e.At.Before(s.LastSeen) {
			s.LastSeen = e.At
			s.EstablishedBy = e.EstablishedBy
		}
	}
	out := make([]Summary, 0, len(byGroup))
	for _, s := range byGroup {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Runs != b.Runs {
			return a.Runs > b.Runs
		}
		if !a.LastSeen.Equal(b.LastSeen) {
			return a.LastSeen.After(b.LastSeen)
		}
		if a.Step != b.Step {
			return a.Step < b.Step
		}
		return a.MissingState < b.MissingState
	})
	return out
}

// CompactionError reports why the file could not be compacted when it was
// opened, or nil. Entries that expired are still left out of every summary, but
// they stay on disk until a later start compacts the file.
func (l *Log) CompactionError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.compactErr
}

// Summary returns the summary for one step, rule and missing state of workflow
// at version within the TTL, the same row Summaries would list, without
// building the rows for every other block. ok is false if it was never blocked.
func (l *Log) Summary(workflow, version string, step verify.StepID, blockedBy string, missing verify.State) (s Summary, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.ttl)
	for _, seq := range l.bySum[sumKey{workflow, version, step, blockedBy, missing}] {
		e := &l.entries[seq-l.base]
		if e.At.Before(cutoff) {
			continue
		}
		if !ok {
			s = Summary{Step: e.Step, BlockedBy: e.BlockedBy, MissingState: e.MissingState}
			ok = true
		}
		s.Runs++
		if !e.At.Before(s.LastSeen) {
			s.LastSeen = e.At
			s.EstablishedBy = e.EstablishedBy
		}
	}
	return s, ok
}

// Close closes the backing file, if any.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
