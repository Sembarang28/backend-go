package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"backend-go/internal/pkg/telemetry"
)

// MessagePublisher publishes raw message payloads with headers to broker (e.g. NATS, Kafka).
type MessagePublisher interface {
	Publish(ctx context.Context, topic string, payload []byte, headers map[string]string) error
}

// WorkerConfig holds tuning options for outbox processor.
type WorkerConfig struct {
	PartitionCount int
	BatchSize      int
	PollInterval   time.Duration
	LockTTL        time.Duration
	MaxRetries     int
	BaseBackoff    time.Duration
}

// DefaultConfig provides sensible production defaults.
func DefaultConfig() WorkerConfig {
	return WorkerConfig{
		PartitionCount: 8,
		BatchSize:      100,
		PollInterval:   200 * time.Millisecond,
		LockTTL:        5 * time.Second,
		MaxRetries:     5,
		BaseBackoff:    1 * time.Second,
	}
}

// Worker coordinates distributed multi-partition outbox processing.
type Worker struct {
	cfg       WorkerConfig
	repo      Repository
	lock      DistributedLock
	publisher MessagePublisher
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewWorker initializes a partitioned Outbox Worker.
func NewWorker(cfg WorkerConfig, repo Repository, lock DistributedLock, publisher MessagePublisher) *Worker {
	if cfg.PartitionCount <= 0 {
		cfg.PartitionCount = 4
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 5 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}

	return &Worker{
		cfg:       cfg,
		repo:      repo,
		lock:      lock,
		publisher: publisher,
		stopCh:    make(chan struct{}),
	}
}

// Start launches partition processing goroutines.
func (w *Worker) Start(ctx context.Context) {
	for p := 0; p < w.cfg.PartitionCount; p++ {
		w.wg.Add(1)
		go w.runPartitionLoop(ctx, p)
	}
}

// Stop initiates graceful worker termination.
func (w *Worker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

func (w *Worker) runPartitionLoop(ctx context.Context, partition int) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	lockKey := fmt.Sprintf("outbox:lock:partition:%d", partition)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.processPartition(ctx, partition, lockKey)
		}
	}
}

func (w *Worker) processPartition(ctx context.Context, partition int, lockKey string) {
	// 1. Acquire Distributed Lock for partition
	if w.lock == nil {
		slog.ErrorContext(ctx, "Outbox partition lock is unavailable", "partition", partition)
		return
	}
	token, err := w.lock.Acquire(ctx, lockKey, w.cfg.LockTTL)
	if err != nil {
		if !errors.Is(err, ErrLockFailed) {
			slog.ErrorContext(ctx, "Failed to acquire outbox partition lock", "partition", partition, "error", err)
		}
		return
	}
	defer func() {
		if err := w.lock.Release(ctx, lockKey, token); err != nil {
			slog.ErrorContext(ctx, "Failed to release outbox partition lock", "partition", partition, "error", err)
		}
	}()

	// 2. Fetch pending batch
	events, err := w.repo.FetchPendingBatch(ctx, partition, w.cfg.BatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to fetch pending outbox events", "partition", partition, "error", err)
		return
	}
	if len(events) == 0 {
		return
	}

	ids := make([]string, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	if err := w.repo.MarkProcessing(ctx, ids); err != nil {
		slog.ErrorContext(ctx, "Failed to mark outbox events processing", "partition", partition, "error", err)
		return
	}

	// 3. Process events
	for _, ev := range events {
		w.processEvent(ctx, ev)
	}
}

func (w *Worker) processEvent(ctx context.Context, ev *OutboxEvent) {
	// Reconstruct span context from event headers for end-to-end tracing
	eventCtx := ctx
	if len(ev.Headers) > 0 {
		sc := telemetry.ExtractMap(ev.Headers)
		eventCtx = telemetry.ContextWithSpan(ctx, sc)
	}

	// Publish to Message Broker
	var err error
	if w.publisher == nil {
		err = errors.New("outbox message publisher is unavailable")
	} else {
		err = w.publisher.Publish(eventCtx, ev.EventType, ev.Payload, ev.Headers)
	}
	if err == nil {
		if markErr := w.repo.MarkPublished(ctx, ev.ID); markErr != nil {
			slog.ErrorContext(eventCtx, "Event published but failed to mark outbox event published", "event_id", ev.ID, "error", markErr)
			return
		}
		slog.InfoContext(eventCtx, "Outbox event published", "event_id", ev.ID, "type", ev.EventType)
		return
	}

	// Failure handling with exponential backoff & DLQ routing
	retryCount := ev.RetryCount + 1
	if retryCount >= w.cfg.MaxRetries {
		slog.ErrorContext(eventCtx, "Outbox event exceeded max retries, moving to DLQ", "event_id", ev.ID, "retries", retryCount, "error", err)
		if dlqErr := w.repo.MoveToDLQ(ctx, ev.ID, fmt.Sprintf("Exceeded %d retries: %v", w.cfg.MaxRetries, err)); dlqErr != nil {
			slog.ErrorContext(eventCtx, "Failed to move outbox event to DLQ", "event_id", ev.ID, "error", dlqErr)
		}
		return
	}

	backoff := w.cfg.BaseBackoff * time.Duration(1<<retryCount)
	nextRetry := time.Now().Add(backoff)
	if markErr := w.repo.MarkFailed(ctx, ev.ID, err.Error(), nextRetry); markErr != nil {
		slog.ErrorContext(eventCtx, "Failed to schedule outbox event retry", "event_id", ev.ID, "error", markErr)
		return
	}
	slog.WarnContext(eventCtx, "Outbox event failed, scheduled for retry", "event_id", ev.ID, "attempt", retryCount, "next_retry", nextRetry, "error", err)
}
