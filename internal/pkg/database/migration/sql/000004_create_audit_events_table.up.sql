CREATE TABLE IF NOT EXISTS audit_events (
    id VARCHAR(36) PRIMARY KEY,
    actor_id VARCHAR(255) NOT NULL,
    action VARCHAR(255) NOT NULL,
    target_resource VARCHAR(255) NOT NULL,
    resource_id VARCHAR(255) NOT NULL,
    payload_before JSONB NOT NULL,
    payload_after JSONB NOT NULL,
    ip_address VARCHAR(45) NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    trace_id VARCHAR(32) NOT NULL DEFAULT '',
    span_id VARCHAR(16) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_events_actor_created_at
    ON audit_events (actor_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_events_resource_created_at
    ON audit_events (target_resource, resource_id, created_at DESC);
