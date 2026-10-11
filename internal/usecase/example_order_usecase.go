//go:build examples

package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"backend-go/internal/domain"
)

type orderUsecase struct {
	repo domain.ExampleOrderRepository
}

func NewExampleOrderUsecase(repo domain.ExampleOrderRepository) domain.ExampleOrderUsecase {
	return &orderUsecase{repo: repo}
}

func (u *orderUsecase) CreateOrder(ctx context.Context, customerID string, amountMinor int64, currency, idempotencyKey string) (*domain.ExampleOrder, error) {
	customerID = strings.TrimSpace(customerID)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if customerID == "" || amountMinor <= 0 || len(currency) != 3 || idempotencyKey == "" {
		return nil, domain.ErrInvalidExampleOrder
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			return nil, domain.ErrInvalidExampleOrder
		}
	}

	id, err := newOrderID()
	if err != nil {
		return nil, fmt.Errorf("generate order ID: %w", err)
	}
	order := &domain.ExampleOrder{
		ID:          id,
		CustomerID:  customerID,
		AmountMinor: amountMinor,
		Currency:    currency,
		Status:      "PAYMENT_PENDING",
		CreatedAt:   time.Now().UTC(),
	}
	payload, err := json.Marshal(struct {
		EventType string              `json:"event_type"`
		Order     domain.ExampleOrder `json:"order"`
	}{EventType: "order.created", Order: *order})
	if err != nil {
		return nil, fmt.Errorf("marshal order-created event: %w", err)
	}
	fingerprintPayload, err := json.Marshal(struct {
		CustomerID  string `json:"customer_id"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}{CustomerID: customerID, AmountMinor: amountMinor, Currency: currency})
	if err != nil {
		return nil, fmt.Errorf("marshal order request fingerprint: %w", err)
	}
	fingerprintBytes := sha256.Sum256(fingerprintPayload)
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	if err := u.repo.Create(ctx, order, payload, idempotencyKey, fingerprint); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return order, nil
}

func (u *orderUsecase) GetOrder(ctx context.Context, id string) (*domain.ExampleOrder, error) {
	if strings.TrimSpace(id) == "" {
		return nil, domain.ErrInvalidExampleOrder
	}
	return u.repo.GetByID(ctx, id)
}

func (u *orderUsecase) GetOrderForCriticalDecision(ctx context.Context, id string) (*domain.ExampleOrder, error) {
	if strings.TrimSpace(id) == "" {
		return nil, domain.ErrInvalidExampleOrder
	}
	return u.repo.GetByIDCritical(ctx, id)
}

func newOrderID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}
