//go:build examples

package usecase

import (
	"context"
	"fmt"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/reconciliation"
)

type orderOutboxReconciliationJob struct {
	repo domain.ExampleOrderRepository
}

func NewExampleOrderOutboxReconciliationJob(repo domain.ExampleOrderRepository) reconciliation.Job {
	return &orderOutboxReconciliationJob{repo: repo}
}

func (j *orderOutboxReconciliationJob) Name() string {
	return "orders-missing-outbox-event"
}

func (j *orderOutboxReconciliationJob) FetchStaleRecords(ctx context.Context, threshold time.Duration, limit int) ([]reconciliation.StaleRecord, error) {
	orders, err := j.repo.FindOrdersWithoutOutbox(ctx, time.Now().Add(-threshold), limit)
	if err != nil {
		return nil, err
	}
	records := make([]reconciliation.StaleRecord, 0, len(orders))
	for _, order := range orders {
		records = append(records, reconciliation.StaleRecord{
			ID:        order.ID,
			Reference: order.ID,
			Status:    order.Status,
			CreatedAt: order.CreatedAt,
		})
	}
	return records, nil
}

func (j *orderOutboxReconciliationJob) Reconcile(ctx context.Context, record reconciliation.StaleRecord) (reconciliation.Outcome, error) {
	if record.ID == "" {
		return reconciliation.OutcomeFailed, fmt.Errorf("order ID is required for reconciliation")
	}
	if err := j.repo.EnsureOrderCreatedEvent(ctx, record.ID); err != nil {
		return reconciliation.OutcomeRetryNeeded, fmt.Errorf("restore outbox event for order %q: %w", record.ID, err)
	}
	return reconciliation.OutcomeResolved, nil
}
