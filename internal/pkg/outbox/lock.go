package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrLockFailed  = errors.New("failed to acquire distributed lock")
	ErrLockNotHeld = errors.New("lock not held or token mismatch")
)

// DistributedLock defines contract for multi-pod safe distributed locking.
type DistributedLock interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (token string, err error)
	Release(ctx context.Context, key string, token string) error
	Extend(ctx context.Context, key string, token string, ttl time.Duration) error
}

// RedisDistributedLock implements Redlock/SETNX pattern on Redis.
type RedisDistributedLock struct {
	client redis.UniversalClient
}

// NewRedisDistributedLock creates a new RedisDistributedLock instance.
func NewRedisDistributedLock(client redis.UniversalClient) *RedisDistributedLock {
	return &RedisDistributedLock{client: client}
}

// Acquire tries to obtain lock using SET NX PX.
func (l *RedisDistributedLock) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if l.client == nil {
		return "", errors.New("redis client unavailable")
	}

	token := generateToken()
	ok, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrLockFailed
	}
	return token, nil
}

// Release safely releases lock via atomic Lua script.
func (l *RedisDistributedLock) Release(ctx context.Context, key string, token string) error {
	if l.client == nil {
		return nil
	}

	luaScript := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`
	res, err := l.client.Eval(ctx, luaScript, []string{key}, token).Result()
	if err != nil {
		return err
	}
	if res == int64(0) {
		return ErrLockNotHeld
	}
	return nil
}

// Extend renews lock lease via atomic Lua script.
func (l *RedisDistributedLock) Extend(ctx context.Context, key string, token string, ttl time.Duration) error {
	if l.client == nil {
		return nil
	}

	luaScript := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("pexpire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`
	res, err := l.client.Eval(ctx, luaScript, []string{key}, token, ttl.Milliseconds()).Result()
	if err != nil {
		return err
	}
	if res == int64(0) {
		return ErrLockNotHeld
	}
	return nil
}

func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
