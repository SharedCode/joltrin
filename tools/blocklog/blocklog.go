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

// Entry is one recorded block.
type Entry struct {
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
	workflow, version, trace string
	step                     verify.StepID
	blockedBy                string
	missing                  verify.State
}

func (e Entry) key() key {
	return key{e.Workflow, e.Version, e.TraceID, e.Step, e.BlockedBy, e.MissingState}
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
	mu         sync.Mutex
	entries    []Entry
	seen       map[key]struct{}
	file       *os.File
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

func newLog(opts []Option) *Log {
	l := &Log{
		seen:       make(map[key]struct{}),
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
		if err := l.rewrite(path); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("blocklog: open %s: %w", path, err)
	}
	l.file = f
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
	l.entries = append(l.entries, e)
	l.seen[e.key()] = struct{}{}
}

// trim drops the oldest entries until the cap holds.
func (l *Log) trim() {
	for len(l.entries) > l.maxEntries {
		delete(l.seen, l.entries[0].key())
		l.entries = l.entries[1:]
	}
}

// Record stores e unless the same run already recorded the same block, and
// reports whether it was new. A failure to write the file is returned, but the
// entry still counts in memory. Callers should treat that as non-fatal:
// recording must never change what the barrier does.
func (l *Log) Record(e Entry) (added bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	b, err := json.Marshal(e)
	if err != nil {
		return true, fmt.Errorf("blocklog: encode entry: %w", err)
	}
	if _, err := l.file.Write(append(b, '\n')); err != nil {
		return true, fmt.Errorf("blocklog: write entry: %w", err)
	}
	return true, nil
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
		if e.Workflow != workflow || e.Version != version || e.At.Before(cutoff) {
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
