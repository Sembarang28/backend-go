package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SQLExecutor is implemented by *sql.DB and *sql.Tx.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// PostgresRepository persists outbox events and DLQ records in PostgreSQL.
// Pass a *sql.Tx to NewPostgresRepository when Save must share the business transaction.
type PostgresRepository struct {
	db SQLExecutor
}

// NewPostgresRepository creates a PostgreSQL-backed repository.
func NewPostgresRepository(db SQLExecutor) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (p *PostgresRepository) Save(ctx context.Context, event *OutboxEvent) error {
	if err := p.checkDB(); err != nil {
		return err
	}
	if event == nil || event.ID == "" {
		return errors.New("outbox event and event ID are required")
	}

	now := time.Now().UTC()
	status := event.Status
	if status == "" {
		status = StatusPending
	}
	nextRetryAt := event.NextRetryAt
	if nextRetryAt.IsZero() {
		nextRetryAt = now
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := event.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	maxRetries := event.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	payload := event.Payload
	if payload == nil {
		payload = []byte{}
	}
	headers, err := marshalHeaders(event.Headers)
	if err != nil {
		return fmt.Errorf("marshal outbox headers: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO outbox_events (
			id, aggregate_type, aggregate_id, event_type, payload, headers,
			partition, status, retry_count, max_retries, next_retry_at,
			last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			aggregate_type = EXCLUDED.aggregate_type,
			aggregate_id = EXCLUDED.aggregate_id,
			event_type = EXCLUDED.event_type,
			payload = EXCLUDED.payload,
			headers = EXCLUDED.headers,
			partition = EXCLUDED.partition,
			status = EXCLUDED.status,
			retry_count = EXCLUDED.retry_count,
			max_retries = EXCLUDED.max_retries,
			next_retry_at = EXCLUDED.next_retry_at,
			last_error = EXCLUDED.last_error,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at`,
		event.ID, event.AggregateType, event.AggregateID, event.EventType,
		payload, string(headers), event.Partition, status, event.RetryCount,
		maxRetries, nextRetryAt, nullableString(event.LastError), createdAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("save outbox event %q: %w", event.ID, err)
	}
	return nil
}

func (p *PostgresRepository) FetchPendingBatch(ctx context.Context, partition int, limit int) ([]*OutboxEvent, error) {
	if err := p.checkDB(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []*OutboxEvent{}, nil
	}

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, headers,
			partition, status, retry_count, max_retries, next_retry_at,
			last_error, created_at, updated_at
		FROM outbox_events
		WHERE partition = $1
			AND (status = $2 OR (status = $3 AND next_retry_at <= CURRENT_TIMESTAMP))
		ORDER BY created_at, id
		LIMIT $4`,
		partition, StatusPending, StatusFailed, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch pending outbox events for partition %d: %w", partition, err)
	}
	defer rows.Close()

	events := make([]*OutboxEvent, 0)
	for rows.Next() {
		event := new(OutboxEvent)
		var headers []byte
		var lastError sql.NullString
		if err := rows.Scan(
			&event.ID, &event.AggregateType, &event.AggregateID, &event.EventType,
			&event.Payload, &headers, &event.Partition, &event.Status,
			&event.RetryCount, &event.MaxRetries, &event.NextRetryAt,
			&lastError, &event.CreatedAt, &event.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending outbox event: %w", err)
		}
		if err := json.Unmarshal(headers, &event.Headers); err != nil {
			return nil, fmt.Errorf("decode headers for outbox event %q: %w", event.ID, err)
		}
		event.LastError = lastError.String
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending outbox events: %w", err)
	}
	return events, nil
}

func (p *PostgresRepository) MarkProcessing(ctx context.Context, ids []string) error {
	if err := p.checkDB(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, 0, len(ids)+1)
	args = append(args, StatusProcessing)
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("$%d", i+2)
	}

	_, err := p.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("mark outbox events processing: %w", err)
	}
	return nil
}

func (p *PostgresRepository) MarkPublished(ctx context.Context, id string) error {
	return p.updateStatus(ctx, id, StatusPublished, "", nil)
}

