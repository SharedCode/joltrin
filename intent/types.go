// Package intent is Joltrin Private Intent: a vault of explicit, owner-approved
// operating rules for AI agents, and an engine that decides whether one
// proposed action is allowed right now.
//
// An agent proposes an Action. The Engine checks it against the active rules
// in the Vault, against signed Evidence that prerequisites happened (a backup
// was validated, tests passed), and against signed Approvals from the people
// the rules name. The answer is ALLOW, BLOCK or REQUIRE_APPROVAL. An ALLOW
// comes with a signed Receipt bound to the exact action and the policy that
// applied. The Guard on the executing side refuses to run anything without a
// valid, unused receipt.
//
// What this does not do: it cannot stop an agent that has its own path to the
// target system. It only works when the executor (the thing holding the
// credentials) runs the action through a Guard. A receipt proves a decision
// was made. It does not prove the action ran or succeeded.
//
// Privacy: a rule has a public Label and an optional private Preference. The
// Preference is stored sealed and never appears in a Decision, a Receipt, a
// log line or the policy digest. Everything the verifier needs is in the
// public fields.
package intent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Verdict is the result of evaluating one action.
type Verdict string

const (
	Allow           Verdict = "ALLOW"
	Block           Verdict = "BLOCK"
	RequireApproval Verdict = "REQUIRE_APPROVAL"
)

// Mode says what a matching rule does. A grant lets an agent act, subject to
// its Requires list. A deny always blocks. With no matching grant the answer
// is BLOCK: the engine is default-deny.
type Mode string

const (
	Grant Mode = "grant"
	Deny  Mode = "deny"
)

// RequirementKind is what a Requirement asks for.
type RequirementKind string

const (
	// NeedEvidence is a state attested by a trusted issuer, such as
	// "backup_validated" signed by the backup system.
	NeedEvidence RequirementKind = "evidence"
	// NeedApproval is a signed approval of this exact action by someone
	// holding Role.
	NeedApproval RequirementKind = "approval"
)

// Requirement is one prerequisite a grant imposes.
type Requirement struct {
	Kind RequirementKind `json:"kind"`
	// State is the attested fact, for NeedEvidence.
	State string `json:"state,omitempty"`
	// Role is who must approve, for NeedApproval.
	Role string `json:"role,omitempty"`
	// Describe is the short public phrase used in block messages, such as
	// "verified backup" or "owner approval".
	Describe string `json:"describe"`
}

func (r Requirement) key() string {
	if r.Kind == NeedEvidence {
		return "evidence:" + r.State
	}
	return "approval:" + r.Role
}

// Provenance records why a rule exists.
type Provenance struct {
	// Source is "explicit" when the owner wrote it, or "suggestion" when the
	// owner accepted a suggestion. Nothing is ever created automatically.
	Source string    `json:"source"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// Rule is one record in the vault.
type Rule struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Preference is the owner's private wording. It is sealed at rest and is
	// excluded from the policy digest, decisions and receipts.
	Preference string `json:"preference,omitempty"`
	Owner      string `json:"owner"`
	Purpose    string `json:"purpose"`

	Mode     Mode          `json:"mode"`
	Agent    string        `json:"agent"`
	Tool     string        `json:"tool"`
	Resource string        `json:"resource"`
	Env      string        `json:"env,omitempty"`
	Requires []Requirement `json:"requires,omitempty"`

	Provenance Provenance `json:"provenance"`
	CreatedAt  time.Time  `json:"created_at"`
	// ExpiresAt is zero for a rule that does not expire.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Version is the vault version at which this rule last changed.
	Version   int       `json:"version"`
	Revoked   bool      `json:"revoked"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

func (r *Rule) active(now time.Time) bool {
	if r.Revoked {
		return false
	}
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

func (r *Rule) matches(a Action) bool {
	return glob(r.Agent, a.Agent) && glob(r.Tool, a.Tool) && glob(r.Resource, a.Resource) &&
		(r.Env == "" || r.Env == a.Env)
}

func glob(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

// validate rejects a rule the engine could not evaluate safely.
func (r *Rule) validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("intent: rule needs an id")
	case r.Label == "":
		return fmt.Errorf("intent: rule %q needs a public label", r.ID)
	case r.Owner == "":
		return fmt.Errorf("intent: rule %q needs an owner", r.ID)
	case r.Purpose == "":
		return fmt.Errorf("intent: rule %q needs a purpose", r.ID)
	case r.Mode != Grant && r.Mode != Deny:
		return fmt.Errorf("intent: rule %q has unknown mode %q", r.ID, r.Mode)
	case r.Mode == Deny && len(r.Requires) > 0:
		return fmt.Errorf("intent: deny rule %q cannot have requirements", r.ID)
	}
	for _, p := range []string{r.Agent, r.Tool, r.Resource} {
		if p == "" {
			return fmt.Errorf("intent: rule %q needs an agent, tool and resource scope", r.ID)
		}
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("intent: rule %q has a bad pattern %q: %w", r.ID, p, err)
		}
	}
	for _, q := range r.Requires {
		switch {
		case q.Kind == NeedEvidence && q.State != "":
		case q.Kind == NeedApproval && q.Role != "":
		default:
			return fmt.Errorf("intent: rule %q has an incomplete requirement", r.ID)
		}
		if q.Describe == "" {
			return fmt.Errorf("intent: rule %q requirement needs a describe phrase", r.ID)
		}
	}
	return nil
}

