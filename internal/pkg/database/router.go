package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type contextKey struct{}

var tenantContextKey = contextKey{}

var (
	ErrNoReplicaAvailable = errors.New("no read replica available")
	ErrTenantNotFound     = errors.New("tenant database not found")
)

// WithTenantID stores tenant identifier in context.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey, tenantID)
}

// TenantIDFromContext retrieves tenant identifier from context.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(tenantContextKey).(string)
	return id, ok
}

// DBGroup encapsulates Primary (Write) and Replica (Read) connections.
type DBGroup struct {
	Primary  *sql.DB
	Replicas []*sql.DB
	rrIndex  uint64
}

// NewDBGroup initializes a read/write split database cluster.
func NewDBGroup(primary *sql.DB, replicas ...*sql.DB) *DBGroup {
	return &DBGroup{
		Primary:  primary,
		Replicas: replicas,
	}
}

// Write returns the primary database for mutations.
func (g *DBGroup) Write() *sql.DB {
	return g.Primary
}

// ReadPrimary returns the primary database for reads that require the latest
// committed data, such as read-after-write and consistency-critical decisions.
func (g *DBGroup) ReadPrimary() *sql.DB {
	return g.Primary
}

// Read returns next Replica database via round-robin, falling back to Primary if no replicas.
func (g *DBGroup) Read() *sql.DB {
	if len(g.Replicas) == 0 {
		return g.Primary
	}
	idx := atomic.AddUint64(&g.rrIndex, 1) % uint64(len(g.Replicas))
	replica := g.Replicas[idx]
	if replica != nil {
		return replica
	}
	return g.Primary
}

// TenantResolver manages dynamic multi-tenant database routing.
type TenantResolver interface {
	Resolve(ctx context.Context) (*DBGroup, error)
	Register(tenantID string, group *DBGroup)
}

// DynamicTenantRouter routes queries to tenant-specific DBGroup based on context tenant ID.
type DynamicTenantRouter struct {
	mu        sync.RWMutex
	defaultDB *DBGroup
	tenants   map[string]*DBGroup
}

// NewDynamicTenantRouter initializes DynamicTenantRouter.
func NewDynamicTenantRouter(defaultDB *DBGroup) *DynamicTenantRouter {
	return &DynamicTenantRouter{
		defaultDB: defaultDB,
		tenants:   make(map[string]*DBGroup),
	}
}

// Register maps tenantID to its dedicated DBGroup.
func (r *DynamicTenantRouter) Register(tenantID string, group *DBGroup) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenants[tenantID] = group
}

// Resolve looks up the appropriate DBGroup for the current context.
func (r *DynamicTenantRouter) Resolve(ctx context.Context) (*DBGroup, error) {
	tenantID, ok := TenantIDFromContext(ctx)
	if !ok || tenantID == "" {
		if r.defaultDB != nil {
			return r.defaultDB, nil
		}
		return nil, ErrTenantNotFound
	}

	r.mu.RLock()
	group, exists := r.tenants[tenantID]
	r.mu.RUnlock()

	if exists {
		return group, nil
	}

	// Fallback to default if tenant-specific isolation isn't configured
	if r.defaultDB != nil {
		return r.defaultDB, nil
	}

	return nil, fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
}
