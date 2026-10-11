CREATE TABLE IF NOT EXISTS example_orders (
    id VARCHAR(64) PRIMARY KEY,
    customer_id VARCHAR(128) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL,
    status VARCHAR(32) NOT NULL,
    outbox_enqueued BOOLEAN NOT NULL DEFAULT FALSE,
    idempotency_key VARCHAR(256) NOT NULL UNIQUE,
    request_fingerprint CHAR(64) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_example_orders_outbox_reconciliation
    ON example_orders (created_at, id)
    WHERE outbox_enqueued = FALSE;
