package intent

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// A Contract is the scope an owner signs for one agent on one task, such as
// "review PR 412: read these files, post one comment". A ScopeLock enforces it
// at the tool boundary, so an agent that drifts into other repos, other tools
// or more calls than it was given is blocked and the attempt is recorded.
// A lock also checks that each finding an agent reports quotes text that is
// really in the material it was given.
//
// Fan out forty agents with forty contracts and Report shows which ones tried
// to leave their lane and which findings had no support.
type Contract struct {
	ID     string `json:"id"`
	Tenant string `json:"tenant"`
	Agent  string `json:"agent"`
	Task   string `json:"task"`
	// Tools and Resources are glob allow-lists (path.Match). An action must
	// match one of each. An empty list allows nothing.
	Tools     []string `json:"tools"`
	Resources []string `json:"resources"`
	// Env, when set, must equal the action's Env.
	Env string `json:"env,omitempty"`
	// MaxActions caps allowed actions. Zero means no cap.
	MaxActions int    `json:"max_actions,omitempty"`
	Issuer     string `json:"issuer"`
	IssuedAt   int64  `json:"issued_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Sig        []byte `json:"sig"`
}

// SignContract signs c with the owner's key and fills ID, Issuer and the times.
func SignContract(priv ed25519.PrivateKey, c Contract, now time.Time, ttl time.Duration) Contract {
	c.Issuer = KeyID(priv.Public().(ed25519.PublicKey))
	if c.ID == "" {
		c.ID = newID("ct")
	}
	c.IssuedAt, c.ExpiresAt, c.Sig = now.Unix(), now.Add(ttl).Unix(), nil
	c.Sig = ed25519.Sign(priv, signedBytes("contract", c))
	return c
}

func (c Contract) verify(pub ed25519.PublicKey) bool {
	sig := c.Sig
	c.Sig = nil
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, signedBytes("contract", c), sig)
}

// Reason codes returned by ScopeLock.
const (
	CodeWrongAgent         = "wrong_agent"
	CodeWrongTenant        = "wrong_tenant"
	CodeExpired            = "contract_expired"
	CodeToolOutOfScope     = "tool_out_of_scope"
	CodeResourceOutOfScope = "resource_out_of_scope"
	CodeWrongEnv           = "wrong_env"
	CodeBudget             = "budget_exhausted"

	CodeNoEvidence      = "no_evidence"
	CodeWeakQuote       = "quote_too_short"
	CodeCitedOutOfScope = "cited_out_of_scope"
	CodeSourceMissing   = "source_unreadable"
	CodeQuoteNotFound   = "quote_not_found"
)

// minQuote is the shortest quote accepted as evidence. A one-word quote
// matches almost any file and proves nothing.
const minQuote = 12

// Attempt is one action an agent tried, with the lock's answer.
type Attempt struct {
	Seq      int     `json:"seq"`
	At       int64   `json:"at"`
	Tool     string  `json:"tool"`
	Resource string  `json:"resource"`
	Verdict  Verdict `json:"verdict"`
	Code     string  `json:"code,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// Citation points at text in a resource. Quote must appear in it.
type Citation struct {
	Resource string `json:"resource"`
	Quote    string `json:"quote"`
}

// Claim is a finding an agent reports, with the text that supports it.
type Claim struct {
	Text  string     `json:"text"`
	Cites []Citation `json:"cites"`
}

// Source returns the text of a resource, read by the caller's own code, never
// by the agent.
type Source interface {
	Read(resource string) (string, error)
}

// MapSource is a Source backed by a map, handy for a PR's changed files.
type MapSource map[string]string

// Read implements Source.
func (m MapSource) Read(resource string) (string, error) {
	s, ok := m[resource]
	if !ok {
		return "", errors.New("not found")
	}
	return s, nil
}

// ClaimResult is the verdict on one claim.
type ClaimResult struct {
	Text     string `json:"text"`
	Grounded bool   `json:"grounded"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// ScopeLock enforces one Contract. It is safe for concurrent use.
type ScopeLock struct {
	mu       sync.Mutex
	c        Contract
	now      func() time.Time
	used     int
	attempts []Attempt
	claims   []ClaimResult
}

// NewScopeLock checks the contract was signed by the owner and returns a lock.
// now may be nil.
func NewScopeLock(c Contract, owner ed25519.PublicKey, now func() time.Time) (*ScopeLock, error) {
	if !c.verify(owner) || c.Issuer != KeyID(owner) {
		return nil, errors.New("contract is not signed by the owner key")
	}
	if c.Agent == "" || c.Tenant == "" {
		return nil, errors.New("contract needs a tenant and an agent")
	}
	if now == nil {
		now = time.Now
	}
	return &ScopeLock{c: c, now: now}, nil
}

func anyGlob(patterns []string, s string) bool {
	for _, p := range patterns {
		if glob(p, s) {
			return true
		}
	}
	return false
}

// Check decides whether the agent may take a. Allowed actions count against
// MaxActions. Every call, allowed or blocked, is recorded.
func (l *ScopeLock) Check(a Action) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	code, msg := "", ""
	switch {
	case a.Tenant != l.c.Tenant:
		code, msg = CodeWrongTenant, "this contract belongs to another tenant"
	case a.Agent != l.c.Agent:
		code, msg = CodeWrongAgent, "this contract was issued to a different agent"
	case now.Unix() >= l.c.ExpiresAt:
		code, msg = CodeExpired, "the contract for this task has expired"
	case !anyGlob(l.c.Tools, a.Tool):
		code, msg = CodeToolOutOfScope, fmt.Sprintf("%s is not part of task %q", a.Tool, l.c.Task)
	case !anyGlob(l.c.Resources, a.Resource):
		code, msg = CodeResourceOutOfScope, fmt.Sprintf("%s is outside the scope of task %q", a.Resource, l.c.Task)
	case l.c.Env != "" && a.Env != l.c.Env:
		code, msg = CodeWrongEnv, "this contract does not cover that environment"
	case l.c.MaxActions > 0 && l.used >= l.c.MaxActions:
		code, msg = CodeBudget, fmt.Sprintf("the %d-action budget for this task is used up", l.c.MaxActions)
	}

	d := Decision{Verdict: Allow, Summary: "Inside the task scope.", AppliedRules: []string{l.c.ID}, ActionHash: a.Hash()}
	if code != "" {
		d.Verdict, d.Summary = Block, msg
		d.Reasons = []Reason{{Code: code, RuleID: l.c.ID, Message: msg}}
	} else {
		l.used++
	}
	l.attempts = append(l.attempts, Attempt{
		Seq: len(l.attempts) + 1, At: now.Unix(), Tool: a.Tool, Resource: a.Resource,
		Verdict: d.Verdict, Code: code, Message: msg,
	})
	return d
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// CheckClaim accepts a claim only if every citation names a resource inside
// the contract and quotes text that appears in it. It proves the agent is
// pointing at real text. It does not prove the claim follows from that text.
func (l *ScopeLock) CheckClaim(c Claim, src Source) ClaimResult {
	res := ClaimResult{Text: c.Text, Grounded: true}
	fail := func(code, msg string) ClaimResult {
		return ClaimResult{Text: c.Text, Code: code, Message: msg}
	}
	switch {
	case len(c.Cites) == 0:
		res = fail(CodeNoEvidence, "the claim cites nothing")
	default:
		for _, ct := range c.Cites {
			q := squash(ct.Quote)
			if len(q) < minQuote {
				res = fail(CodeWeakQuote, fmt.Sprintf("the quote from %s is too short to prove anything", ct.Resource))
				break
			}
			if !anyGlob(l.c.Resources, ct.Resource) {
				res = fail(CodeCitedOutOfScope, fmt.Sprintf("%s was not part of this task", ct.Resource))
				break
			}
			body, err := src.Read(ct.Resource)
			if err != nil {
				res = fail(CodeSourceMissing, fmt.Sprintf("%s could not be read", ct.Resource))
				break
			}
			if !strings.Contains(squash(body), q) {
				res = fail(CodeQuoteNotFound, fmt.Sprintf("the quote is not in %s", ct.Resource))
				break
			}
		}
	}
	l.mu.Lock()
	l.claims = append(l.claims, res)
	l.mu.Unlock()
	return res
}

// Report is what one lock saw.
type Report struct {
	Contract string        `json:"contract"`
	Agent    string        `json:"agent"`
	Task     string        `json:"task"`
	Allowed  int           `json:"allowed"`
	Blocked  int           `json:"blocked"`
	Attempts []Attempt     `json:"attempts"`
	Claims   []ClaimResult `json:"claims"`
}

// Report returns a copy of everything the lock recorded.
func (l *ScopeLock) Report() Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := Report{Contract: l.c.ID, Agent: l.c.Agent, Task: l.c.Task,
		Attempts: append([]Attempt(nil), l.attempts...), Claims: append([]ClaimResult(nil), l.claims...)}
	for _, a := range l.attempts {
		if a.Verdict == Allow {
			r.Allowed++
		} else {
			r.Blocked++
		}
	}
	return r
}

// Drifted reports whether the agent tried anything outside scope or reported
// a claim that was not grounded.
func (r Report) Drifted() bool {
	if r.Blocked > 0 {
		return true
	}
	for _, c := range r.Claims {
		if !c.Grounded {
			return true
		}
	}
	return false
}

// SwarmSummary rolls many reports into one view for the person who started
// the run.
type SwarmSummary struct {
	Agents         int      `json:"agents"`
	Allowed        int      `json:"allowed"`
	Blocked        int      `json:"blocked"`
	ClaimsGrounded int      `json:"claims_grounded"`
	ClaimsRejected int      `json:"claims_rejected"`
	Drifted        []string `json:"drifted"`
}

// Summarize combines the reports of a run. Drifted lists the agents to read
// first, sorted.
func Summarize(reports ...Report) SwarmSummary {
	s := SwarmSummary{Agents: len(reports), Drifted: []string{}}
	for _, r := range reports {
		s.Allowed += r.Allowed
		s.Blocked += r.Blocked
		for _, c := range r.Claims {
			if c.Grounded {
				s.ClaimsGrounded++
			} else {
				s.ClaimsRejected++
			}
		}
		if r.Drifted() {
			s.Drifted = append(s.Drifted, r.Agent)
		}
	}
	sort.Strings(s.Drifted)
	return s
}
