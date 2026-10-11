package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"backend-go/internal/pkg/outbox"
)

type Outcome string

const (
	OutcomeResolved    Outcome = "RESOLVED"
	OutcomeFailed      Outcome = "FAILED"
	OutcomeRetryNeeded Outcome = "RETRY_NEEDED"
	OutcomeIgnored     Outcome = "IGNORED"
)

// StaleRecord represents an unresolved/pending transaction.
type StaleRecord struct {
	ID        string            `json:"id"`
	Reference string            `json:"reference"`
	Status    string            `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Job defines the contract for a specific domain reconciliation workflow.
type Job interface {
	Name() string
	FetchStaleRecords(ctx context.Context, threshold time.Duration, limit int) ([]StaleRecord, error)
	Reconcile(ctx context.Context, record StaleRecord) (Outcome, error)
}

// Config specifies reconciliation engine parameters.
type Config struct {
	Interval  time.Duration
	Threshold time.Duration
	BatchSize int
	LockTTL   time.Duration
}

// DefaultConfig provides production defaults for reconciliation.
func DefaultConfig() Config {
	return Config{
		Interval:  5 * time.Minute,
		Threshold: 15 * time.Minute,
		BatchSize: 100,
		LockTTL:   1 * time.Minute,
	}
}

// Engine coordinates multi-job background self-healing reconciliation.
type Engine struct {
	cfg    Config
	lock   outbox.DistributedLock
	jobs   []Job
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewEngine initializes reconciliation engine.
func NewEngine(cfg Config, lock outbox.DistributedLock) *Engine {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 15 * time.Minute
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 1 * time.Minute
	}

	return &Engine{
		cfg:    cfg,
		lock:   lock,
		jobs:   make([]Job, 0),
		stopCh: make(chan struct{}),
	}
}

// RegisterJob adds a reconciliation job to the engine.
func (e *Engine) RegisterJob(job Job) {
	e.jobs = append(e.jobs, job)
}

// Start launches the reconciliation workers.
func (e *Engine) Start(ctx context.Context) {
	for _, job := range e.jobs {
		e.wg.Add(1)
		go e.runJobLoop(ctx, job)
	}
}

// Stop initiates graceful worker shutdown.
func (e *Engine) Stop() {
	close(e.stopCh)
	e.wg.Wait()
}

func (e *Engine) runJobLoop(ctx context.Context, job Job) {
	defer e.wg.Done()

	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.executeJob(ctx, job)
		}
	}
}

func (e *Engine) executeJob(ctx context.Context, job Job) {
	lockKey := fmt.Sprintf("reconciliation:lock:%s", job.Name())

	// 1. Acquire distributed lock so only one pod runs this reconciliation job
	if e.lock != nil {
		token, err := e.lock.Acquire(ctx, lockKey, e.cfg.LockTTL)
		if err != nil {
			if !errors.Is(err, outbox.ErrLockFailed) {
				slog.ErrorContext(ctx, "Failed to acquire reconciliation lock", "job", job.Name(), "error", err)
			}
			return
		}
		defer func() {
			if err := e.lock.Release(ctx, lockKey, token); err != nil {
				slog.ErrorContext(ctx, "Failed to release reconciliation lock", "job", job.Name(), "error", err)
			}
		}()
	}

	// 2. Fetch stale pending records
	records, err := job.FetchStaleRecords(ctx, e.cfg.Threshold, e.cfg.BatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to fetch stale records for reconciliation", "job", job.Name(), "error", err)
		return
	}

	if len(records) == 0 {
		return
	}

	slog.InfoContext(ctx, "Starting reconciliation batch", "job", job.Name(), "records_count", len(records))

	// 3. Process each stale record
	for _, record := range records {
		outcome, err := job.Reconcile(ctx, record)
		if err != nil {
			slog.WarnContext(ctx, "Reconciliation error on record", "job", job.Name(), "record_id", record.ID, "error", err)
		} else {
			slog.InfoContext(ctx, "Reconciled record", "job", job.Name(), "record_id", record.ID, "outcome", outcome)
		}
	}
}
