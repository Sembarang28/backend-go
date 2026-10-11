package events

import (
	"context"
	"errors"
	"fmt"

	"backend-go/internal/pkg/outbox"
	"backend-go/internal/pkg/resilience"

	"github.com/nats-io/nats.go"
)

type natsOutboxPublisher struct {
	conn    *nats.Conn
	breaker *resilience.CircuitBreaker
}

func NewNATSOutboxPublisher(conn *nats.Conn, breaker *resilience.CircuitBreaker) outbox.MessagePublisher {
	return &natsOutboxPublisher{conn: conn, breaker: breaker}
}

func (p *natsOutboxPublisher) Publish(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
	if p.conn == nil {
		return errors.New("NATS connection unavailable")
	}
	publish := func(ctx context.Context) (interface{}, error) {
		msg := &nats.Msg{Subject: subject, Data: payload, Header: nats.Header{}}
		for key, value := range headers {
			msg.Header.Set(key, value)
		}
		if err := p.conn.PublishMsg(msg); err != nil {
			return nil, fmt.Errorf("publish NATS subject %q: %w", subject, err)
		}
		if err := p.conn.FlushWithContext(ctx); err != nil {
			return nil, fmt.Errorf("flush NATS publish for subject %q: %w", subject, err)
		}
		return nil, nil
	}
	if p.breaker == nil {
		_, err := publish(ctx)
		return err
	}
	_, err := p.breaker.Execute(ctx, publish, nil)
	return err
}
