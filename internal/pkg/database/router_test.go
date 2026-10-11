package database_test

import (
	"context"
	"database/sql"
	"testing"

	"backend-go/internal/pkg/database"

	"github.com/stretchr/testify/assert"
)

func TestDBGroupReadPrimary(t *testing.T) {
	primary := &sql.DB{}
	replica := &sql.DB{}
	group := database.NewDBGroup(primary, replica)

	assert.Same(t, primary, group.ReadPrimary())
	assert.Same(t, primary, group.Write())
	assert.Same(t, replica, group.Read())
}

func TestDynamicTenantRouter(t *testing.T) {
	defaultDB := database.NewDBGroup(nil)
	tenantADB := database.NewDBGroup(nil)

	router := database.NewDynamicTenantRouter(defaultDB)
	router.Register("tenant-alpha", tenantADB)

	// Context with Tenant Alpha
	ctxTenantA := database.WithTenantID(context.Background(), "tenant-alpha")
	groupA, err := router.Resolve(ctxTenantA)
	assert.NoError(t, err)
	assert.Equal(t, tenantADB, groupA)

	// Context without tenant falls back to defaultDB
	groupDefault, err := router.Resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, defaultDB, groupDefault)
}
