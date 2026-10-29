# Go Backend Clean Architecture Template (Gin & gRPC)

Production-grade Go backend architecture featuring Gin HTTP, gRPC delivery, transactional Outbox worker with distributed locking, idempotency engine, W3C distributed tracing, resilient rate limiting & circuit breakers, and multi-tenant read/write database routing.

## Project Structure

```plaintext
backend-go/
├── cmd/
│   └── api/
│       ├── main.go                 # App entry point (DI & HTTP/gRPC server startup)
│       └── pprof.go                # Isolated internal pprof diagnostic server (:6060)
├── deployments/
│   ├── docker/                     # Multi-stage hardened Dockerfile
│   └── k8s/                        # Kubernetes Deployment, Job, NetPol, HPA, PDB manifests
├── docs/
│   ├── infra_operations_guide.md          # Panduan deployment Kubernetes, observability, dan runbook
│   ├── postgres_replication_guide.md      # Panduan PostgreSQL critical reads dan replication lag
│   ├── redis_replication_guide.md         # Panduan Redis routing, critical reads, dan replication lag
│   ├── contributing_guide.md               # Panduan developer untuk berkontribusi
│   ├── audit_guide.md                       # Pilihan audit log-only atau persistence PostgreSQL
│   ├── outbox_guide.md                      # PostgreSQL persistence, transactions, worker setup, and adapter extensibility
│   ├── reconciliation_guide.md              # Implementing and operating reconciliation jobs
│   ├── configuration_management_migration_guide.md # Centralized Config (Consul/Vault/ESO) migration guide
├── internal/
│   ├── config/                     # Configuration loader (.env & system environment)
│   ├── domain/                     # Shared domain errors; sample domains are optional
│   ├── events/                     # Message broker publishers (NATS / Kafka)
│   ├── handler/
│   │   ├── http/                   # REST API Delivery Layer (Gin controllers & routes)
│   │   └── grpc/                   # gRPC Delivery Layer (Service server adapters)
│   ├── pkg/
│   │   ├── audit/                  # Immutable Audit Trail logging
│   │   ├── cache/                  # Singleflight Stampede-Protected Cache Manager
│   │   ├── database/               # Read/Write Splitting & Dynamic Multi-Tenant DB Router
│   │   ├── health/                 # Non-blocking K8s Liveness & Readiness Probes
│   │   ├── idempotency/            # Universal Idempotency Engine (HTTP & gRPC middleware)
│   │   ├── metrics/                # Prometheus RED & Outbox/DB Pool Collector
│   │   ├── middleware/             # Context Timeout, Security Headers, CORS, Recovery
│   │   ├── outbox/                 # Transactional Outbox Worker, Distributed Lock, DLQ, Cleaner
│   │   ├── reconciliation/         # Background Self-Healing & Transaction Reconciliation
│   │   ├── resilience/             # Distributed Rate Limiting (Sliding Window) & Circuit Breaker
│   │   ├── response/               # Standardized JSON response helpers
│   │   ├── shutdown/               # Phased Graceful Shutdown Engine
│   │   └── telemetry/              # W3C TraceContext propagator & slog TraceHandler
│   ├── repository/                 # Data Access Layer with caching & DB persistence
│   └── usecase/                    # Shared use-case infrastructure and optional examples
├── proto/
│   └── example_user/v1/example_user.proto # Optional sample Protobuf service
├── tests/
│   └── integration/                # End-to-End Testcontainers tests (PostgreSQL + Redis)
├── Makefile
├── go.mod
└── README.md
```

Panduan memulai kontribusi tersedia di
[docs/contributing_guide.md](docs/contributing_guide.md).

## License

This project is licensed under the Apache License 2.0. See [LICENSE](LICENSE)
for the full license text.

---

## Enterprise Modules & Features

