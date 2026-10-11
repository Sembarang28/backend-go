//go:build examples

package domain

import (
	"context"
	"time"
)

type ExampleOrder struct {
	ID             string    `json:"id"`
	CustomerID     string    `json:"customer_id"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	OutboxEnqueued bool      `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type ExampleOrderRepository interface {
	Create(ctx context.Context, order *ExampleOrder, eventPayload []byte, idempotencyKey, requestFingerprint string) error
	GetByID(ctx context.Context, id string) (*ExampleOrder, error)
	GetByIDCritical(ctx context.Context, id string) (*ExampleOrder, error)
	FindOrdersWithoutOutbox(ctx context.Context, cutoff time.Time, limit int) ([]ExampleOrder, error)
	EnsureOrderCreatedEvent(ctx context.Context, orderID string) error
}

type ExampleOrderUsecase interface {
	CreateOrder(ctx context.Context, customerID string, amountMinor int64, currency, idempotencyKey string) (*ExampleOrder, error)
	GetOrder(ctx context.Context, id string) (*ExampleOrder, error)
	GetOrderForCriticalDecision(ctx context.Context, id string) (*ExampleOrder, error)
}
