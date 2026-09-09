package governance

import (
	"context"
	"errors"
	"fmt"

	"github.com/sharedcode/joltrin/ai/verify"
)

// RuleKind classifies declarative safety or invariant constraints.
type RuleKind string

const (
	RuleKindPrecedence   RuleKind = "precedence"
	RuleKindReachability RuleKind = "reachability"
)

// RuleSpec defines a declarative governance constraint.
type RuleSpec struct {
	Name        string       `json:"name"`
	Kind        RuleKind     `json:"kind"`
	Forbidden   verify.State `json:"forbidden,omitempty"`
	Requires    verify.State `json:"requires,omitempty"`
	Target      verify.State `json:"target,omitempty"`
	Description string       `json:"description,omitempty"`
}

// StepSpec defines an actionable operational step and its security requirements.
type StepSpec struct {
	ID           verify.StepID  `json:"id"`
	Requires     []verify.State `json:"requires,omitempty"`
	Establishes  []verify.State `json:"establishes,omitempty"`
	AllowedRoles []string       `json:"allowed_roles,omitempty"`
	Description  string         `json:"description,omitempty"`
}

// PolicyManifest is a declarative, serializable Policy-as-Code specification.
type PolicyManifest struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	MinTier     Tier       `json:"min_tier"`
	Rules       []RuleSpec `json:"rules"`
	Steps       []StepSpec `json:"steps"`
}

// CompiledPolicy encapsulates the verified runtime workflow and access-control matrix.
type CompiledPolicy struct {
	Manifest         *PolicyManifest
	Workflow         *verify.Workflow
	roleRequirements map[verify.StepID][]string
}

// EvaluationResult contains the detailed outcome of a policy barrier check.
type EvaluationResult struct {
	Allowed   bool              `json:"allowed"`
	Decision  Decision          `json:"decision"`
	StepID    verify.StepID     `json:"step_id"`
	Reason    string            `json:"reason"`
	Violation *verify.Violation `json:"violation,omitempty"`
}

// CompilePolicy compiles a declarative PolicyManifest into an executable ai/verify.Workflow.
// If the manifest specifies a MinTier, the provided FeatureGate is checked.
func CompilePolicy(manifest *PolicyManifest, gate *FeatureGate) (*CompiledPolicy, error) {
	if manifest == nil {
		return nil, errors.New("governance: policy manifest cannot be nil")
	}

	if manifest.MinTier != "" && gate != nil {
		caps, err := TierCapabilities(gate.Tier())
		if err != nil {
			return nil, err
		}
		requiredCaps, err := TierCapabilities(manifest.MinTier)
		if err != nil {
			return nil, err
		}
		if len(caps) < len(requiredCaps) {
			return nil, fmt.Errorf("%w: policy %q requires tier %s (active: %s)",
				ErrCapabilityNotLicensed, manifest.ID, manifest.MinTier, gate.Tier())
		}
	}

	steps := make([]verify.Step, 0, len(manifest.Steps))
	roleReqs := make(map[verify.StepID][]string, len(manifest.Steps))

	for _, s := range manifest.Steps {
		steps = append(steps, verify.Step{
			ID:          s.ID,
			Requires:    s.Requires,
			Establishes: s.Establishes,
		})
		if len(s.AllowedRoles) > 0 {
			roleReqs[s.ID] = append([]string(nil), s.AllowedRoles...)
		}
	}

	safetyRules := make([]verify.SafetyRule, 0)
	reachabilityRules := make([]verify.ReachabilityRule, 0)

	for _, r := range manifest.Rules {
		switch r.Kind {
		case RuleKindPrecedence:
			safetyRules = append(safetyRules, verify.SafetyRule{
				Name:      r.Name,
				Forbidden: r.Forbidden,
				Requires:  r.Requires,
			})
		case RuleKindReachability:
			reachabilityRules = append(reachabilityRules, verify.ReachabilityRule{
				Name:   r.Name,
				Target: r.Target,
			})
		default:
			return nil, fmt.Errorf("governance: unknown rule kind %q in rule %q", r.Kind, r.Name)
		}
	}

	wf, err := verify.NewWorkflow(steps, safetyRules, reachabilityRules)
	if err != nil {
		return nil, fmt.Errorf("governance: failed to construct verification workflow: %w", err)
	}

	return &CompiledPolicy{
		Manifest:         manifest,
		Workflow:         wf,
		roleRequirements: roleReqs,
	}, nil
}

// Evaluate evaluates whether a step is safe and authorized to execute without committing.
func (cp *CompiledPolicy) Evaluate(ctx context.Context, trace *verify.Trace, stepID verify.StepID, callerRoles []string) (*EvaluationResult, error) {
	// 1. Check Role-Based Access Control
	if allowedRoles, hasRoles := cp.roleRequirements[stepID]; hasRoles && len(allowedRoles) > 0 {
		authorized := false
		for _, ar := range allowedRoles {
			if ar == "*" {
				authorized = true
				break
			}
			for _, cr := range callerRoles {
				if cr == ar || cr == "Admin" {
					authorized = true
					break
				}
			}
			if authorized {
				break
			}
		}
		if !authorized {
			return &EvaluationResult{
				Allowed:  false,
				Decision: DecisionDeny,
				StepID:   stepID,
				Reason:   fmt.Sprintf("caller roles %v not authorized for step %q (allowed: %v)", callerRoles, stepID, allowedRoles),
			}, nil
		}
	}

	// 2. Check ai/verify Barrier Certificate
	if err := cp.Workflow.CheckSafety(trace, stepID); err != nil {
		var v *verify.Violation
		if errors.As(err, &v) {
			return &EvaluationResult{
				Allowed:   false,
				Decision:  DecisionViolation,
				StepID:    stepID,
				Reason:    v.Message,
				Violation: v,
			}, nil
		}
		return nil, err
	}

	return &EvaluationResult{
		Allowed:  true,
		Decision: DecisionAllow,
		StepID:   stepID,
		Reason:   "preconditions met and safety rules satisfied",
	}, nil
}

// EvaluateAndCommit evaluates safety, commits the step to the trace if valid,
// and optionally logs an immutable audit event to the provided sink.
func (cp *CompiledPolicy) EvaluateAndCommit(
	ctx context.Context,
	trace *verify.Trace,
	stepID verify.StepID,
	callerRoles []string,
	actorID string,
	tenantID string,
	workspaceID string,
	sink AuditSink,
) (*EvaluationResult, error) {
	eval, err := cp.Evaluate(ctx, trace, stepID, callerRoles)
	if err != nil {
		return nil, err
	}

	if eval.Allowed {
		if err := cp.Workflow.CheckAndCommit(trace, stepID); err != nil {
			var v *verify.Violation
			if errors.As(err, &v) {
				eval.Allowed = false
				eval.Decision = DecisionViolation
				eval.Violation = v
				eval.Reason = v.Message
			} else {
				return nil, err
			}
		}
	}

	if sink != nil {
		_ = sink.Record(ctx, &AuditEvent{
			TenantID:    tenantID,
			WorkspaceID: workspaceID,
			ActorID:     actorID,
			Action:      string(stepID),
			Resource:    cp.Manifest.ID,
			Decision:    eval.Decision,
			Reason:      eval.Reason,
		})
	}

	return eval, nil
}