func (p *PostgresRepository) MarkFailed(ctx context.Context, id string, errStr string, nextRetry time.Time) error {
	if err := p.checkDB(); err != nil {
		return err
	}
	_, err := p.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = $1, retry_count = retry_count + 1, last_error = $2,
			next_retry_at = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4`, StatusFailed, errStr, nextRetry, id)
	if err != nil {
		return fmt.Errorf("mark outbox event %q failed: %w", id, err)
	}
	return nil
}

func (p *PostgresRepository) MoveToDLQ(ctx context.Context, id string, reason string) error {
	if err := p.checkDB(); err != nil {
		return err
	}

	// One statement ensures the source status change and DLQ insert succeed or fail together.
	_, err := p.db.ExecContext(ctx, `
		WITH moved AS (
			UPDATE outbox_events
			SET status = $2, last_error = $3, updated_at = CURRENT_TIMESTAMP
			WHERE id = $1
			RETURNING id, event_type, payload, headers, retry_count
		)
		INSERT INTO dlq_events (
			id, original_event_id, event_type, payload, headers,
			failure_reason, retry_attempts, failed_at
		)
		SELECT 'dlq_' || id, id, event_type, payload, headers,
			$3, retry_count, CURRENT_TIMESTAMP
		FROM moved
		ON CONFLICT (id) DO UPDATE SET
			failure_reason = EXCLUDED.failure_reason,
			retry_attempts = EXCLUDED.retry_attempts,
			failed_at = EXCLUDED.failed_at`,
		id, StatusDLQ, reason,
	)
	if err != nil {
		return fmt.Errorf("move outbox event %q to DLQ: %w", id, err)
	}
	return nil
}

func (p *PostgresRepository) SaveDLQ(ctx context.Context, dlq *DLQEvent) error {
	if err := p.checkDB(); err != nil {
		return err
	}
	if dlq == nil || dlq.ID == "" {
		return errors.New("DLQ event and event ID are required")
	}
	payload := dlq.Payload
	if payload == nil {
		payload = []byte{}
	}
	headers, err := marshalHeaders(dlq.Headers)
	if err != nil {
		return fmt.Errorf("marshal DLQ headers: %w", err)
	}
	failedAt := dlq.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now().UTC()
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO dlq_events (
			id, original_event_id, event_type, payload, headers,
			failure_reason, retry_attempts, failed_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			original_event_id = EXCLUDED.original_event_id,
			event_type = EXCLUDED.event_type,
			payload = EXCLUDED.payload,
			headers = EXCLUDED.headers,
			failure_reason = EXCLUDED.failure_reason,
			retry_attempts = EXCLUDED.retry_attempts,
			failed_at = EXCLUDED.failed_at`,
		dlq.ID, dlq.OriginalEventID, dlq.EventType, payload, string(headers),
		dlq.FailureReason, dlq.RetryAttempts, failedAt,
	)
	if err != nil {
		return fmt.Errorf("save DLQ event %q: %w", dlq.ID, err)
	}
	return nil
}

func (p *PostgresRepository) DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if err := p.checkDB(); err != nil {
		return 0, err
	}

	query := `
		WITH expired AS (
			SELECT id FROM outbox_events
			WHERE status = $1 AND updated_at < $2
			ORDER BY updated_at, id`
	args := []any{StatusPublished, before}
	if limit > 0 {
		query += ` LIMIT $3`
		args = append(args, limit)
	}
	query += `
		)
		DELETE FROM outbox_events AS events
		USING expired
		WHERE events.id = expired.id`

	result, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete published outbox events before %s: %w", before, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted published outbox events: %w", err)
	}
	return deleted, nil
}

func (p *PostgresRepository) updateStatus(ctx context.Context, id string, status EventStatus, lastError string, nextRetry *time.Time) error {
	if err := p.checkDB(); err != nil {
		return err
	}
	var err error
	if nextRetry == nil {
		_, err = p.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = $1, updated_at = CURRENT_TIMESTAMP
			WHERE id = $2`, status, id)
	} else {
		_, err = p.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = $1, last_error = $2, next_retry_at = $3,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $4`, status, nullableString(lastError), *nextRetry, id)
	}
	if err != nil {
		return fmt.Errorf("update outbox event %q status to %s: %w", id, status, err)
	}
	return nil
}

func (p *PostgresRepository) checkDB() error {
	if p == nil || p.db == nil {
		return errors.New("outbox PostgreSQL repository has no database executor")
	}
	return nil
}

func marshalHeaders(headers map[string]string) ([]byte, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	return json.Marshal(headers)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ Repository = (*PostgresRepository)(nil)
