# Outbox Package Guide

`internal/pkg/outbox` provides a repository contract, a PostgreSQL persistence
adapter, an in-memory adapter for tests, a partitioned worker, Redis-based
distributed locks, and a retention cleaner.

## Persistence and database support

The worker and cleaner depend only on `outbox.Repository`. Use
`outbox.NewPostgresRepository` for durable PostgreSQL storage or
`outbox.NewMemoryRepository` for tests and local-only use. The PostgreSQL
adapter uses Go's `database/sql`; the application owns the connection pool and
the driver. This keeps connection lifecycle, credentials, and driver settings
under application control.

The repository uses the `outbox_events` and `dlq_events` tables defined in
`internal/pkg/database/migration/sql/000002_create_outbox_table.up.sql` and
`000003_create_dlq_table.up.sql`. Apply the project's migrations before
starting the worker:

```go
if err := migration.NewEngine().Run(ctx, db); err != nil {
    return fmt.Errorf("run database migrations: %w", err)
}

outboxRepo := outbox.NewPostgresRepository(db)
```

The project integration test uses pgx's `database/sql` driver. Register and
open the driver in the application (or choose another PostgreSQL-compatible
driver):

```go
import (
    "database/sql"

    _ "github.com/jackc/pgx/v5/stdlib"
)

db, err := sql.Open("pgx", dsn)
if err != nil {
    return err
}
defer db.Close()
```

## Write events in the business transaction

To preserve the transactional outbox guarantee, save the business change and
the outbox event using the same database transaction. Pass the transaction as
the repository's `SQLExecutor`:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

if _, err := tx.ExecContext(ctx,
    "UPDATE orders SET status = $1 WHERE id = $2", "created", orderID,
); err != nil {
    return err
}

txOutbox := outbox.NewPostgresRepository(tx)
if err := txOutbox.Save(ctx, &outbox.OutboxEvent{
    ID:            eventID,
    AggregateType: "Order",
    AggregateID:   orderID,
    EventType:     "order.created",
    Payload:       payload,
    Headers:       map[string]string{"traceparent": traceparent},
    Partition:     partition,
}); err != nil {
    return err
}

if err := tx.Commit(); err != nil {
    return err
}
```

Only use this transaction-bound repository for writes in that transaction.
Create a separate repository from `*sql.DB` for the long-running worker and
cleaner, since they need their own connections.

## Start the worker

The worker polls each configured partition, acquires a distributed lock for
that partition, and publishes eligible events. Provide a `DistributedLock`
implementation; for multi-instance deployments, use the included Redis lock.

```go
repo := outbox.NewPostgresRepository(db)
lock := outbox.NewRedisDistributedLock(redisClient)
worker := outbox.NewWorker(outbox.DefaultConfig(), repo, lock, publisher)

worker.Start(ctx)
// During shutdown, cancel ctx and then wait for the worker to stop.
worker.Stop()
```

Run one cleaner per deployment (the Redis lock prevents concurrent cleanups):

```go
cleaner := outbox.NewCleaner(outbox.DefaultCleanerConfig(), repo, lock)
cleaner.Start(ctx)
// During shutdown:
cleaner.Stop()
```

The cleaner removes only published events older than the configured retention
period. DLQ records are retained for application-managed inspection and
redrive.

## Delivery and operational behavior

- Publishing is **at least once**, not exactly once. A process can publish an
  event and terminate before recording it as published. Consumers should
  deduplicate by event ID or otherwise be idempotent.
- Failed publishes are retried with exponential backoff. After the worker's
  `MaxRetries` threshold, the event is marked `DLQ` and copied to `dlq_events`.
- `Save` is idempotent by event ID (upsert). Use stable, unique event IDs.
- `FetchPendingBatch` returns pending events and failed events whose retry time
  has arrived, ordered by creation time within a partition.
- Configure the worker's `PartitionCount` to match the range used when
  assigning event partitions. Changing the count requires a coordinated
  rollout so producers and workers agree on partition assignment.

## Adding support for another database

Keep business code, `Worker`, and `Cleaner` using `outbox.Repository`; do not
add database-specific branches to those components. To support another
database:

1. Implement every method in `outbox.Repository` in a separate adapter, such as
   `MySQLRepository`.
2. Keep its SQL, placeholder syntax, locking/claim semantics, and schema
   migrations specific to that database.
3. Preserve the observable contract: retry scheduling, partition filtering,
   status transitions, DLQ behavior, retention limits, and context/error
   propagation.
4. Ensure moving an event to the DLQ and updating its source status are atomic.
   Use one statement or a transaction.
5. Add database integration tests for transactions, concurrent workers,
   retries, DLQ, and bounded cleanup.

`SQLExecutor` is only the adapter's minimal `database/sql` query surface; it is
not intended to make SQL dialects interchangeable. For a database that does
not use `database/sql`, its adapter can implement `Repository` directly and
use its native client. The application selects the adapter during dependency
injection; no worker or use-case changes are needed.
