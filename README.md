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
│   ├── outbox_guide.md            # PostgreSQL persistence, transactions, worker setup, and adapter extensibility
│   ├── configuration_management_migration_guide.md # Centralized Config (Consul/Vault/ESO) migration guide
│   ├── project.md                  # Comprehensive architecture documentation
│   └── proto_migration_guide.md    # Centralized Protobuf migration guide
├── internal/
│   ├── config/                     # Configuration loader (.env & system environment)
│   ├── domain/                     # Domain entities, error types, repository/usecase interfaces
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
│   └── usecase/                    # Business Logic Layer
├── proto/
│   └── user/v1/user.proto          # Protobuf service definitions
├── tests/
│   └── integration/                # End-to-End Testcontainers tests (PostgreSQL + Redis)
├── Makefile
├── go.mod
└── README.md
```

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
- **Read/Write Splitting (`DBGroup`)**: Automatically routes mutation queries (`Write()`) to Primary DB and query operations (`Read()`) across read-replica pools using round-robin distribution.
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
- **Immutable Audit Trail (`internal/pkg/audit`)**: Captures mutation events with actor, before/after states, IP, user-agent, and trace IDs.
- **Outbox Retention Cleaner (`internal/pkg/outbox/cleaner.go`)**: Distributed locked background worker that purges processed records to prevent table bloat.

### 8. High Performance & Testability Patterns
- **Singleflight Cache Stampede Protection (`internal/pkg/cache`)**: Utilizes `golang.org/x/sync/singleflight` to suppress redundant database queries during concurrent cache misses on high-traffic keys.
- **Self-Healing Reconciliation Engine (`internal/pkg/reconciliation`)**: Background distributed-locked reconciliation daemon to resolve stuck/pending transactions with external providers.
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
  - Contains Gin REST controllers and route registration (e.g. `RegisterUserRoutes`).
- **gRPC Handlers (`internal/handler/grpc`)**:
  - Contains gRPC server implementations exposing usecases over Protobuf/gRPC (e.g. `UserGRPCHandler`).

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
APP_ENV=development go run cmd/api/main.go
```
The server will run on `http://127.0.0.1:8080` with pprof at `http://127.0.0.1:6060/debug/pprof/`.

### 2. Run the Server in Production Mode
```bash
APP_ENV=production go run cmd/api/main.go
```

### 3. Run Unit & Integration Tests
```bash
# Run unit tests
go test -v -cover ./...

# Run Testcontainers integration tests (requires Docker)
make test-integration
```

### 4. Build the Binary
```bash
go build -o bin/api cmd/api/main.go
./bin/api
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
