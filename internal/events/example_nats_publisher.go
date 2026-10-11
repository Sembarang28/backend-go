//go:build examples

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"backend-go/internal/domain"

	"github.com/nats-io/nats.go"
)

type natsEventPublisher struct {
	conn *nats.Conn
}

// NewExampleNATSEventPublisher returns a new domain.ExampleEventPublisher backed by NATS.
func NewExampleNATSEventPublisher(conn *nats.Conn) domain.ExampleEventPublisher {
	return &natsEventPublisher{conn: conn}
}

// PublishUserAccessed publishes a UserAccessedEvent to NATS subject 'user.accessed'.
func (p *natsEventPublisher) PublishUserAccessed(ctx context.Context, event domain.ExampleUserAccessedEvent) error {
	if p.conn == nil {
		slog.Debug("NATS connection is nil, skipping event publish", "event", event)
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal user accessed event: %w", err)
	}

	subject := "user.accessed"
	if err := p.conn.Publish(subject, payload); err != nil {
		slog.Error("Failed to publish event to NATS", "subject", subject, "error", err)
		return fmt.Errorf("failed to publish event to NATS: %w", err)
	}

	slog.Info("Successfully published event to NATS", "subject", subject, "user_id", event.UserID, "source", event.Source)
	return nil
}
