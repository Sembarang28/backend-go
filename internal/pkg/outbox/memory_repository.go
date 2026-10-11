package outbox

import (
	"context"
	"sync"
	"time"
)

// MemoryRepository provides thread-safe in-memory storage for development and testing.
type MemoryRepository struct {
	mu     sync.RWMutex
	events map[string]*OutboxEvent
	dlq    map[string]*DLQEvent
}

// NewMemoryRepository initializes MemoryRepository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events: make(map[string]*OutboxEvent),
		dlq:    make(map[string]*DLQEvent),
	}
}

func (m *MemoryRepository) Save(ctx context.Context, event *OutboxEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[event.ID] = event
	return nil
}

func (m *MemoryRepository) FetchPendingBatch(ctx context.Context, partition int, limit int) ([]*OutboxEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var result []*OutboxEvent

	for _, ev := range m.events {
		if ev.Partition == partition && (ev.Status == StatusPending || (ev.Status == StatusFailed && now.After(ev.NextRetryAt))) {
			result = append(result, ev)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryRepository) MarkProcessing(ctx context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if ev, ok := m.events[id]; ok {
			ev.Status = StatusProcessing
			ev.UpdatedAt = time.Now()
		}
	}
	return nil
}

func (m *MemoryRepository) MarkPublished(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev, ok := m.events[id]; ok {
		ev.Status = StatusPublished
		ev.UpdatedAt = time.Now()
	}
	return nil
}

func (m *MemoryRepository) MarkFailed(ctx context.Context, id string, errStr string, nextRetry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev, ok := m.events[id]; ok {
		ev.Status = StatusFailed
		ev.RetryCount++
		ev.LastError = errStr
		ev.NextRetryAt = nextRetry
		ev.UpdatedAt = time.Now()
	}
	return nil
}

func (m *MemoryRepository) MoveToDLQ(ctx context.Context, id string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev, ok := m.events[id]; ok {
		ev.Status = StatusDLQ
		ev.LastError = reason
		ev.UpdatedAt = time.Now()

		dlq := &DLQEvent{
			ID:              "dlq_" + ev.ID,
			OriginalEventID: ev.ID,
			EventType:       ev.EventType,
			Payload:         ev.Payload,
			Headers:         ev.Headers,
			FailureReason:   reason,
			RetryAttempts:   ev.RetryCount,
			FailedAt:        time.Now(),
		}
		m.dlq[dlq.ID] = dlq
	}
	return nil
}

func (m *MemoryRepository) SaveDLQ(ctx context.Context, dlq *DLQEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dlq[dlq.ID] = dlq
	return nil
}

func (m *MemoryRepository) DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int64
	for id, ev := range m.events {
		if ev.Status == StatusPublished && ev.UpdatedAt.Before(before) {
			delete(m.events, id)
			deleted++
			if limit > 0 && int(deleted) >= limit {
				break
			}
		}
	}
	return deleted, nil
}