### 1. Advanced Outbox Worker & Distributed Locking (`internal/pkg/outbox`)
- **Distributed Lock (`lock.go`)**: Multi-pod safe distributed locking using Redis `SET NX PX` and atomic Lua release/extension scripts.
- **Partitioned Batching (`worker.go`)**: Multi-partition worker pool for high-throughput event processing.
- **Dead Letter Queue (`DLQ`)**: Automatic routing to DLQ when event retry limit is reached with exponential backoff.
- **Trace Context Propagation**: Preserves W3C `traceparent` from request headers to message broker payload headers.
- **Persistence**: `memory_repository.go` provides the in-memory test adapter and `postgres_repository.go` provides PostgreSQL persistence backed by `database/sql`. The PostgreSQL repository can share the business transaction when saving events. See the [Outbox Package Guide](docs/outbox_guide.md) for setup and adding other database adapters.

### 2. Idempotency Engine (`internal/pkg/idempotency`)
- **HTTP Middleware (`HTTPMiddleware`)**: Intercepts requests with `X-Idempotency-Key` header, locks in-flight processing, caches response payloads, and replays completed responses on duplicate calls.
- **gRPC Interceptor (`UnaryServerInterceptor`)**: Intercepts gRPC calls carrying `x-idempotency-key` metadata, preventing duplicate execution and replaying cached Protobuf/JSON responses.

### 3. Distributed Tracing & Telemetry (`internal/pkg/telemetry`)
- **W3C TraceContext Propagator (`trace.go`)**: Seamless context injection/extraction across HTTP headers, gRPC metadata, NATS/Kafka broker headers, and Go `context.Context`.
- **Structured Logger Handler (`slog_handler.go`)**: Decorates `slog.Handler` to automatically attach `trace_id` and `span_id` to every structured log entry.

### 4. Resiliency & Rate Limiting (`internal/pkg/resilience`)
- **Distributed Sliding Window Rate Limiter (`ratelimit.go`)**: Enforces rate limits per-tenant (`X-Tenant-ID`), per-user (`X-User-ID`), or per-IP using Redis Sorted Sets and atomic Lua scripts.
- **Circuit Breaker (`circuitbreaker.go`)**: Powered by `github.com/sony/gobreaker/v2` with state machine (`CLOSED`, `OPEN`, `HALF_OPEN`) protecting downstream HTTP and gRPC service integrations with automatic fallback execution.
- **gRPC Resiliency Interceptors (`grpc.go`)**: Native `UnaryServerRateLimitInterceptor` and `UnaryClientCircuitBreakerInterceptor`.

### 5. Database Optimization & Multi-Tenancy (`internal/pkg/database`)
- **Read/Write Splitting (`DBGroup`)**: Menyediakan `Write()` untuk mutasi, `ReadPrimary()` untuk critical reads, dan `Read()` untuk query toleran-stale yang dibagi round-robin ke read replicas.
- **Critical Read Guidance**: Gunakan PostgreSQL primary untuk read-after-write dan keputusan critical; lewati cache Redis untuk data authoritative. Lihat [Panduan PostgreSQL Replication Lag](docs/postgres_replication_guide.md) dan [Panduan Redis Replication Lag](docs/redis_replication_guide.md) untuk panduan spesifik masing-masing datastore.
- **Optional examples**: sample user/order handlers, workflows, migrations, and guides are excluded from the default build. Run with `make run-examples`, test with `make test-examples`, or remove all files named as examples in one step with `make remove-examples`.
- **Dynamic Tenant Router (`DynamicTenantRouter`)**: Resolves tenant-specific database connection pools dynamically based on `TenantID` in context.
- **Embedded Migration Engine (`internal/pkg/database/migration`)**: Programmatic DDL execution using `go:embed` SQL files with transaction safety and version tracking (`schema_migrations`).

### 6. Kubernetes Probes, Observability & Graceful Shutdown
- **Health Probes (`internal/pkg/health`)**: Non-blocking asynchronous checks for Primary/Replica DB, Redis, and NATS at `/healthz/live` and `/healthz/ready`.
- **Prometheus Metrics (`internal/pkg/metrics`)**: Exposes HTTP/gRPC RED metrics, outbox queue metrics, and DB connection pool stats at `/metrics`.
- **Phased Graceful Shutdown (`internal/pkg/shutdown`)**: Multi-phase termination handling SIGTERM/SIGINT (Readiness flip -> Ingress drain -> Listener shutdown -> Worker drain -> Client cleanup).
- **Context Deadlines & Timeouts (`internal/pkg/middleware/timeout.go`)**: Global request timeout injection with downstream deadline propagation.
- **Go Runtime & Concurrency Hardening**: `automaxprocs` integration for Kubernetes CPU quota alignment and isolated internal diagnostic server (`cmd/api/pprof.go` on `:6060`).

