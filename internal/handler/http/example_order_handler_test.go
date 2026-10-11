//go:build examples

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend-go/internal/domain"
	handlerhttp "backend-go/internal/handler/http"
	"backend-go/internal/pkg/idempotency"
	"backend-go/internal/pkg/resilience"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderUsecaseStub struct {
	createCalls   int
	displayCalls  int
	criticalCalls int
	lastKey       string
}

func (u *orderUsecaseStub) CreateOrder(_ context.Context, _ string, _ int64, _ string, key string) (*domain.ExampleOrder, error) {
	u.createCalls++
	u.lastKey = key
	return &domain.ExampleOrder{ID: "order-1", Status: "PAYMENT_PENDING"}, nil
}

func (u *orderUsecaseStub) GetOrder(context.Context, string) (*domain.ExampleOrder, error) {
	u.displayCalls++
	return &domain.ExampleOrder{ID: "order-1"}, nil
}

func (u *orderUsecaseStub) GetOrderForCriticalDecision(context.Context, string) (*domain.ExampleOrder, error) {
	u.criticalCalls++
	return &domain.ExampleOrder{ID: "order-1"}, nil
}

func TestOrderRoutesUseIdempotencyAndSeparateReadPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &orderUsecaseStub{}
	router := gin.New()
	handlerhttp.RegisterExampleOrderRoutes(
		router,
		uc,
		idempotency.NewMemoryStorage(),
		resilience.NewMemoryRateLimiter(),
		100,
		time.Minute,
		time.Hour,
		true,
	)

	post := func(body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
		if key != "" {
			req.Header.Set(idempotency.HeaderIdempotencyKey, key)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	require.Equal(t, http.StatusBadRequest, post(`{"customer_id":"c1","amount_minor":100,"currency":"USD"}`, "").Code)
	require.Equal(t, http.StatusCreated, post(`{"customer_id":"c1","amount_minor":100,"currency":"USD"}`, "order-key").Code)
	replay := post(`{"customer_id":"c1","amount_minor":100,"currency":"USD"}`, "order-key")
	require.Equal(t, http.StatusCreated, replay.Code)
	assert.Equal(t, "HIT-IDEMPOTENT", replay.Header().Get("X-Cache-Lookup"))
	assert.Equal(t, 1, uc.createCalls)
	assert.Contains(t, uc.lastKey, "POST:/orders:default:order-key")

	mismatch := post(`{"customer_id":"c1","amount_minor":200,"currency":"USD"}`, "order-key")
	assert.Equal(t, http.StatusConflict, mismatch.Code)
	assert.Equal(t, 1, uc.createCalls)

	displayReq := httptest.NewRequest(http.MethodGet, "/orders/order-1", nil)
	displayRes := httptest.NewRecorder()
	router.ServeHTTP(displayRes, displayReq)
	assert.Equal(t, http.StatusOK, displayRes.Code)

	criticalReq := httptest.NewRequest(http.MethodGet, "/orders/order-1/critical", nil)
	criticalRes := httptest.NewRecorder()
	router.ServeHTTP(criticalRes, criticalReq)
	assert.Equal(t, http.StatusOK, criticalRes.Code)
	assert.Equal(t, 1, uc.displayCalls)
	assert.Equal(t, 1, uc.criticalCalls)
}

func TestOrderCreateUnavailableWithoutWorkflowDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlerhttp.RegisterExampleOrderRoutes(router, nil, nil, nil, 1, time.Second, time.Hour, false)

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{}`))
	req.Header.Set(idempotency.HeaderIdempotencyKey, "key")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
