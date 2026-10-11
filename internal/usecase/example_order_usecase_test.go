//go:build examples

package usecase_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/usecase"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderRepositoryStub struct {
	createdOrder       *domain.ExampleOrder
	eventPayload       []byte
	idempotencyKey     string
	requestFingerprint string
}

func (r *orderRepositoryStub) Create(_ context.Context, order *domain.ExampleOrder, payload []byte, key, fingerprint string) error {
	r.createdOrder = order
	r.eventPayload = payload
	r.idempotencyKey = key
	r.requestFingerprint = fingerprint
	return nil
}

func (*orderRepositoryStub) GetByID(context.Context, string) (*domain.ExampleOrder, error) {
	return nil, nil
}

func (*orderRepositoryStub) GetByIDCritical(context.Context, string) (*domain.ExampleOrder, error) {
	return nil, nil
}

func (*orderRepositoryStub) FindOrdersWithoutOutbox(context.Context, time.Time, int) ([]domain.ExampleOrder, error) {
	return nil, nil
}

func (*orderRepositoryStub) EnsureOrderCreatedEvent(context.Context, string) error {
	return nil
}

func TestCreateOrderBuildsPendingPaymentEventAndStableFingerprint(t *testing.T) {
	repo := &orderRepositoryStub{}
	uc := usecase.NewExampleOrderUsecase(repo)

	order, err := uc.CreateOrder(context.Background(), " customer-1 ", 1250, "usd", "request-42")

	require.NoError(t, err)
	assert.Len(t, order.ID, 32)
	assert.Equal(t, "customer-1", order.CustomerID)
	assert.Equal(t, int64(1250), order.AmountMinor)
	assert.Equal(t, "USD", order.Currency)
	assert.Equal(t, "PAYMENT_PENDING", order.Status)
	assert.Equal(t, "request-42", repo.idempotencyKey)
	assert.Len(t, repo.requestFingerprint, 64)

	var event struct {
		EventType string `json:"event_type"`
		Order     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"order"`
	}
	require.NoError(t, json.Unmarshal(repo.eventPayload, &event))
	assert.Equal(t, "order.created", event.EventType)
	assert.Equal(t, order.ID, event.Order.ID)
	assert.Equal(t, order.Status, event.Order.Status)
}

func TestCreateOrderValidatesFieldsBeforePersistence(t *testing.T) {
	repo := &orderRepositoryStub{}
	uc := usecase.NewExampleOrderUsecase(repo)

	_, err := uc.CreateOrder(context.Background(), "customer", 0, "USD", "key")

	assert.ErrorIs(t, err, domain.ErrInvalidExampleOrder)
	assert.Nil(t, repo.createdOrder)
}
