//go:build examples

package repository

import (
	"context"
	"fmt"
	"time"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/cache"

	"github.com/redis/go-redis/v9"
)

type userRepository struct {
	cacheMgr *cache.Manager
	cacheTTL time.Duration
}

// NewExampleUserRepository creates a new instance of domain.ExampleUserRepository with Singleflight Cache protection.
func NewExampleUserRepository(redisClient redis.UniversalClient, cacheTTL time.Duration) domain.ExampleUserRepository {
	if cacheTTL == 0 {
		cacheTTL = 5 * time.Minute
	}
	return &userRepository{
		cacheMgr: cache.NewManager(redisClient),
		cacheTTL: cacheTTL,
	}
}

// GetByID returns a User by ID protected against cache stampedes.
func (r *userRepository) GetByID(ctx context.Context, id int) (*domain.ExampleUser, error) {
	cacheKey := fmt.Sprintf("user:%d", id)

	return cache.GetOrSet(ctx, r.cacheMgr, cacheKey, r.cacheTTL, func(fetchCtx context.Context) (*domain.ExampleUser, error) {
		return r.fetchFromDatabase(fetchCtx, id)
	})
}

func (r *userRepository) fetchFromDatabase(ctx context.Context, id int) (*domain.ExampleUser, error) {
	// Check for context cancellation before executing DB operation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Simulated DB logic
	if id == 1 {
		return &domain.ExampleUser{
			ID:    1,
			Name:  "Developer",
			Email: "dev@example.com",
		}, nil
	}

	return nil, domain.ErrExampleUserNotFound
}
