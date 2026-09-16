// Package runbookstore holds the registered ai/verify.Workflow runbooks and
// their in-progress execution traces, shared by every protocol front-end
// this repo exposes them through (tools/mcpserver, tools/a2aagent). Sharing
// one Store is what makes the two protocols actually interoperable rather
// than two isolated demos: a step executed via A2A advances the same trace
// an MCP client can inspect with validate_step, and vice versa.
package runbookstore

import (
	"fmt"
	"sort"
	"sync"

	"github.com/sharedcode/joltrin/ai/verify"
)

// DefaultMaxTraces bounds how many execution traces a Store retains. A
// trace is created on first sight of a trace_id, so without a cap any
// client that can reach a protocol server (tools/a2aagent serves over the
// network) can allocate unbounded memory just by sending fresh trace_ids.
const DefaultMaxTraces = 4096

// Store holds the runbooks this server can validate and execute steps
// against, plus one execution Trace per trace_id. Safe for concurrent use.
type Store struct {
	mu        sync.RWMutex
	workflows map[string]*verify.Workflow
	traces    map[string]*verify.Trace
	// traceOrder records trace_ids in creation order so the oldest can be
	// evicted once maxTraces is reached.
	traceOrder []string
	maxTraces  int
}

// New returns an empty Store holding at most DefaultMaxTraces traces. Use
// RegisterWorkflow to add runbooks before serving.
func New() *Store {
	return NewWithMaxTraces(DefaultMaxTraces)
}

// NewWithMaxTraces returns an empty Store that retains at most maxTraces
// execution traces, evicting the oldest first. A value below 1 falls back
// to DefaultMaxTraces.
//
// Eviction is safe in the direction that matters: a trace_id whose trace
// was evicted starts over with nothing established, so a step that needs a
// precondition is blocked rather than allowed. Losing a trace can only make
// the barrier stricter, never permissive.
func NewWithMaxTraces(maxTraces int) *Store {
	if maxTraces < 1 {
		maxTraces = DefaultMaxTraces
	}
	return &Store{
		workflows: make(map[string]*verify.Workflow),
		traces:    make(map[string]*verify.Trace),
		maxTraces: maxTraces,
	}
}

// RegisterWorkflow makes a runbook available under name. It runs
// VerifyReachability against the workflow immediately and returns an error
// if that fails: a runbook whose rollback path can dead-end should never be
// registered in the first place, catching that at registration time is
// strictly better than discovering it mid-incident.
func (s *Store) RegisterWorkflow(name string, wf *verify.Workflow) error {
	if err := wf.VerifyReachability(); err != nil {
		return fmt.Errorf("runbookstore: refusing to register workflow %q: %w", name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows[name] = wf
	return nil
}

// Workflow returns the registered workflow by name, if any.
func (s *Store) Workflow(name string) (*verify.Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wf, ok := s.workflows[name]
	return wf, ok
}

// WorkflowNames returns the names of every registered workflow, sorted.
// Intended for an "unknown workflow" response, so a caller that mistyped or
// guessed a name gets a correction it can act on rather than a dead end.
func (s *Store) WorkflowNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.workflows))
	for name := range s.workflows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TraceFor returns the Trace for traceID, creating a fresh one on first
// use and evicting the oldest trace once the store is at its cap.
func (s *Store) TraceFor(traceID string) *verify.Trace {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr, ok := s.traces[traceID]; ok {
		return tr
	}

	for len(s.traceOrder) >= s.maxTraces {
		oldest := s.traceOrder[0]
		s.traceOrder = s.traceOrder[1:]
		delete(s.traces, oldest)
	}

	tr := verify.NewTrace()
	s.traces[traceID] = tr
	s.traceOrder = append(s.traceOrder, traceID)
	return tr
}

// TraceCount reports how many traces the store currently retains.
func (s *Store) TraceCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.traces)
}