### 7. Security, Auditing & Panic Recovery
- **Panic Recovery Middleware (`internal/pkg/middleware/recovery.go`)**: Recovers runtime panics across Gin and gRPC with structured JSON logs preserving contextual trace identifiers.
- **Security Headers & Strict CORS (`internal/pkg/middleware/security.go`)**: OWASP-aligned response headers (`X-Frame-Options`, `X-Content-Type-Options`, `HSTS`, `CSP`) and fine-grained CORS.
- **Audit Trail (`internal/pkg/audit`)**: Catat aksi penting ke structured log saja atau simpan secara persisten di PostgreSQL; gunakan transaksi yang sama dengan perubahan bisnis jika audit wajib atomik. Lihat [Panduan Paket Audit](docs/audit_guide.md).
- **Outbox Retention Cleaner (`internal/pkg/outbox/cleaner.go`)**: Distributed locked background worker that purges processed records to prevent table bloat.

### 8. High Performance & Testability Patterns
- **Singleflight Cache Stampede Protection (`internal/pkg/cache`)**: Utilizes `golang.org/x/sync/singleflight` to suppress redundant database queries during concurrent cache misses on high-traffic keys.
- **Self-Healing Reconciliation Engine (`internal/pkg/reconciliation`)**: Background distributed-locked reconciliation daemon to resolve stuck/pending transactions with external providers. See the [Reconciliation Guide](docs/reconciliation_guide.md) for implementing and registering jobs.
- **Integration Testing with Testcontainers (`tests/integration`)**: Automated Docker-based integration tests verifying PostgreSQL, Redis, DDL migrations, and Outbox locking end-to-end (`make test-integration`).

---

## Deployment & Kubernetes Manifests

Ready-to-use production assets located under `deployments/`:
- **Docker (`deployments/docker/Dockerfile`)**: Multi-stage build with Go module caching, non-root user `appuser:10001`, and minimal runtime image.
- **Kubernetes (`deployments/k8s/`)**:
  - `deployment.yaml`: Zero-downtime rolling update, security context, liveness/readiness/startup probes, and `preStop` hook.
  - `job-migration.yaml`: Pre-deployment DDL database migration Job.
  - `networkpolicy.yaml`: Microsegmentation restricting ingress/egress network traffic.
  - `hpa.yaml`: Horizontal Pod Autoscaler targeting CPU (70%), Memory (80%), and Prometheus metrics.
  - `pdb.yaml`: Pod Disruption Budget guaranteeing minimum pod availability.
  - `service.yaml` & `configmap.yaml`: Networking and environment configurations.

---

## Handler Layer Organization

- **HTTP Handlers (`internal/handler/http`)**:
  - Contains HTTP delivery middleware; sample routes are opt-in with the `examples` build tag.
- **gRPC Handlers (`internal/handler/grpc`)**:
  - Contains gRPC server infrastructure; sample handlers are opt-in with the `examples` build tag.

---

## Configuration

Configuration values are dynamically loaded from environment-specific files based on `APP_ENV` (`.env.development` or `.env.production`), falling back to `.env` and system environment variables.

### Environment Files:
- **`.env.development`**: Configured for local debugging with verbose `LOG_LEVEL=debug`, relaxed timeouts (`60s`) for IDE breakpoint inspection, local addresses, and lightweight Outbox concurrency.
- **`.env.production`**: Hardened for production with `LOG_LEVEL=info`, strict timeouts (`10s`), high-throughput Outbox worker partitioning, and tight rate limits.
- **`.env.example`**: Comprehensive reference listing all available configuration variables.

### Available Variables:

