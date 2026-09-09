package governance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTenantNotFound    = errors.New("governance: tenant not found")
	ErrWorkspaceNotFound = errors.New("governance: workspace not found")
	ErrQuotaExceeded     = errors.New("governance: resource quota exceeded for tenant tier")
)

// Tenant represents an organizational boundary owning workspaces and policies.
type Tenant struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Tier          Tier      `json:"tier"`
	MaxWorkspaces int       `json:"max_workspaces"`
	MaxAgents     int       `json:"max_agents"`
	CreatedAt     time.Time `json:"created_at"`
}

// Workspace represents an isolated runtime environment within a Tenant.
type Workspace struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Name      string            `json:"name"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// WorkspaceManager defines the tenant and workspace lifecycle operations.
type WorkspaceManager interface {
	GetTenant(ctx context.Context, id string) (*Tenant, error)
	CreateTenant(ctx context.Context, name string, tier Tier) (*Tenant, error)
	GetWorkspace(ctx context.Context, id string) (*Workspace, error)
	CreateWorkspace(ctx context.Context, tenantID string, name string) (*Workspace, error)
	ListWorkspaces(ctx context.Context, tenantID string) ([]*Workspace, error)
	ValidateQuota(ctx context.Context, tenantID string, resourceType string, currentCount int) error
}

// MemoryWorkspaceManager is a concurrent in-memory WorkspaceManager.
type MemoryWorkspaceManager struct {
	mu         sync.RWMutex
	tenants    map[string]*Tenant
	workspaces map[string]*Workspace
}

// NewMemoryWorkspaceManager creates a new in-memory workspace manager.
func NewMemoryWorkspaceManager() *MemoryWorkspaceManager {
	return &MemoryWorkspaceManager{
		tenants:    make(map[string]*Tenant),
		workspaces: make(map[string]*Workspace),
	}
}

func (m *MemoryWorkspaceManager) CreateTenant(ctx context.Context, name string, tier Tier) (*Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	maxWorkspaces := 1
	maxAgents := 5

	switch tier {
	case TierPro:
		maxWorkspaces = 10
		maxAgents = 50
	case TierEnterprise, TierHosted:
		maxWorkspaces = 1000
		maxAgents = 10000
	}

	t := &Tenant{
		ID:            uuid.NewString(),
		Name:          name,
		Tier:          tier,
		MaxWorkspaces: maxWorkspaces,
		MaxAgents:     maxAgents,
		CreatedAt:     time.Now().UTC(),
	}
	m.tenants[t.ID] = t
	return t, nil
}

func (m *MemoryWorkspaceManager) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}
	return t, nil
}

func (m *MemoryWorkspaceManager) CreateWorkspace(ctx context.Context, tenantID string, name string) (*Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.tenants[tenantID]
	if !ok {
		return nil, ErrTenantNotFound
	}

	count := 0
	for _, w := range m.workspaces {
		if w.TenantID == tenantID {
			count++
		}
	}

	if count >= tenant.MaxWorkspaces {
		return nil, fmt.Errorf("%w: tenant %s reached max workspaces (%d)", ErrQuotaExceeded, tenantID, tenant.MaxWorkspaces)
	}

	ws := &Workspace{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	m.workspaces[ws.ID] = ws
	return ws, nil
}

func (m *MemoryWorkspaceManager) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ws, ok := m.workspaces[id]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	return ws, nil
}

func (m *MemoryWorkspaceManager) ListWorkspaces(ctx context.Context, tenantID string) ([]*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]*Workspace, 0)
	for _, ws := range m.workspaces {
		if ws.TenantID == tenantID {
			res = append(res, ws)
		}
	}
	return res, nil
}

func (m *MemoryWorkspaceManager) ValidateQuota(ctx context.Context, tenantID string, resourceType string, currentCount int) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, ok := m.tenants[tenantID]
	if !ok {
		return ErrTenantNotFound
	}

	switch resourceType {
	case "workspaces":
		if currentCount >= tenant.MaxWorkspaces {
			return fmt.Errorf("%w: workspace count %d exceeds limit %d", ErrQuotaExceeded, currentCount, tenant.MaxWorkspaces)
		}
	case "agents":
		if currentCount >= tenant.MaxAgents {
			return fmt.Errorf("%w: agent count %d exceeds limit %d", ErrQuotaExceeded, currentCount, tenant.MaxAgents)
		}
	}
	return nil
}

type tenantCtxKey struct{}
type workspaceCtxKey struct{}

// ContextWithTenant injects Tenant into the context.
func ContextWithTenant(ctx context.Context, t *Tenant) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, t)
}

// GetTenantFromContext extracts Tenant from context.
func GetTenantFromContext(ctx context.Context) (*Tenant, bool) {
	t, ok := ctx.Value(tenantCtxKey{}).(*Tenant)
	return t, ok
}

// ContextWithWorkspace injects Workspace into the context.
func ContextWithWorkspace(ctx context.Context, w *Workspace) context.Context {
	return context.WithValue(ctx, workspaceCtxKey{}, w)
}

// GetWorkspaceFromContext extracts Workspace from context.
func GetWorkspaceFromContext(ctx context.Context) (*Workspace, bool) {
	w, ok := ctx.Value(workspaceCtxKey{}).(*Workspace)
	return w, ok
}
