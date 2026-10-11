//go:build examples

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/cache"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/outbox"

	"github.com/redis/go-redis/v9"
)

type orderRepository struct {
	dbGroup          *database.DBGroup
	cacheManager     *cache.Manager
	cacheTTL         time.Duration
	outboxPartitions int
}

func NewExampleOrderRepository(
	dbGroup *database.DBGroup,
	redisClient redis.UniversalClient,
	cacheTTL time.Duration,
	outboxPartitions int,
) domain.ExampleOrderRepository {
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	if outboxPartitions <= 0 {
		outboxPartitions = 1
	}
	return &orderRepository{
		dbGroup:          dbGroup,
		cacheManager:     cache.NewManager(redisClient),
		cacheTTL:         cacheTTL,
		outboxPartitions: outboxPartitions,
	}
}

func (r *orderRepository) Create(ctx context.Context, order *domain.ExampleOrder, eventPayload []byte, idempotencyKey, requestFingerprint string) error {
	if r.dbGroup == nil || r.dbGroup.Write() == nil {
		return errors.New("order repository requires PostgreSQL primary")
	}
	if order == nil {
		return domain.ErrInvalidExampleOrder
	}

	tx, err := r.dbGroup.Write().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create order transaction: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
		INSERT INTO example_orders (
			id, customer_id, amount_minor, currency, status, outbox_enqueued,
			idempotency_key, request_fingerprint, created_at
		)
		VALUES ($1, $2, $3, $4, $5, FALSE, $6, $7, $8)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING created_at`,
		order.ID, order.CustomerID, order.AmountMinor, order.Currency, order.Status,
		idempotencyKey, requestFingerprint, order.CreatedAt,
	).Scan(&order.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		var existing domain.ExampleOrder
		var existingFingerprint string
		err := r.dbGroup.Write().QueryRowContext(ctx, `
			SELECT id, customer_id, amount_minor, currency, status, outbox_enqueued, created_at, request_fingerprint
			FROM example_orders WHERE idempotency_key = $1`, idempotencyKey,
		).Scan(
			&existing.ID, &existing.CustomerID, &existing.AmountMinor, &existing.Currency,
			&existing.Status, &existing.OutboxEnqueued, &existing.CreatedAt, &existingFingerprint,
		)
		if err != nil {
			return fmt.Errorf("load existing order for idempotency key: %w", err)
		}
		if existingFingerprint != requestFingerprint {
			return domain.ErrExampleIdempotencyUsed
		}
		*order = existing
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert order %q: %w", order.ID, err)
	}

	eventID := "order-created-" + order.ID
	event := &outbox.OutboxEvent{
		ID:            eventID,
		AggregateType: "order",
		AggregateID:   order.ID,
		EventType:     "order.created",
		Payload:       eventPayload,
		Headers:       map[string]string{},
		Partition:     int(crc32.ChecksumIEEE([]byte(order.ID)) % uint32(r.outboxPartitions)),
	}
	if err := outbox.NewPostgresRepository(tx).Save(ctx, event); err != nil {
		return fmt.Errorf("save order-created outbox event: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE example_orders SET outbox_enqueued = TRUE WHERE id = $1`, order.ID); err != nil {
		return fmt.Errorf("mark order %q outbox event enqueued: %w", order.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create order transaction: %w", err)
	}
	order.OutboxEnqueued = true
	return nil
}

func (r *orderRepository) GetByID(ctx context.Context, id string) (*domain.ExampleOrder, error) {
	key := "order:" + id
	return cache.GetOrSet(ctx, r.cacheManager, key, r.cacheTTL, func(ctx context.Context) (*domain.ExampleOrder, error) {
		return r.getFromDB(ctx, r.dbGroup.Read(), id)
	})
}

