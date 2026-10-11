package resilience_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend-go/internal/pkg/resilience"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiter(t *testing.T) {
	limiter := resilience.NewMemoryRateLimiter()
	ctx := context.Background()

	// Allow 2 requests per 100ms
	allowed, remaining, _, err := limiter.Allow(ctx, "user-1", 2, 100*time.Millisecond)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(1), remaining)

	allowed, remaining, _, err = limiter.Allow(ctx, "user-1", 2, 100*time.Millisecond)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining)

	// Third request rejected
	allowed, _, _, err = limiter.Allow(ctx, "user-1", 2, 100*time.Millisecond)
	assert.NoError(t, err)
	assert.False(t, allowed)
}

func TestRedisRateLimiterRejectsInvalidPolicy(t *testing.T) {
	limiter := resilience.NewRedisSlidingWindowLimiter(nil)

	allowed, _, _, err := limiter.Allow(context.Background(), "user-1", 0, time.Minute)
	assert.Error(t, err)
	assert.False(t, allowed)

	allowed, _, _, err = limiter.Allow(context.Background(), "user-1", 1, 0)
	assert.Error(t, err)
	assert.False(t, allowed)
}

func TestCircuitBreaker(t *testing.T) {
	cfg := resilience.CircuitBreakerConfig{
		Name:                "test-service",
		ConsecutiveFailures: 2,
		Timeout:             50 * time.Millisecond,
	}
	cb := resilience.NewCircuitBreaker(cfg)
	ctx := context.Background()

	// 1. Failures trip breaker to OPEN
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(ctx, func(ctx context.Context) (interface{}, error) {
			return nil, errors.New("downstream error")
		}, nil)
	}

	assert.Equal(t, gobreaker.StateOpen, cb.CurrentState())

	// 2. Call while open returns fallback
	res, err := cb.Execute(ctx, func(ctx context.Context) (interface{}, error) {
		return "real", nil
	}, func(ctx context.Context, err error) (interface{}, error) {
		return "fallback", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "fallback", res)

	// 3. Wait for Timeout -> HALF_OPEN -> SUCCESS -> CLOSED
	time.Sleep(70 * time.Millisecond)

	res, err = cb.Execute(ctx, func(ctx context.Context) (interface{}, error) {
		return "recovered", nil
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, "recovered", res)
	assert.Equal(t, gobreaker.StateClosed, cb.CurrentState())
}
