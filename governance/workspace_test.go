package governance

import (
	"context"
	"errors"
	"testing"
)

func TestWorkspaceManager(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemoryWorkspaceManager()

	// 1. Create Core Tenant (limit 1 workspace)
	tenant, err := mgr.CreateTenant(ctx, "Acme Starter", TierCore)
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	ws1, err := mgr.CreateWorkspace(ctx, tenant.ID, "Default Workspace")
	if err != nil {
		t.Fatalf("failed to create workspace 1: %v", err)
	}

	// 2. Exceed quota on Core tenant
	_, err = mgr.CreateWorkspace(ctx, tenant.ID, "Second Workspace")
	if err == nil {
		t.Fatalf("expected quota exceeded error, got nil")
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}

	// 3. Context propagation
	ctxTenant := ContextWithTenant(ctx, tenant)
	ctxAll := ContextWithWorkspace(ctxTenant, ws1)

	retrievedTenant, ok := GetTenantFromContext(ctxAll)
	if !ok || retrievedTenant.ID != tenant.ID {
		t.Errorf("failed to retrieve tenant from context")
	}

	retrievedWs, ok := GetWorkspaceFromContext(ctxAll)
	if !ok || retrievedWs.ID != ws1.ID {
		t.Errorf("failed to retrieve workspace from context")
	}
}