func (r *orderRepository) GetByIDCritical(ctx context.Context, id string) (*domain.ExampleOrder, error) {
	return cache.LoadWithoutCache(ctx, func(ctx context.Context) (*domain.ExampleOrder, error) {
		if r.dbGroup == nil {
			return nil, errors.New("order repository requires PostgreSQL")
		}
		return r.getFromDB(ctx, r.dbGroup.ReadPrimary(), id)
	})
}

func (r *orderRepository) getFromDB(ctx context.Context, db *sql.DB, id string) (*domain.ExampleOrder, error) {
	if db == nil {
		return nil, errors.New("order repository requires PostgreSQL")
	}
	order := new(domain.ExampleOrder)
	err := db.QueryRowContext(ctx, `
		SELECT id, customer_id, amount_minor, currency, status, outbox_enqueued, created_at
		FROM example_orders WHERE id = $1`, id,
	).Scan(
		&order.ID, &order.CustomerID, &order.AmountMinor, &order.Currency,
		&order.Status, &order.OutboxEnqueued, &order.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrExampleOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order %q: %w", id, err)
	}
	return order, nil
}

func (r *orderRepository) FindOrdersWithoutOutbox(ctx context.Context, cutoff time.Time, limit int) ([]domain.ExampleOrder, error) {
	if r.dbGroup == nil {
		return nil, errors.New("order repository requires PostgreSQL")
	}
	if limit <= 0 {
		return []domain.ExampleOrder{}, nil
	}

	rows, err := r.dbGroup.Write().QueryContext(ctx, `
		SELECT id, customer_id, amount_minor, currency, status, outbox_enqueued, created_at
		FROM example_orders
		WHERE outbox_enqueued = FALSE AND created_at < $1
		ORDER BY created_at, id
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("find orders missing outbox events: %w", err)
	}
	defer rows.Close()

	var orders []domain.ExampleOrder
	for rows.Next() {
		var order domain.ExampleOrder
		if err := rows.Scan(
			&order.ID, &order.CustomerID, &order.AmountMinor, &order.Currency,
			&order.Status, &order.OutboxEnqueued, &order.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order needing reconciliation: %w", err)
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders needing reconciliation: %w", err)
	}
	return orders, nil
}

func (r *orderRepository) EnsureOrderCreatedEvent(ctx context.Context, orderID string) error {
	if r.dbGroup == nil {
		return errors.New("order repository requires PostgreSQL")
	}
	tx, err := r.dbGroup.Write().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reconcile order transaction: %w", err)
	}
	defer tx.Rollback()

	var order domain.ExampleOrder
	err = tx.QueryRowContext(ctx, `
		SELECT id, customer_id, amount_minor, currency, status, outbox_enqueued, created_at
		FROM example_orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(
		&order.ID, &order.CustomerID, &order.AmountMinor, &order.Currency,
		&order.Status, &order.OutboxEnqueued, &order.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrExampleOrderNotFound
	}
	if err != nil {
		return fmt.Errorf("lock order %q for reconciliation: %w", orderID, err)
	}
	if order.OutboxEnqueued {
		return nil
	}

	payload, err := json.Marshal(struct {
		EventType string              `json:"event_type"`
		Order     domain.ExampleOrder `json:"order"`
	}{EventType: "order.created", Order: order})
	if err != nil {
		return fmt.Errorf("marshal reconciled order event: %w", err)
	}
	event := &outbox.OutboxEvent{
		ID:            "order-created-" + order.ID,
		AggregateType: "order",
		AggregateID:   order.ID,
		EventType:     "order.created",
		Payload:       payload,
		Headers:       map[string]string{},
		Partition:     int(crc32.ChecksumIEEE([]byte(order.ID)) % uint32(r.outboxPartitions)),
	}
	if err := outbox.NewPostgresRepository(tx).Save(ctx, event); err != nil {
		return fmt.Errorf("restore order-created outbox event: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE example_orders SET outbox_enqueued = TRUE WHERE id = $1`, order.ID); err != nil {
		return fmt.Errorf("mark reconciled order %q enqueued: %w", order.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reconcile order transaction: %w", err)
	}
	return nil
}
