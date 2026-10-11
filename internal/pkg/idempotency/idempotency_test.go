package idempotency_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"backend-go/internal/pkg/idempotency"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdempotencyMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storage := idempotency.NewMemoryStorage()

	var counter int32

	r := gin.New()
	r.Use(idempotency.HTTPMiddleware(storage, 5*time.Second, 1*time.Minute))
	r.POST("/orders", func(c *gin.Context) {
		atomic.AddInt32(&counter, 1)
		c.JSON(http.StatusCreated, gin.H{"order_id": "ord-99", "count": atomic.LoadInt32(&counter)})
	})

	// First Request: Executes handler
	req1 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req1.Header.Set(idempotency.HeaderIdempotencyKey, "idem-key-1")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusCreated, w1.Code)
	assert.Contains(t, w1.Body.String(), `"order_id":"ord-99"`)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter))

	// Second Request with SAME key: Replays cached response without executing handler
	req2 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req2.Header.Set(idempotency.HeaderIdempotencyKey, "idem-key-1")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Contains(t, w2.Body.String(), `"order_id":"ord-99"`)
	assert.Equal(t, "HIT-IDEMPOTENT", w2.Header().Get("X-Cache-Lookup"))
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter), "Handler should not have been called a second time")
}

func TestIdempotencyMiddlewareRejectsReusedKeyWithDifferentBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storage := idempotency.NewMemoryStorage()
	var calls atomic.Int32

	router := gin.New()
	router.Use(idempotency.HTTPMiddleware(storage, time.Second, time.Minute))
	router.POST("/orders", func(c *gin.Context) {
		calls.Add(1)
		c.Status(http.StatusCreated)
	})

	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
		req.Header.Set(idempotency.HeaderIdempotencyKey, "same-key")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	first := request(`{"amount":100}`)
	require.Equal(t, http.StatusCreated, first.Code)

	retry := request(`{"amount":100}`)
	require.Equal(t, http.StatusCreated, retry.Code)
	require.Equal(t, "HIT-IDEMPOTENT", retry.Header().Get("X-Cache-Lookup"))

	mismatch := request(`{"amount":200}`)
	require.Equal(t, http.StatusConflict, mismatch.Code)
	require.Contains(t, mismatch.Body.String(), "IDEMPOTENCY_KEY_REUSED")
	require.EqualValues(t, 1, calls.Load())
}
