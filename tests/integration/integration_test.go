//go:build integration && !examples
// +build integration,!examples

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"backend-go/internal/pkg/audit"
	"backend-go/internal/pkg/cache"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/database/migration"
	"backend-go/internal/pkg/outbox"
	"backend-go/internal/pkg/telemetry"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type integrationUser struct {
	ID    int
	Name  string
	Email string
}

func TestEndToEndWithTestcontainers(t *testing.T) {
	ctx := context.Background()

	// 1. Start PostgreSQL Container
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	defer func() { _ = pgContainer.Terminate(ctx) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Connect to PostgreSQL
	db, err := sql.Open("pgx", connStr)
	require.NoError(t, err)
	defer db.Close()

	database.ConfigurePool(db, database.DefaultPoolConfig())

	// 2. Run Database Migrations
	migrator := migration.NewEngine()
	err = migrator.Run(ctx, db)
	require.NoError(t, err)
	var exampleTableExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'example_orders'
		)`).Scan(&exampleTableExists))
	assert.False(t, exampleTableExists, "default migrations must omit optional example schemas")

	// 3. Start Redis Container
	redisContainer, err := tcredis.Run(ctx,
		"redis:7-alpine",
	)
	require.NoError(t, err)
	defer func() { _ = redisContainer.Terminate(ctx) }()

	redisURI, err := redisContainer.ConnectionString(ctx)
	require.NoError(t, err)

	redisOpts, err := redis.ParseURL(redisURI)
	require.NoError(t, err)

	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	// 4. Test Singleflight Cache with Real Redis
	cacheMgr := cache.NewManager(redisClient)
	cachedUser, err := cache.GetOrSet(ctx, cacheMgr, "example:user:1", 1*time.Minute, func(c context.Context) (*integrationUser, error) {
		return &integrationUser{ID: 1, Name: "Integration User", Email: "integration@example.com"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Integration User", cachedUser.Name)

	// 5. Test Outbox Worker with Real Redis Distributed Lock
	outboxLock := outbox.NewRedisDistributedLock(redisClient)

	// Acquire and release distributed lock
	token, err := outboxLock.Acquire(ctx, "outbox:lock:partition:0", 5*time.Second)
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	err = outboxLock.Release(ctx, "outbox:lock:partition:0", token)
	assert.NoError(t, err)
}

func TestPostgresOutboxRepository(t *testing.T) {
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("outbox_test"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, migration.NewEngine().Run(ctx, db))

	repo := outbox.NewPostgresRepository(db)
	event := &outbox.OutboxEvent{
		ID:            "event-pending",
		AggregateType: "Order",
		AggregateID:   "42",
		EventType:     "order.created",
		Payload:       []byte(`{"id":42}`),
		Headers:       map[string]string{"traceparent": "00-test"},
		Partition:     2,
	}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, outbox.NewPostgresRepository(tx).Save(ctx, event))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE id = $1", event.ID).Scan(&count))
	assert.Zero(t, count, "transaction-bound Save must not become visible before commit")
	require.NoError(t, tx.Rollback())

	require.NoError(t, repo.Save(ctx, event))
	pending, err := repo.FetchPendingBatch(ctx, 2, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, event.ID, pending[0].ID)
	assert.Equal(t, event.Headers, pending[0].Headers)
	require.NoError(t, repo.MarkProcessing(ctx, []string{event.ID}))
	require.NoError(t, repo.MarkFailed(ctx, event.ID, "temporary failure", time.Now().Add(-time.Second)))
	pending, err = repo.FetchPendingBatch(ctx, 2, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, 1, pending[0].RetryCount)
	require.NoError(t, repo.MarkPublished(ctx, event.ID))

	deleted, err := repo.DeletePublishedBefore(ctx, time.Now().Add(time.Second), 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	dlqEvent := &outbox.OutboxEvent{
		ID:         "event-dlq",
		EventType:  "order.failed",
		Payload:    []byte(`{"id":99}`),
		Headers:    map[string]string{"source": "test"},
		RetryCount: 3,
	}
	require.NoError(t, repo.Save(ctx, dlqEvent))
	require.NoError(t, repo.MoveToDLQ(ctx, dlqEvent.ID, "permanent failure"))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT status FROM outbox_events WHERE id = $1", dlqEvent.ID).Scan(&dlqEvent.Status))
	assert.Equal(t, outbox.StatusDLQ, dlqEvent.Status)
	var dlqCount int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dlq_events WHERE original_event_id = $1", dlqEvent.ID).Scan(&dlqCount))
	assert.Equal(t, 1, dlqCount)

	require.NoError(t, repo.SaveDLQ(ctx, &outbox.DLQEvent{
		ID:              "manual-dlq",
		OriginalEventID: "manual-event",
		EventType:       "manual.failed",
		Payload:         []byte(`{}`),
		FailureReason:   "manual",
	}))
}

func TestPostgresPrimaryReplicaGroup(t *testing.T) {
	ctx := context.Background()
	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("routing_test"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	group, err := database.OpenPostgresGroup(ctx, dsn, []string{dsn, dsn}, database.DefaultPoolConfig())
	require.NoError(t, err)
	t.Cleanup(func() { _ = group.Close() })

	assert.Same(t, group.Primary, group.Write())
	assert.Same(t, group.Replicas[1], group.Read())
	assert.Same(t, group.Replicas[0], group.Read())
	require.NoError(t, group.Write().PingContext(ctx))
	for _, replica := range group.Replicas {
		require.NoError(t, replica.PingContext(ctx))
	}
}

func TestPostgresAuditor(t *testing.T) {
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("audit_test"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, migration.NewEngine().Run(ctx, db))
	_, err = db.ExecContext(ctx, "CREATE TABLE audit_test_business (id INT PRIMARY KEY)")
	require.NoError(t, err)

	event := audit.AuditEvent{
		ActorID:        "user-101",
		Action:         "user.update",
		TargetResource: "User",
		ResourceID:     "101",
		PayloadBefore:  map[string]string{"role": "member"},
		PayloadAfter:   map[string]string{"role": "admin"},
		IPAddress:      "192.0.2.1",
		UserAgent:      "integration-test",
	}

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_test_business (id) VALUES (1)")
	require.NoError(t, err)
	require.NoError(t, audit.NewPostgresAuditor(tx).Log(ctx, event))
	require.NoError(t, tx.Rollback())

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_test_business").Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE action = $1", event.Action).Scan(&count))
	assert.Zero(t, count, "audit insert must roll back with the business transaction")

	sc := telemetry.NewSpanContext()
	ctx = telemetry.ContextWithSpan(ctx, sc)
	require.NoError(t, audit.NewPostgresAuditor(db).Log(ctx, event))

	var (
		id, traceID, spanID, userAgent string
		before, after                  []byte
	)
	err = db.QueryRowContext(ctx, `
		SELECT id, trace_id, span_id, user_agent, payload_before, payload_after
		FROM audit_events WHERE action = $1`, event.Action).Scan(
		&id, &traceID, &spanID, &userAgent, &before, &after,
	)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	assert.Equal(t, sc.TraceID, traceID)
	assert.Equal(t, sc.SpanID, spanID)
	assert.Equal(t, event.UserAgent, userAgent)
	assert.JSONEq(t, `{"role":"member"}`, string(before))
	assert.JSONEq(t, `{"role":"admin"}`, string(after))
}
