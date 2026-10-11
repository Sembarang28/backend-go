package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Manager provides cache operations with Singleflight Stampede Protection.
type Manager struct {
	client redis.UniversalClient
	sf     singleflight.Group
}

// NewManager initializes cache manager.
func NewManager(client redis.UniversalClient) *Manager {
	return &Manager{
		client: client,
	}
}

// LoadWithoutCache invokes loader directly without reading from or writing to
// Redis. Use this for data that must come from its authoritative source.
func LoadWithoutCache[T any](ctx context.Context, loader func(context.Context) (T, error)) (T, error) {
	var zero T
	if loader == nil {
		return zero, fmt.Errorf("cache loader is required")
	}
	return loader(ctx)
}

// GetOrSet retrieves an item from Redis or invokes loader protected by singleflight.
func GetOrSet[T any](ctx context.Context, m *Manager, key string, ttl time.Duration, loader func(ctx context.Context) (T, error)) (T, error) {
	var zero T

	// 1. Try reading from cache directly
	if m != nil && m.client != nil {
		val, err := m.client.Get(ctx, key).Result()
		if err == nil {
			var result T
			if unmarshalErr := json.Unmarshal([]byte(val), &result); unmarshalErr == nil {
				return result, nil
			}
		} else if err != redis.Nil {
			slog.WarnContext(ctx, "Redis cache read failed, proceeding with loader", "key", key, "error", err)
		}
	}

	// 2. Cache Miss: Wrap loader inside singleflight to prevent cache stampede
	if m == nil {
		return loader(ctx)
	}

	res, err, shared := m.sf.Do(key, func() (interface{}, error) {
		// Double check cache within singleflight execution
		if m.client != nil {
			if val, err := m.client.Get(ctx, key).Result(); err == nil {
				var cached T
				if json.Unmarshal([]byte(val), &cached) == nil {
					return cached, nil
				}
			}
		}

		data, loadErr := loader(ctx)
		if loadErr != nil {
			return zero, loadErr
		}

		// Save to Redis asynchronously
		if m.client != nil && ttl > 0 {
			bytes, marshalErr := json.Marshal(data)
			if marshalErr == nil {
				_ = m.client.Set(ctx, key, bytes, ttl).Err()
			}
		}

		return data, nil
	})

	if err != nil {
		return zero, err
	}

	if shared {
		slog.DebugContext(ctx, "Singleflight suppressed concurrent cache stampede", "key", key)
	}

	return res.(T), nil
}

// Invalidate removes a cached key.
func (m *Manager) Invalidate(ctx context.Context, key string) error {
	if m == nil || m.client == nil {
		return nil
	}
	return m.client.Del(ctx, key).Err()
}

// FormatKey formats domain and entity ID into standardized cache key.
func FormatKey(entity string, id any) string {
	return fmt.Sprintf("cache:%s:%v", entity, id)
}
