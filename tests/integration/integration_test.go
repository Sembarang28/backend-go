//go:build integration
// +build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/cache"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/database/migration"
	"backend-go/internal/pkg/outbox"
	"backend-go/internal/repository"
	"backend-go/internal/usecase"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type mockIntegrationPublisher struct {
	publishedEvents []domain.UserAccessedEvent
}

func (m *mockIntegrationPublisher) PublishUserAccessed(ctx context.Context, event domain.UserAccessedEvent) error {
	m.publishedEvents = append(m.publishedEvents, event)
	return nil
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
	cachedUser, err := cache.GetOrSet(ctx, cacheMgr, "user:1", 1*time.Minute, func(c context.Context) (*domain.User, error) {
		return &domain.User{ID: 1, Name: "Integration User", Email: "integration@example.com"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Integration User", cachedUser.Name)

	// 5. Test Usecase with Real Redis-backed Repository
	userRepo := repository.NewUserRepository(redisClient, 5*time.Minute)
	pub := &mockIntegrationPublisher{}
	uc := usecase.NewUserUsecase(userRepo, pub)

	user, err := uc.GetUser(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, user.ID)

	// 6. Test Outbox Worker with Real Redis Distributed Lock
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