// publicView is what the policy digest covers: everything that changes a
// decision, nothing the owner wrote privately.
type publicView struct {
	ID        string        `json:"id"`
	Label     string        `json:"label"`
	Mode      Mode          `json:"mode"`
	Agent     string        `json:"agent"`
	Tool      string        `json:"tool"`
	Resource  string        `json:"resource"`
	Env       string        `json:"env"`
	Requires  []Requirement `json:"requires"`
	ExpiresAt int64         `json:"expires_at"`
	Version   int           `json:"version"`
	Revoked   bool          `json:"revoked"`
}

func (r *Rule) public() publicView {
	v := publicView{ID: r.ID, Label: r.Label, Mode: r.Mode, Agent: r.Agent, Tool: r.Tool,
		Resource: r.Resource, Env: r.Env, Version: r.Version, Revoked: r.Revoked}
	v.Requires = append([]Requirement{}, r.Requires...)
	sort.Slice(v.Requires, func(i, j int) bool { return v.Requires[i].key() < v.Requires[j].key() })
	if !r.ExpiresAt.IsZero() {
		v.ExpiresAt = r.ExpiresAt.Unix()
	}
	return v
}

// Action is what an agent proposes to do.
type Action struct {
	Tenant   string `json:"tenant"`
	Agent    string `json:"agent"`
	Tool     string `json:"tool"`
	Resource string `json:"resource"`
	Env      string `json:"env"`
	// Params are the call arguments that matter to the decision. Keep
	// secrets out: only a digest of Params enters the action hash, but the
	// caller decides what goes in.
	Params map[string]string `json:"params,omitempty"`
}

// Hash is the canonical digest receipts and approvals bind to. Field order is
// fixed by the struct and encoding/json sorts map keys, so equal actions
// always hash equal.
func (a Action) Hash() string {
	b, _ := json.Marshal(a)
	sum := sha256.Sum256(append([]byte("joltrin-intent/action/v1\n"), b...))
	return hex.EncodeToString(sum[:])
}

func (a Action) complete() bool {
	return a.Tenant != "" && a.Agent != "" && a.Tool != "" && a.Resource != "" && a.Env != ""
}

// Reason explains one part of a decision without exposing private text.
type Reason struct {
	Code    string `json:"code"`
	RuleID  string `json:"rule_id,omitempty"`
	Message string `json:"message"`
}

// Missing names one prerequisite that is not satisfied.
type Missing struct {
	Kind     RequirementKind `json:"kind"`
	State    string          `json:"state,omitempty"`
	Role     string          `json:"role,omitempty"`
	Describe string          `json:"describe"`
	RuleID   string          `json:"rule_id"`
}

// Decision is the structured answer for one action.
type Decision struct {
	Verdict      Verdict   `json:"verdict"`
	Summary      string    `json:"summary"`
	Reasons      []Reason  `json:"reasons"`
	Missing      []Missing `json:"missing,omitempty"`
	AppliedRules []string  `json:"applied_rules"`
	PolicyDigest string    `json:"policy_digest"`
	PolicyVer    int       `json:"policy_version"`
	ActionHash   string    `json:"action_hash"`

	evidenceIDs []string
	approvalIDs []string
}

func joinPhrases(p []string) string {
	switch len(p) {
	case 0:
		return ""
	case 1:
		return p[0]
	}
	return strings.Join(p[:len(p)-1], ", ") + " and " + p[len(p)-1]
}
