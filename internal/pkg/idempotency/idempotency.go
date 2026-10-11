package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRequestInProgress = errors.New("concurrent request with same idempotency key is already in progress")
	ErrInvalidKey        = errors.New("idempotency key cannot be empty")
)

type Status string

const (
	StatusStarted   Status = "STARTED"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
)

// Record represents cached idempotency execution state.
type Record struct {
	Key          string            `json:"key"`
	Fingerprint  string            `json:"fingerprint,omitempty"`
	Status       Status            `json:"status"`
	StatusCode   int               `json:"status_code"`
	Headers      map[string]string `json:"headers"`
	ResponseBody []byte            `json:"response_body"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Storage defines interface for persisting idempotency state.
type Storage interface {
	// TryLock returns (acquired=true, nil, nil) if lock obtained.
	// Returns (acquired=false, existingRecord, nil) if key already exists.
	TryLock(ctx context.Context, key string, inFlightTTL time.Duration) (bool, *Record, error)
	SaveResponse(ctx context.Context, key string, record *Record, completedTTL time.Duration) error
	ReleaseLock(ctx context.Context, key string) error
}

type FingerprintStorage interface {
	TryLockWithFingerprint(ctx context.Context, key, fingerprint string, inFlightTTL time.Duration) (bool, *Record, error)
}

// RedisStorage implements Storage backed by Redis.
type RedisStorage struct {
	client redis.UniversalClient
}

// NewRedisStorage initializes RedisStorage.
func NewRedisStorage(client redis.UniversalClient) *RedisStorage {
	return &RedisStorage{client: client}
}

func (s *RedisStorage) formatKey(key string) string {
	return fmt.Sprintf("idempotency:%s", key)
}

func (s *RedisStorage) TryLock(ctx context.Context, key string, inFlightTTL time.Duration) (bool, *Record, error) {
	return s.TryLockWithFingerprint(ctx, key, "", inFlightTTL)
}

func (s *RedisStorage) TryLockWithFingerprint(ctx context.Context, key, fingerprint string, inFlightTTL time.Duration) (bool, *Record, error) {
	if s.client == nil {
		return true, nil, nil
	}
	rKey := s.formatKey(key)

	initialRecord := Record{
		Key:         key,
		Fingerprint: fingerprint,
		Status:      StatusStarted,
		CreatedAt:   time.Now(),
	}
	data, _ := json.Marshal(initialRecord)

	ok, err := s.client.SetNX(ctx, rKey, data, inFlightTTL).Result()
	if err != nil {
		return false, nil, err
	}
	if ok {
		return true, nil, nil
	}

	// Key already exists, fetch existing record
	val, err := s.client.Get(ctx, rKey).Result()
	if err != nil {
		return false, nil, err
	}

	var existing Record
	if err := json.Unmarshal([]byte(val), &existing); err != nil {
		return false, nil, err
	}

	return false, &existing, nil
}

func (s *RedisStorage) SaveResponse(ctx context.Context, key string, record *Record, completedTTL time.Duration) error {
	if s.client == nil {
		return nil
	}
	rKey := s.formatKey(key)
	record.Status = StatusCompleted

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return s.client.Set(ctx, rKey, data, completedTTL).Err()
}

func (s *RedisStorage) ReleaseLock(ctx context.Context, key string) error {
	if s.client == nil {
		return nil
	}
	rKey := s.formatKey(key)
	return s.client.Del(ctx, rKey).Err()
}

// MemoryStorage provides in-memory implementation for testing/single-instance.
type MemoryStorage struct {
	mu        sync.Mutex
	records   map[string]*Record
	expiresAt map[string]time.Time
}

// NewMemoryStorage initializes MemoryStorage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		records:   make(map[string]*Record),
		expiresAt: make(map[string]time.Time),
	}
}

func (m *MemoryStorage) TryLock(ctx context.Context, key string, inFlightTTL time.Duration) (bool, *Record, error) {
	return m.TryLockWithFingerprint(ctx, key, "", inFlightTTL)
}

func (m *MemoryStorage) TryLockWithFingerprint(ctx context.Context, key, fingerprint string, inFlightTTL time.Duration) (bool, *Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, exists := m.records[key]; exists {
		if expiry := m.expiresAt[key]; expiry.IsZero() || time.Now().Before(expiry) {
			return false, rec, nil
		}
		delete(m.records, key)
		delete(m.expiresAt, key)
	}

	rec := &Record{
		Key:         key,
		Fingerprint: fingerprint,
		Status:      StatusStarted,
		CreatedAt:   time.Now(),
	}
	m.records[key] = rec
	m.expiresAt[key] = time.Now().Add(inFlightTTL)
	return true, nil, nil
}

func (m *MemoryStorage) SaveResponse(ctx context.Context, key string, record *Record, completedTTL time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record.Status = StatusCompleted
	m.records[key] = record
	m.expiresAt[key] = time.Now().Add(completedTTL)
	return nil
}

func (m *MemoryStorage) ReleaseLock(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, key)
	delete(m.expiresAt, key)
	return nil
}