| Variable | Description | Development Default | Production Default |
|----------|-------------|---------------------|--------------------|
| `APP_ENV` | Application environment (`development` / `production`). | `development` | `production` |
| `LOG_LEVEL` | Structured log severity (`debug`, `info`, `warn`, `error`). | `debug` | `info` |
| `SERVER_ADDRESS` | HTTP server host and port. | `127.0.0.1:8080` | `:8080` |
| `GRPC_ADDRESS` | gRPC server host and port. | `127.0.0.1:50051` | `:50051` |
| `PPROF_ADDRESS` | Isolated diagnostic pprof server address. | `127.0.0.1:6060` | `127.0.0.1:6060` |
| `READ_TIMEOUT` | HTTP server read timeout. | `60s` | `10s` |
| `WRITE_TIMEOUT` | HTTP server write timeout. | `60s` | `10s` |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown timeout before forced kill. | `5s` | `15s` |
| `DB_PRIMARY_DSN` | Primary PostgreSQL connection string (Write). | `postgres://.../appdb_dev` | `postgres://.../appdb` |
| `DB_REPLICA_DSN` | Read-replica PostgreSQL connection string (Read). | `postgres://.../appdb_dev` | `postgres://.../appdb` |
| `DB_MAX_OPEN_CONNS`| Max open connections in DB pool. | `10` | `50` |
| `DB_MAX_IDLE_CONNS`| Max idle connections in DB pool. | `5` | `20` |
| `DB_CONN_MAX_LIFETIME`| Max connection lifetime in pool. | `10m` | `15m` |
| `REDIS_ADDRESS` | Redis host and port for lock & caching. | `127.0.0.1:6379` | `redis-cluster:6379` |
| `CACHE_TTL` | Entity caching duration. | `1m` | `15m` |
| `NATS_ADDRESS` | Message broker address. | `nats://127.0.0.1:4222` | `nats://nats-cluster:4222` |
| `OUTBOX_PARTITIONS`| Outbox processing partition count. | `2` | `16` |
| `OUTBOX_BATCH_SIZE`| Batch fetch size per partition. | `20` | `200` |
| `OUTBOX_POLL_INTERVAL`| Polling interval for pending events. | `500ms` | `100ms` |
| `IDEMPOTENCY_TTL` | Deduplication response cache duration. | `10m` | `24h` |
| `RATE_LIMIT_REQUESTS`| Sliding-window rate limit request count. | `1000` | `100` |
| `RATE_LIMIT_WINDOW`| Sliding-window duration. | `1m` | `1m` |

---

## Getting Started

### 1. Run the Server Locally (Development / Debug Mode)
```bash
APP_ENV=development go run ./cmd/api
```
The server will run on `http://127.0.0.1:8080` with pprof at `http://127.0.0.1:6060/debug/pprof/`.

### 2. Run the Server in Production Mode
```bash
APP_ENV=production go run ./cmd/api
```

### 3. Run the Optional Examples
```bash
make run-examples
```
The examples use the Go `examples` build tag and are not part of the default
application build. Use `make remove-examples` to delete their source, tests,
migrations, and example-only guides from a fork.

### 4. Run Unit & Integration Tests
```bash
# Run unit tests
go test -v -cover ./...

# Run Testcontainers integration tests (requires Docker)
make test-integration
```

### 5. Build the Binary
```bash
go build -o bin/api ./cmd/api
./bin/api

# Build the schema migrator used by deployments/k8s/job-migration.yaml
go build -o bin/migrate ./cmd/migrate
```

---

## Creating a New Project from this Template

You can easily instantiate a brand new project with a customized module path and name using the automated instructions below.

### Method A: Using Make (Recommended)
If `make` is installed on your system:
```bash
make new path=../my-new-project module=my-new-project
```

### Method B: Using Shell Commands
If `make` is not available, execute this command directly in your shell (adjust the path and module values as needed):
```bash
NEW_PATH="../my-new-project"
NEW_MODULE="my-new-project"

mkdir -p "$NEW_PATH"
rsync -av --exclude='bin' --exclude='.git' ./ "$NEW_PATH"/
perl -pi -e "s|module backend-go|module $NEW_MODULE|g" "$NEW_PATH/go.mod"
find "$NEW_PATH" -type f \( -name "*.go" -o -name "*.proto" -o -name "*.yaml" -o -name "*.yml" -o -name "Makefile" -o -name "Dockerfile" \) -exec perl -pi -e "s|backend-go|$NEW_MODULE|g" {} +
cd "$NEW_PATH" && go mod tidy && go test ./...
```

