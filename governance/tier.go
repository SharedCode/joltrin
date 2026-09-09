package governance

import (
	"errors"
	"fmt"
	"sync"
)

// Tier represents the licensing and operational tier of a Joltrin deployment.
type Tier string

const (
	// TierCore is the 100% free, MIT-licensed open-source embedded engine.
	// Includes full copy-on-write B-Tree, WAL, 2PC, erasure coding, AI memory,
	// in-memory vector similarity, embedded MCP server, A2A agent runtime, and local ai/verify barrier.
	TierCore Tier = "core"

	// TierPro provides policy-as-code runtime validation, signed audit export,
	// tamper-evident lineage, team-level workspace configs, and priority MCP gateways.
	TierPro Tier = "pro"

	// TierEnterprise provides multi-tenant RBAC, enterprise audit streaming (SIEM/Kafka),
	// fine-grained verification rules, custom invariant enforcement, and SLA guarantees.
	TierEnterprise Tier = "enterprise"

	// TierHosted is the managed cloud service with multi-region replication,
	// cloud-hosted MCP hubs, managed agent networks, and automated backups.
	TierHosted Tier = "hosted"
)

// Capability identifies a specific architectural feature or entitlement.
type Capability string

const (
	// Core capabilities (always available under MIT license)
	CapEmbeddedBTree      Capability = "core:btree"
	CapWAL2PC             Capability = "core:wal_2pc"
	CapErasureCoding      Capability = "core:erasure_coding"
	CapAgentMemory        Capability = "core:agent_memory"
	CapVectorSimilarity   Capability = "core:vector_similarity"
	CapEmbeddedMCP        Capability = "core:embedded_mcp"
	CapA2ARuntime         Capability = "core:a2a_runtime"
	CapLocalVerifyBarrier Capability = "core:verify_barrier"
	CapDeveloperOIDC      Capability = "core:developer_oidc"

	// Pro capabilities
	CapPolicyAsCode         Capability = "pro:policy_as_code"
	CapSignedAuditExport    Capability = "pro:signed_audit_export"
	CapTamperEvidentLineage Capability = "pro:tamper_evident_lineage"
	CapTeamWorkspaces       Capability = "pro:team_workspaces"
	CapPriorityMCPGateway   Capability = "pro:priority_mcp_gateway"

	// Enterprise capabilities
	CapMultiTenantRBAC         Capability = "enterprise:multi_tenant_rbac"
	CapEnterpriseSSO           Capability = "enterprise:sso"
	CapSIEMStreamingAudit      Capability = "enterprise:siem_streaming_audit"
	CapFineGrainedVerification Capability = "enterprise:fine_grained_verification"
	CapCustomInvariantEngine   Capability = "enterprise:custom_invariants"
	CapComplianceEnforcement   Capability = "enterprise:compliance_enforcement"

	// Hosted capabilities
	CapManagedInstances           Capability = "hosted:managed_instances"
	CapCloudMCPHub                Capability = "hosted:cloud_mcp_hub"
	CapMultiRegionReplication     Capability = "hosted:multi_region_replication"
	CapManagedCoordinationNetwork Capability = "hosted:managed_coordination"
	CapAutomatedSnapshots         Capability = "hosted:automated_snapshots"
)

var (
	// ErrCapabilityNotLicensed is returned when an operation requires an unentitled capability.
	ErrCapabilityNotLicensed = errors.New("governance: capability not licensed in current tier")

	// ErrInvalidTier is returned when an unknown tier is supplied.
	ErrInvalidTier = errors.New("governance: unrecognized tier")
)

// TierCapabilities returns the full set of capabilities entitled to a given tier.
// Each tier strictly builds upon the capabilities of the preceding tier.
func TierCapabilities(tier Tier) ([]Capability, error) {
	coreCaps := []Capability{
		CapEmbeddedBTree,
		CapWAL2PC,
		CapErasureCoding,
		CapAgentMemory,
		CapVectorSimilarity,
		CapEmbeddedMCP,
		CapA2ARuntime,
		CapLocalVerifyBarrier,
		CapDeveloperOIDC,
	}

	switch tier {
	case TierCore, "":
		return coreCaps, nil

	case TierPro:
		proCaps := []Capability{
			CapPolicyAsCode,
			CapSignedAuditExport,
			CapTamperEvidentLineage,
			CapTeamWorkspaces,
			CapPriorityMCPGateway,
		}
		return append(coreCaps, proCaps...), nil

	case TierEnterprise:
		proCaps, _ := TierCapabilities(TierPro)
		entCaps := []Capability{
			CapMultiTenantRBAC,
			CapEnterpriseSSO,
			CapSIEMStreamingAudit,
			CapFineGrainedVerification,
			CapCustomInvariantEngine,
			CapComplianceEnforcement,
		}
		return append(proCaps, entCaps...), nil

	case TierHosted:
		entCaps, _ := TierCapabilities(TierEnterprise)
		hostedCaps := []Capability{
			CapManagedInstances,
			CapCloudMCPHub,
			CapMultiRegionReplication,
			CapManagedCoordinationNetwork,
			CapAutomatedSnapshots,
		}
		return append(entCaps, hostedCaps...), nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidTier, tier)
	}
}

// Option configures a FeatureGate.
type Option func(*FeatureGate)

// WithOverride allows explicitly enabling or disabling a capability.
func WithOverride(cap Capability, enabled bool) Option {
	return func(fg *FeatureGate) {
		fg.overrides[cap] = enabled
	}
}

// FeatureGate manages capability checks, tier entitlements, and runtime feature toggles.
type FeatureGate struct {
	mu        sync.RWMutex
	tier      Tier
	overrides map[Capability]bool
}

// NewFeatureGate constructs a new FeatureGate for the requested Tier.
func NewFeatureGate(tier Tier, opts ...Option) *FeatureGate {
	fg := &FeatureGate{
		tier:      tier,
		overrides: make(map[Capability]bool),
	}
	for _, opt := range opts {
		opt(fg)
	}
	return fg
}

// Tier returns the currently active deployment tier.
func (fg *FeatureGate) Tier() Tier {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	return fg.tier
}

// Allows checks whether the specified capability is currently permitted.
func (fg *FeatureGate) Allows(cap Capability) bool {
	fg.mu.RLock()
	defer fg.mu.RUnlock()

	// Check manual overrides first
	if enabled, ok := fg.overrides[cap]; ok {
		return enabled
	}

	// Check tier capabilities
	caps, err := TierCapabilities(fg.tier)
	if err != nil {
		return false
	}
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

// Require asserts that a capability is allowed; returns ErrCapabilityNotLicensed if not.
func (fg *FeatureGate) Require(cap Capability) error {
	if !fg.Allows(cap) {
		return fmt.Errorf("%w: capability %q requires an upgraded tier (current: %s)", ErrCapabilityNotLicensed, cap, fg.Tier())
	}
	return nil
}

// SetOverride dynamically toggles a capability override.
func (fg *FeatureGate) SetOverride(cap Capability, enabled bool) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.overrides[cap] = enabled
}

// Capabilities returns all capabilities currently active on this gate.
func (fg *FeatureGate) Capabilities() []Capability {
	fg.mu.RLock()
	defer fg.mu.RUnlock()

	caps, _ := TierCapabilities(fg.tier)
	active := make(map[Capability]bool, len(caps))
	for _, c := range caps {
		active[c] = true
	}
	for c, enabled := range fg.overrides {
		if enabled {
			active[c] = true
		} else {
			delete(active, c)
		}
	}

	result := make([]Capability, 0, len(active))
	for c := range active {
		result = append(result, c)
	}
	return result
}
