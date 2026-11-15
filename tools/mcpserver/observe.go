package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

// Observability and one guardrail for the barrier server. All of it is off
// unless an option turns it on, and none of it changes what the barrier
// allows or blocks.

// decision is what one execute_step call came to.
type decision struct {
	workflow  string
	trace     string
	step      string
	outcome   string // allowed, replayed, blocked, observed or invalid
	blockedBy string
	missing   verify.State
	attempts  int
	next      string
	stopped   bool // the retry limit turned the next action into stop_and_ask
	took      time.Duration
}

var durationBuckets = []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.25, 1}

// Metrics counts what the barrier decided. Serve Handler on a port of your
// choice, or scrape it with anything that reads the Prometheus text format.
type Metrics struct {
	mu      sync.Mutex
	steps   map[[2]string]uint64 // workflow, outcome
	blocks  map[[2]string]uint64 // workflow, rule
	stopped map[string]uint64    // workflow
	buckets []uint64
	count   uint64
	sum     float64
	started time.Time
}

// NewMetrics returns an empty set of counters.
func NewMetrics() *Metrics {
	return &Metrics{
		steps:   map[[2]string]uint64{},
		blocks:  map[[2]string]uint64{},
		stopped: map[string]uint64{},
		buckets: make([]uint64, len(durationBuckets)),
		started: time.Now(),
	}
}

func (m *Metrics) record(d decision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps[[2]string{d.workflow, d.outcome}]++
	if d.outcome == "blocked" {
		m.blocks[[2]string{d.workflow, d.blockedBy}]++
	}
	if d.stopped {
		m.stopped[d.workflow]++
	}
	secs := d.took.Seconds()
	m.count++
	m.sum += secs
	for i, le := range durationBuckets {
		if secs <= le {
			m.buckets[i]++
		}
	}
}

// Handler serves the counters in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.mu.Lock()
		defer m.mu.Unlock()
		var b strings.Builder
		fmt.Fprintf(&b, "# HELP joltrin_barrier_uptime_seconds Seconds since the barrier server started.\n# TYPE joltrin_barrier_uptime_seconds gauge\njoltrin_barrier_uptime_seconds %d\n", int64(time.Since(m.started).Seconds()))
		b.WriteString("# HELP joltrin_steps_total execute_step calls by workflow and outcome (allowed, replayed, blocked, observed, invalid).\n# TYPE joltrin_steps_total counter\n")
		for _, k := range sortedPairs(m.steps) {
			fmt.Fprintf(&b, "joltrin_steps_total{workflow=%s,outcome=%s} %d\n", quote(k[0]), quote(k[1]), m.steps[k])
		}
		b.WriteString("# HELP joltrin_blocks_total Blocked steps by workflow and the rule that blocked them.\n# TYPE joltrin_blocks_total counter\n")
		for _, k := range sortedPairs(m.blocks) {
			fmt.Fprintf(&b, "joltrin_blocks_total{workflow=%s,rule=%s} %d\n", quote(k[0]), quote(k[1]), m.blocks[k])
		}
		b.WriteString("# HELP joltrin_retry_limit_stops_total Blocks where the retry limit told the agent to stop and ask.\n# TYPE joltrin_retry_limit_stops_total counter\n")
		wfs := make([]string, 0, len(m.stopped))
		for wf := range m.stopped {
			wfs = append(wfs, wf)
		}
		sort.Strings(wfs)
		for _, wf := range wfs {
			fmt.Fprintf(&b, "joltrin_retry_limit_stops_total{workflow=%s} %d\n", quote(wf), m.stopped[wf])
		}
		b.WriteString("# HELP joltrin_step_duration_seconds Time to decide one execute_step call.\n# TYPE joltrin_step_duration_seconds histogram\n")
		for i, le := range durationBuckets {
			fmt.Fprintf(&b, "joltrin_step_duration_seconds_bucket{le=\"%g\"} %d\n", le, m.buckets[i])
		}
		fmt.Fprintf(&b, "joltrin_step_duration_seconds_bucket{le=\"+Inf\"} %d\njoltrin_step_duration_seconds_sum %g\njoltrin_step_duration_seconds_count %d\n", m.count, m.sum, m.count)
		io.WriteString(w, b.String())
	})
}

func sortedPairs(m map[[2]string]uint64) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}

// quote renders a label value for the Prometheus text format.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

// decisionLog writes one JSON line per decision.
type decisionLog struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *decisionLog) write(d decision) {
	line := struct {
		Time      string `json:"ts"`
		Event     string `json:"event"`
		Workflow  string `json:"workflow,omitempty"`
		Trace     string `json:"trace_id,omitempty"`
		Step      string `json:"step,omitempty"`
		Outcome   string `json:"outcome"`
		BlockedBy string `json:"blocked_by,omitempty"`
		Missing   string `json:"missing_state,omitempty"`
		Attempts  int    `json:"attempts,omitempty"`
		Next      string `json:"next,omitempty"`
		Stopped   bool   `json:"retry_limit_stop,omitempty"`
		Micros    int64  `json:"duration_us"`
	}{
		Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "execute_step",
		Workflow: d.workflow, Trace: d.trace, Step: d.step, Outcome: d.outcome,
		BlockedBy: d.blockedBy, Missing: string(d.missing), Attempts: d.attempts,
		Next: d.next, Stopped: d.stopped, Micros: d.took.Microseconds(),
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(append(b, '\n'))
}

// WithMetrics counts every execute_step decision in m.
func WithMetrics(m *Metrics) Option {
	return func(c *config) { c.metrics = m }
}

// WithDecisionLog writes one JSON line per execute_step decision to w: the
// workflow, trace, step, outcome, the rule that blocked it and how long the
// check took. Never give it the stream an MCP stdio server answers on.
func WithDecisionLog(w io.Writer) Option {
	return func(c *config) {
		if w != nil {
			c.decisions = &decisionLog{w: w}
		}
	}
}

// WithRetryLimit is a guardrail against an agent that keeps retrying. Once
// the same step has been blocked for the same reason more than n times in one
// trace, the result says to stop and ask a person instead of running more
// steps. It changes advice only: the step is blocked either way.
func WithRetryLimit(n int) Option {
	return func(c *config) { c.retryLimit = n }
}

// WithObserveOnly turns the barrier into a dry run. A step that would have
// been blocked is let through and committed, and the result says it would have
// been blocked and why. The decision log and metrics record it as "observed".
// It is for trying a runbook on real agent traffic before enforcing it, and
// is not safe to leave on: nothing is stopped. Off by default.
func WithObserveOnly() Option {
	return func(c *config) { c.observeOnly = true }
}

func (c *config) observe(d decision) {
	if c.metrics != nil {
		c.metrics.record(d)
	}
	if c.decisions != nil {
		c.decisions.write(d)
	}
}

// retryLimitHint is the message that goes with a retry-limit stop.
func retryLimitHint(attempts, limit int) string {
	return fmt.Sprintf("This step has been blocked %d times for the same reason in this trace, past the limit of %d. Stop and ask a person.", attempts, limit)
}

// observeOnlyHint goes with every result that observe mode let through.
const observeOnlyHint = "Observe mode is on, so nothing is blocked. This step would have been blocked in enforcing mode. Fix the order before turning enforcement on."