---

## Trivia: Glossary of Terms

This section explains the technical terms used in this README.

Overall, this template already covers many things that are usually added late in a backend skeleton: transactional outbox, DLQ, reconciliation, idempotency, distributed rate limiter, circuit breaker, phased graceful shutdown, health probes, Prometheus, audit log, read-replica routing, hardened Kubernetes manifests, and separation of example code via the `examples` build tag. The sample order flow (`example_order_repository.go`) also applies the correct idempotency pattern at the database level, for example through a unique constraint or `INSERT ... ON CONFLICT`. This is more reliable than a manual check in application code because it is not prone to race conditions.

### Architecture & Delivery

| Term | Explanation |
|---|---|
| **Clean Architecture** | Code is split into layers (handler, usecase, repository, domain). Outer layers may depend on inner layers, never the reverse, so business logic is not tied to a framework or database. |
| **DI (Dependency Injection)** | Dependencies (DB, cache, etc.) are created in one place (`main.go`) and handed to the components that need them, instead of each component creating its own. This makes testing easier. |
| **Gin** | An HTTP framework for Go, used here for the REST API. |
| **gRPC** | An RPC protocol built on HTTP/2 with a binary format. Commonly used for service-to-service communication because it is faster than REST/JSON. |
| **Protobuf (Protocol Buffers)** | A binary serialization format and schema definition language (`.proto`) used by gRPC. |
| **Delivery layer** | The layer that receives external requests (HTTP or gRPC) and passes them on to the usecase. |
| **Interceptor** | The gRPC equivalent of middleware: code that runs before or after a gRPC call. |
| **Middleware** | A function that runs between an incoming request and the handler, for example for timeouts, CORS, or recovery. |
| **NATS / Kafka** | Message brokers: intermediaries that receive events from one service and forward them to others. |
| **Multi-tenant** | One application serves many customers (tenants) and keeps their data separate. Here the DB connection is chosen per `TenantID`. |
| **DSN** | Data Source Name: the database connection string (host, user, password, DB name). |
| **TTL (Time To Live)** | How long data (cache entries, idempotency results) stays valid before it expires. |

### Data Reliability & Messaging

| Term | Explanation |
|---|---|
| **Transactional Outbox** | Events are stored in a DB table within the same transaction as the business data, then sent to the message broker by a separate worker. As a result, no data is saved without its event, and no event is sent without its data. |
| **Outbox Worker / Partitioned Batching** | A worker that reads the outbox table and sends events to the broker. Events are split into partitions and fetched in batches for high throughput. |
| **Outbox Cleaner (Retention)** | A worker that deletes old outbox records that were already sent, so the table does not bloat. |
| **DLQ (Dead Letter Queue)** | A place to hold events that failed processing repeatedly, so they are not lost and do not block the queue. |
| **Exponential backoff** | Increasing delays between retries (for example 1s, 2s, 4s, 8s), so a struggling system is not flooded with retries. |
| **Idempotency** | The same request sent multiple times (for example because of retries) is processed only once. For instance, it prevents duplicate orders or payments. |
| **Idempotency key** | A unique ID sent by the client via the `X-Idempotency-Key` header (HTTP) or the `x-idempotency-key` metadata (gRPC) to mark identical requests. |
| **In-flight** | A request that is currently being processed and has not finished yet. |
| **Replay response** | For a duplicate request, the server returns the stored response without running the work again. |
| **Reconciliation** | A periodic process that compares data across systems or with an external provider (for example a payment gateway) and fixes stuck or out-of-sync transactions. |
| **Self-healing** | The system repairs inconsistencies automatically without human intervention. |
| **Audit trail / Audit log** | A permanent record of who did what and when. It can go to the log only, or be stored in PostgreSQL. |
| **Atomic** | Several operations are treated as one unit: either all succeed or all are rolled back. |
| **Transaction (DB)** | A group of database operations that behave atomically (commit or rollback). |

