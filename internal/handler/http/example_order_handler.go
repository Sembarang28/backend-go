//go:build examples

package http

import (
	"errors"
	nethttp "net/http"
	"regexp"
	"strings"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/idempotency"
	"backend-go/internal/pkg/resilience"
	"backend-go/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

type ExampleOrderHandler struct {
	usecase domain.ExampleOrderUsecase
}

func RegisterExampleOrderRoutes(
	r *gin.Engine,
	uc domain.ExampleOrderUsecase,
	idempotencyStorage idempotency.Storage,
	rateLimiter resilience.RateLimiter,
	rateLimit int64,
	rateWindow time.Duration,
	completedTTL time.Duration,
	workflowReady bool,
) {
	handler := &ExampleOrderHandler{usecase: uc}
	r.GET("/orders/:id", requireOrderRead(uc), handler.Get)
	r.GET("/orders/:id/critical", requireOrderRead(uc), handler.GetCritical)

	create := r.Group("/orders")
	create.Use(requireOrderDependencies(uc, idempotencyStorage, rateLimiter, workflowReady))
	create.Use(requireIdempotencyKey())
	create.Use(scopeIdempotencyKey())
	create.Use(resilience.HTTPRateLimitMiddleware(rateLimiter, resilience.TenantOrIPKey, rateLimit, rateWindow))
	create.Use(idempotency.HTTPMiddleware(idempotencyStorage, idempotency.DefaultInFlightTTL, completedTTL))
	create.POST("", handler.Create)
}

type createOrderRequest struct {
	CustomerID  string `json:"customer_id" binding:"required"`
	AmountMinor int64  `json:"amount_minor" binding:"required"`
	Currency    string `json:"currency" binding:"required"`
}

func (h *ExampleOrderHandler) Create(c *gin.Context) {
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, nethttp.StatusBadRequest, "INVALID_ORDER", "A valid customer_id, amount_minor, and currency are required")
		return
	}

	order, err := h.usecase.CreateOrder(
		c.Request.Context(),
		req.CustomerID,
		req.AmountMinor,
		req.Currency,
		c.GetHeader(idempotency.HeaderIdempotencyKey),
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidExampleOrder):
			response.Error(c, nethttp.StatusBadRequest, "INVALID_ORDER", err.Error())
		case errors.Is(err, domain.ErrExampleIdempotencyUsed):
			response.Error(c, nethttp.StatusConflict, "IDEMPOTENCY_KEY_REUSED", err.Error())
		default:
			response.Error(c, nethttp.StatusInternalServerError, "ORDER_CREATE_FAILED", "Order could not be created")
		}
		return
	}
	response.Success(c, nethttp.StatusCreated, order)
}

func (h *ExampleOrderHandler) Get(c *gin.Context) {
	order, err := h.usecase.GetOrder(c.Request.Context(), c.Param("id"))
	h.respondOrder(c, order, err)
}

func (h *ExampleOrderHandler) GetCritical(c *gin.Context) {
	order, err := h.usecase.GetOrderForCriticalDecision(c.Request.Context(), c.Param("id"))
	h.respondOrder(c, order, err)
}

func (*ExampleOrderHandler) respondOrder(c *gin.Context, order *domain.ExampleOrder, err error) {
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidExampleOrder):
			response.Error(c, nethttp.StatusBadRequest, "INVALID_ORDER_ID", err.Error())
		case errors.Is(err, domain.ErrExampleOrderNotFound):
			response.Error(c, nethttp.StatusNotFound, "ORDER_NOT_FOUND", err.Error())
		default:
			response.Error(c, nethttp.StatusInternalServerError, "ORDER_READ_FAILED", "Order could not be read")
		}
		return
	}
	response.Success(c, nethttp.StatusOK, order)
}

func requireOrderDependencies(
	uc domain.ExampleOrderUsecase,
	storage idempotency.Storage,
	limiter resilience.RateLimiter,
	workflowReady bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if uc == nil || storage == nil || limiter == nil || !workflowReady {
			response.Error(c, nethttp.StatusServiceUnavailable, "ORDER_WORKFLOW_UNAVAILABLE", "Order creation requires PostgreSQL, Redis, and NATS")
			c.Abort()
			return
		}
		c.Next()
	}
}

func requireOrderRead(uc domain.ExampleOrderUsecase) gin.HandlerFunc {
	return func(c *gin.Context) {
		if uc == nil {
			response.Error(c, nethttp.StatusServiceUnavailable, "ORDER_READ_UNAVAILABLE", "Order reads require PostgreSQL")
			c.Abort()
			return
		}
		c.Next()
	}
}

func requireIdempotencyKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader(idempotency.HeaderIdempotencyKey))
		if key == "" || len(key) > 128 {
			response.Error(c, nethttp.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "X-Idempotency-Key is required and must be at most 128 characters")
			c.Abort()
			return
		}
		c.Next()
	}
}

var safeIdempotencyKeyPart = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func scopeIdempotencyKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(idempotency.HeaderIdempotencyKey)
		tenant := safeIdempotencyKeyPart.ReplaceAllString(c.GetHeader("X-Tenant-ID"), "_")
		if tenant == "" {
			tenant = "default"
		}
		if len(tenant) > 64 {
			tenant = tenant[:64]
		}
		c.Request.Header.Set(idempotency.HeaderIdempotencyKey, c.Request.Method+":"+c.FullPath()+":"+tenant+":"+key)
		c.Next()
	}
}
