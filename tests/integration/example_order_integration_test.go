//go:build integration && examples

package integration_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/database/migration"
	"backend-go/internal/pkg/reconciliation"
	"backend-go/internal/repository"
	"backend-go/internal/usecase"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestExampleOrderWorkflowTransactionalOutboxAndReconciliation(t *testing.T) {
	ctx := context.Background()
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("orders_test"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, migration.NewEngine().Run(ctx, db))

	dbGroup := database.NewDBGroup(db)
	orderRepo := repository.NewExampleOrderRepository(dbGroup, nil, time.Minute, 8)
	orderUsecase := usecase.NewExampleOrderUsecase(orderRepo)

	order, err := orderUsecase.CreateOrder(ctx, "customer-1", 2500, "usd", "scoped-key-1")
	require.NoError(t, err)
	require.True(t, order.OutboxEnqueued)

	replayedOrder, err := orderUsecase.CreateOrder(ctx, "customer-1", 2500, "USD", "scoped-key-1")
	require.NoError(t, err)
	assert.Equal(t, order.ID, replayedOrder.ID, "database idempotency must survive Redis key expiry or loss")

	_, err = orderUsecase.CreateOrder(ctx, "customer-1", 3000, "USD", "scoped-key-1")
	require.ErrorIs(t, err, domain.ErrExampleIdempotencyUsed)

	var orderCount, eventCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM example_orders WHERE idempotency_key = $1`, "scoped-key-1").Scan(&orderCount))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, order.ID, "order.created").Scan(&eventCount))
	assert.Equal(t, 1, orderCount)
	assert.Equal(t, 1, eventCount)

	staleID := "legacy-order-without-event"
	_, err = db.ExecContext(ctx, `
		INSERT INTO example_orders (
			id, customer_id, amount_minor, currency, status, outbox_enqueued,
			idempotency_key, request_fingerprint, created_at
		) VALUES ($1, $2, $3, $4, $5, FALSE, $6, $7, $8)`,
		staleID, "customer-legacy", 500, "USD", "PAYMENT_PENDING",
		"legacy-idempotency-key", strings.Repeat("a", 64), time.Now().Add(-time.Hour))
	require.NoError(t, err)

	job := usecase.NewExampleOrderOutboxReconciliationJob(orderRepo)
	stale, err := job.FetchStaleRecords(ctx, 15*time.Minute, 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, staleID, stale[0].ID)
	outcome, err := job.Reconcile(ctx, stale[0])
	require.NoError(t, err)
	assert.Equal(t, reconciliation.OutcomeResolved, outcome)

	var enqueued bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT outbox_enqueued FROM example_orders WHERE id = $1`, staleID).Scan(&enqueued))
	assert.True(t, enqueued)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE id = $1`, "order-created-"+staleID).Scan(&eventCount))
	assert.Equal(t, 1, eventCount)
}
