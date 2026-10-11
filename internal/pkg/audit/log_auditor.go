package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"backend-go/internal/pkg/telemetry"

	"github.com/google/uuid"
)

// AuditEvent represents an audit record for sensitive operations.
type AuditEvent struct {
	ID             string      `json:"id"`
	ActorID        string      `json:"actor_id"`
	Action         string      `json:"action"`
	TargetResource string      `json:"target_resource"`
	ResourceID     string      `json:"resource_id"`
	PayloadBefore  interface{} `json:"payload_before,omitempty"`
	PayloadAfter   interface{} `json:"payload_after,omitempty"`
	IPAddress      string      `json:"ip_address,omitempty"`
	UserAgent      string      `json:"user_agent,omitempty"`
	TraceID        string      `json:"trace_id,omitempty"`
	SpanID         string      `json:"span_id,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
}

// Auditor defines the interface for recording audit events.
type Auditor interface {
	Log(ctx context.Context, event AuditEvent) error
}

// StructuredAuditor outputs audit events to structured slog.
type StructuredAuditor struct {
	logger *slog.Logger
}

// NewStructuredAuditor initializes StructuredAuditor.
func NewStructuredAuditor(logger *slog.Logger) *StructuredAuditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &StructuredAuditor{logger: logger}
}

// Log records an audit event with contextual trace identifiers.
func (a *StructuredAuditor) Log(ctx context.Context, event AuditEvent) error {
	event = enrichEvent(ctx, event)
	beforeJSON, err := json.Marshal(event.PayloadBefore)
	if err != nil {
		return fmt.Errorf("marshal audit payload before: %w", err)
	}
	afterJSON, err := json.Marshal(event.PayloadAfter)
	if err != nil {
		return fmt.Errorf("marshal audit payload after: %w", err)
	}

	a.logger.InfoContext(ctx, "AUDIT_TRAIL",
		slog.String("audit_id", event.ID),
		slog.String("actor_id", event.ActorID),
		slog.String("action", event.Action),
		slog.String("target_resource", event.TargetResource),
		slog.String("resource_id", event.ResourceID),
		slog.String("payload_before", string(beforeJSON)),
		slog.String("payload_after", string(afterJSON)),
		slog.String("ip_address", event.IPAddress),
		slog.String("user_agent", event.UserAgent),
		slog.String("trace_id", event.TraceID),
		slog.String("span_id", event.SpanID),
		slog.Time("created_at", event.CreatedAt),
	)

	return nil
}

func enrichEvent(ctx context.Context, event AuditEvent) AuditEvent {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	if sc, ok := telemetry.SpanFromContext(ctx); ok {
		if event.TraceID == "" {
			event.TraceID = sc.TraceID
		}
		if event.SpanID == "" {
			event.SpanID = sc.SpanID
		}
	}
	return event
}