### Distributed Systems & Resiliency

| Term | Explanation |
|---|---|
| **Distributed lock** | A lock shared across pods/instances (via Redis) so that only one instance runs a given task at a time, for example the outbox worker. |
| **`SET NX PX`** | A Redis command that creates a key only if it does not exist yet (`NX`) with an expiry in milliseconds (`PX`). This is the basis of a distributed lock. |
| **Lua script (Redis)** | A small script that Redis executes atomically. Used to release or extend a lock safely, and for the rate limiter. |
| **Redis Sorted Set** | A Redis data structure holding scored elements. Here it stores request timestamps for the rate limiter. |
| **Rate limiting** | Limiting the number of requests per period, so the system is not overwhelmed or abused. |
| **Sliding window** | A rate-limit algorithm that counts requests within a continuously moving time window. More accurate than a fixed window (for example per full minute). |
| **Circuit breaker** | If an external service keeps failing, calls to it are paused so your system does not go down with it. States: `CLOSED` (normal), `OPEN` (blocked), `HALF_OPEN` (trying a few requests again). |
| **Fallback** | A backup action run when the primary service fails or the circuit breaker is open. |
| **Downstream** | A service or component called by our service. |
| **Cache stampede** | Many concurrent requests hit an empty cache and all slam the database at once. |
| **Singleflight** | A technique that merges many identical concurrent requests into a single execution and shares the result. This is how cache stampede is prevented. |
| **Context deadline / timeout** | A time limit passed through Go's `context.Context` to every call beneath it, so requests do not hang forever. |

### Database

| Term | Explanation |
|---|---|
| **Read/Write splitting** | Write queries go to the main DB (primary), read queries go to replicas. This reduces load on the primary. |
| **Read replica** | A read-only copy of the database, kept in sync from the primary. |
| **Round-robin** | Distributing load across several targets (for example replicas) in turn. |
| **Replication lag** | The delay before a change on the primary shows up on a replica. A replica may return slightly stale data. |
| **Read-after-write** | Reading data right after writing it. It must be read from the primary to avoid replication lag. |
| **Critical read** | A read for an important decision that must not use stale data (`ReadPrimary()`). |
| **Stale-tolerant read** | A read that may be slightly stale, for example lists or reports (`Read()`). |
| **Connection pool** | A set of DB connections that are reused so a new connection is not opened for every query. |
| **DDL** | Data Definition Language: commands that change the DB structure (`CREATE TABLE`, `ALTER`, etc.). |
| **Migration** | A versioned script for changing the database schema in a controlled way. Versions are tracked in the `schema_migrations` table. |
| **`go:embed`** | A Go feature that bundles files (here, SQL files) into the binary at build time. |
| **`ON CONFLICT` / unique constraint** | A DB rule that rejects duplicate data. Well suited to enforcing idempotency at the database level. |

### Observability

| Term | Explanation |
|---|---|
| **Observability** | The ability to understand a system's state from the outside through logs, metrics, and traces. |
| **W3C TraceContext / `traceparent`** | A standard header for passing trace IDs between services, so a single request can be followed from start to finish. |
| **`trace_id` / `span_id`** | IDs for a request's whole journey (trace) and a single step within it (span). |
| **Propagator** | A component that injects and extracts trace IDs from HTTP headers, gRPC metadata, or broker headers. |
| **`slog`** | Go's built-in structured logging package. |
| **Structured log** | A log in a structured format (key-value/JSON) instead of free text, making it easy to search and filter. |
| **Prometheus** | A monitoring system that collects metrics from the `/metrics` endpoint. |
| **RED metrics** | Three standard service metrics: *Rate* (number of requests), *Errors* (number of errors), *Duration* (response time). |
| **pprof** | Go's built-in profiling tool for inspecting CPU, memory, and goroutine usage. Here it runs on the internal port `:6060`. |
| **`automaxprocs`** | A library that aligns `GOMAXPROCS` with the Kubernetes container CPU limit, so Go does not use more threads than its quota. |

### Kubernetes & Deployment

