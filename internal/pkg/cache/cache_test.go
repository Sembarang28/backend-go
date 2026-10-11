package cache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"backend-go/internal/pkg/cache"

	"github.com/stretchr/testify/assert"
)

type mockUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestSingleflightCacheProtection(t *testing.T) {
	mgr := cache.NewManager(nil) // in-memory fallback without redis
	ctx := context.Background()

	var dbQueryCount int32

	loader := func(c context.Context) (mockUser, error) {
		time.Sleep(20 * time.Millisecond) // simulate expensive query
		atomic.AddInt32(&dbQueryCount, 1)
		return mockUser{ID: 10, Name: "Aditya"}, nil
	}

	// 50 concurrent requests for the exact same cache key
	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			user, err := cache.GetOrSet(ctx, mgr, "user:10", 1*time.Minute, loader)
			assert.NoError(t, err)
			assert.Equal(t, 10, user.ID)
			assert.Equal(t, "Aditya", user.Name)
		}()
	}

	wg.Wait()

	// Singleflight must ensure loader was invoked ONLY ONCE despite 50 concurrent requests
	assert.Equal(t, int32(1), atomic.LoadInt32(&dbQueryCount), "Singleflight should suppress multiple DB queries for identical key")
}

func TestLoadWithoutCacheAlwaysCallsLoader(t *testing.T) {
	ctx := context.Background()
	var calls int
	want := mockUser{ID: 11, Name: "Fresh"}

	got, err := cache.LoadWithoutCache(ctx, func(context.Context) (mockUser, error) {
		calls++
		return want, nil
	})

	assert.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, calls)
}

func TestLoadWithoutCacheRejectsNilLoader(t *testing.T) {
	_, err := cache.LoadWithoutCache[mockUser](context.Background(), nil)
	assert.Error(t, err)
}
