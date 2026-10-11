package outbox

import (
	"context"
	"time"
)

// Repository defines storage operations for outbox events.
type Repository interface {
	Save(ctx context.Context, event *OutboxEvent) error
	FetchPendingBatch(ctx context.Context, partition int, limit int) ([]*OutboxEvent, error)
	MarkProcessing(ctx context.Context, ids []string) error
	MarkPublished(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string, errStr string, nextRetry time.Time) error
	MoveToDLQ(ctx context.Context, id string, reason string) error
	SaveDLQ(ctx context.Context, dlq *DLQEvent) error
	DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}
