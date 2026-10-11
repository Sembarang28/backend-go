package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// SQLExecutor is implemented by *sql.DB and *sql.Tx.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PostgresAuditor persists audit events in PostgreSQL.
type PostgresAuditor struct {
	db SQLExecutor
}

// NewPostgresAuditor creates an auditor backed by PostgreSQL.
// Pass a *sql.Tx to persist an audit event in the same transaction as a business change.
func NewPostgresAuditor(db SQLExecutor) *PostgresAuditor {
	return &PostgresAuditor{db: db}
}

// Log appends an audit event to the audit_events table.
func (a *PostgresAuditor) Log(ctx context.Context, event AuditEvent) error {
	if a == nil || a.db == nil {
		return errors.New("PostgreSQL auditor has no database executor")
	}

	event = enrichEvent(ctx, event)
	before, err := json.Marshal(event.PayloadBefore)
	if err != nil {
		return fmt.Errorf("marshal audit payload before: %w", err)
	}
	after, err := json.Marshal(event.PayloadAfter)
	if err != nil {
		return fmt.Errorf("marshal audit payload after: %w", err)
	}

	_, err = a.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			id, actor_id, action, target_resource, resource_id,
			payload_before, payload_after, ip_address, user_agent,
			trace_id, span_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12)`,
		event.ID, event.ActorID, event.Action, event.TargetResource, event.ResourceID,
		string(before), string(after), event.IPAddress, event.UserAgent,
		event.TraceID, event.SpanID, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("persist audit event %q: %w", event.ID, err)
	}
	return nil
}
