package governance

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin/ai/verify"
)

func TestPolicyCompilationAndEvaluation(t *testing.T) {
	ctx := context.Background()

	manifest := &PolicyManifest{
		ID:      "db-maintenance-policy",
		Version: "1.0",
		Name:    "Database Maintenance Safeguards",
		MinTier: TierCore,
		Rules: []RuleSpec{
			{
				Name:      "backup_before_drop",
				Kind:      RuleKindPrecedence,
				Forbidden: "prod_db_dropped",
				Requires:  "backup_validated",
			},
		},
		Steps: []StepSpec{
			{
				ID:           "take_backup",
				Establishes:  []verify.State{"backup_taken"},
				AllowedRoles: []string{"Operator", "Admin"},
			},
			{
				ID:           "validate_backup",
				Requires:     []verify.State{"backup_taken"},
				Establishes:  []verify.State{"backup_validated"},
				AllowedRoles: []string{"Operator", "Admin"},
			},
			{
				ID:           "drop_prod_db",
				Establishes:  []verify.State{"prod_db_dropped"},
				AllowedRoles: []string{"Admin"},
			},
		},
	}

	gate := NewFeatureGate(TierCore)
	compiled, err := CompilePolicy(manifest, gate)
	if err != nil {
		t.Fatalf("failed to compile policy: %v", err)
	}

	trace := verify.NewTrace()
	auditSink := NewMemoryAuditRecorder(nil)

	// 1. Operator tries to drop prod db directly -> Should be rejected by RBAC first (Operator not allowed to drop)
	eval, err := compiled.Evaluate(ctx, trace, "drop_prod_db", []string{"Operator"})
	if err != nil {
		t.Fatalf("unexpected error during eval: %v", err)
	}
	if eval.Allowed || eval.Decision != DecisionDeny {
		t.Errorf("expected RBAC deny, got allowed=%v decision=%s", eval.Allowed, eval.Decision)
	}

	// 2. Admin tries to drop prod db directly without backup -> Gated by ai/verify barrier (precedence rule violation)
	eval, err = compiled.Evaluate(ctx, trace, "drop_prod_db", []string{"Admin"})
	if err != nil {
		t.Fatalf("unexpected error during eval: %v", err)
	}
	if eval.Allowed || eval.Decision != DecisionViolation {
		t.Errorf("expected DecisionViolation, got allowed=%v decision=%s", eval.Allowed, eval.Decision)
	}

	// 3. Follow proper workflow: Take backup -> Validate backup -> Drop prod db
	eval, err = compiled.EvaluateAndCommit(ctx, trace, "take_backup", []string{"Operator"}, "agent-1", "t1", "w1", auditSink)
	if err != nil || !eval.Allowed {
		t.Fatalf("failed to execute take_backup: %v (eval: %+v)", err, eval)
	}

	eval, err = compiled.EvaluateAndCommit(ctx, trace, "validate_backup", []string{"Operator"}, "agent-1", "t1", "w1", auditSink)
	if err != nil || !eval.Allowed {
		t.Fatalf("failed to execute validate_backup: %v (eval: %+v)", err, eval)
	}

	// Now Admin can drop prod db
	eval, err = compiled.EvaluateAndCommit(ctx, trace, "drop_prod_db", []string{"Admin"}, "admin-user", "t1", "w1", auditSink)
	if err != nil || !eval.Allowed {
		t.Fatalf("failed to execute drop_prod_db after validation: %v (eval: %+v)", err, eval)
	}

	// Ensure audit sink recorded all 3 steps with cryptographic integrity intact
	if auditSink.Count() != 3 {
		t.Errorf("expected 3 audit events, got %d", auditSink.Count())
	}
	if err := auditSink.VerifyIntegrity(); err != nil {
		t.Errorf("audit chain integrity compromised: %v", err)
	}
}

func TestPolicyTierEnforcement(t *testing.T) {
	manifest := &PolicyManifest{
		ID:      "enterprise-compliance-policy",
		Name:    "Enterprise Compliance Policy",
		MinTier: TierEnterprise,
		Rules:   []RuleSpec{},
		Steps:   []StepSpec{},
	}

	// Feature gate with Core tier
	gateCore := NewFeatureGate(TierCore)
	_, err := CompilePolicy(manifest, gateCore)
	if err == nil {
		t.Fatalf("expected error compiling enterprise policy under core gate, got nil")
	}
	if !errors.Is(err, ErrCapabilityNotLicensed) {
		t.Errorf("expected ErrCapabilityNotLicensed, got %v", err)
	}

	// Feature gate with Enterprise tier
	gateEnt := NewFeatureGate(TierEnterprise)
	compiled, err := CompilePolicy(manifest, gateEnt)
	if err != nil {
		t.Fatalf("expected successful compilation under enterprise gate, got: %v", err)
	}
	if compiled == nil {
		t.Fatalf("compiled policy should not be nil")
	}
}