| Term | Explanation |
|---|---|
| **Pod** | The smallest unit in Kubernetes, containing one or more containers. |
| **Liveness probe** | Checks whether the app is still alive. If it fails, the pod is restarted (`/healthz/live`). |
| **Readiness probe** | Checks whether the app is ready to receive traffic. If it fails, the pod is removed from the load balancer (`/healthz/ready`). |
| **Startup probe** | A check for slow-starting apps, so they are not killed before they are ready. |
| **Non-blocking probe** | Dependency checks run in the background, so the probe endpoint still responds quickly. |
| **`preStop` hook** | A command run before a container is stopped, for example to leave a short delay so traffic can be rerouted. |
| **SIGTERM / SIGINT** | OS signals asking a process to stop gracefully. SIGTERM is sent by Kubernetes, SIGINT comes from Ctrl+C. |
| **Graceful shutdown** | Shutting down a server cleanly: finishing in-flight requests and closing connections instead of dying immediately. |
| **Phased shutdown** | A staged, ordered graceful shutdown: flip readiness to failing, drain traffic (*ingress drain*), close listeners, finish workers, then close client connections (DB, Redis). |
| **Ingress** | The entry point for external traffic into services inside Kubernetes. |
| **Rolling update (zero-downtime)** | Replacing old-version pods with new-version pods gradually, so the service is never down. |
| **Deployment** | A Kubernetes object that manages the number of pods and their update process. |
| **Job** | A Kubernetes object for one-off tasks, for example a DB migration before deployment. |
| **ConfigMap** | A Kubernetes object for storing non-secret configuration. |
| **NetworkPolicy (NetPol)** | Firewall rules between pods. *Microsegmentation* means limiting communication to only what is actually needed. |
| **HPA (Horizontal Pod Autoscaler)** | Automatically adds or removes pods based on CPU, memory, or other metrics. |
| **PDB (Pod Disruption Budget)** | Guarantees a minimum number of pods stay alive during maintenance or node drains. |
| **Security context** | Container security settings, for example running as non-root and a read-only filesystem. |
| **Hardening** | Tightening security configuration to reduce the attack surface. |
| **Multi-stage build (Docker)** | A Docker build in stages: the first compiles, and the final stage carries only the binary. The resulting image is small and safer. |
| **Non-root user** | The container runs as a regular user (`appuser:10001`), not root. |
| **Consul / Vault / ESO** | Consul is a centralized configuration store, Vault is a secrets store, and ESO (External Secrets Operator) syncs external secrets into Kubernetes. See `docs/configuration_management_migration_guide.md`. |

### Security

| Term | Explanation |
|---|---|
| **OWASP** | An organization that publishes web application security standards. The security headers here follow its recommendations. |
| **CORS** | Rules that determine which domains may call the API from a browser. |
| **HSTS** | A header that forces browsers to always use HTTPS. |
| **CSP (Content Security Policy)** | A header that restricts the sources of scripts and content that may be loaded, to prevent XSS. |
| **`X-Frame-Options`** | A header that prevents the page from being embedded in an `<iframe>`, to counter *clickjacking*. |
| **`X-Content-Type-Options`** | A header that stops browsers from guessing content types themselves (MIME sniffing). |
| **Panic recovery** | Middleware that catches a *panic* (a Go runtime crash) so a single bad request does not take down the whole server. |

### Testing & Build

| Term | Explanation |
|---|---|
| **Testcontainers** | A library that runs real Docker containers (PostgreSQL, Redis) during tests, so integration tests run in an environment close to production. |
| **Integration test** | A test that checks several components working together with real dependencies, unlike a unit test, which isolates them. |
| **End-to-end** | Testing the complete flow from start to finish. |
| **In-memory adapter** | A fake implementation that stores data in memory, used for tests without a database. |
| **Build tag (`examples`)** | A marker in a Go file (`//go:build examples`) that makes the file compile only when the tag is enabled. Example code stays out of production builds. |
| **Adapter** | A concrete implementation of an interface, for example a PostgreSQL adapter or an in-memory adapter for the outbox. |
| **Skeleton / Template** | A ready-made project scaffold that is copied as the starting point for a new project. |
