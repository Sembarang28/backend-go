# Base Project Golang Clean Architecture (Production & Kubernetes Ready)

Base backend Golang menggunakan Clean Architecture, Gin HTTP & gRPC delivery, didesain untuk menangani beban transaksi tinggi (high-traffic), testable, dan siap untuk deployment Kubernetes (K8s).

---

## 1. Struktur Direktori Utama

Mengikuti Standard Go Project Layout yang modular:

```plaintext
backend-go/
├── .github/
│   └── workflows/
│       └── ci.yml                 # Script CI/CD GitHub Actions (Lint, Test, Build)
├── cmd/
│   └── api/
│       └── main.go                # Entry point aplikasi (DI, HTTP & gRPC server, Phased Shutdown)
├── deployments/
│   ├── docker/
│   │   └── Dockerfile             # Multi-stage hardened Dockerfile (Non-root 10001, Cache mounts)
│   └── k8s/
│       ├── configmap.yaml         # Environment config
│       ├── deployment.yaml        # K8s Deployment (Probes, preStop hook, SecurityContext)
│       ├── hpa.yaml               # Horizontal Pod Autoscaler (CPU, Memory, Prometheus Metrics)
│       ├── pdb.yaml               # PodDisruptionBudget (Zero-downtime rolling updates)
│       └── service.yaml           # Service networking (HTTP 8080 & gRPC 50051)
├── docs/
│   └── project.md                 # Dokumentasi arsitektur proyek
├── internal/
│   ├── config/                    # Loader environment variables (.env)
│   ├── domain/                    # Entities, custom errors, dan interfaces kontrak
│   ├── events/                    # Publisher message broker (NATS / Kafka)
│   ├── handler/
│   │   ├── http/                  # HTTP Delivery Layer (Gin REST controllers & routing)
│   │   └── grpc/                  # gRPC Delivery Layer (Service server adapters)
│   ├── pkg/
│   │   ├── database/              # Read/Write Splitting & Multi-Tenant DB Router
│   │   │   └── migration/         # Programmatic DDL Migration Engine (go:embed SQL)
│   │   ├── health/                # Non-blocking K8s Liveness & Readiness Probes
│   │   ├── idempotency/           # Universal Idempotency Engine (HTTP & gRPC middleware)
│   │   ├── metrics/               # Prometheus RED Metrics, Outbox & DB Pool Collector
│   │   ├── middleware/            # Context Timeout, Request ID, Slog Logger
│   │   ├── outbox/                # Partitioned Outbox Worker, Distributed Lock (Redis), DLQ
│   │   ├── resilience/            # Distributed Sliding Window Rate Limiter & sony/gobreaker CB
│   │   ├── response/              # Standardized JSON response helpers
│   │   ├── shutdown/              # Phased Graceful Shutdown Engine (SIGTERM/SIGINT)
│   │   └── telemetry/             # W3C TraceContext Propagator & slog TraceHandler
│   ├── repository/                # Data Access Layer (Redis cache & DB persistence)
│   └── usecase/                   # Business Logic Layer
├── proto/
│   └── user/v1/user.proto         # Protobuf contract definitions
├── Makefile                       # Automasi build, test, lint, dan scaffolding
├── go.mod                         # Go module definition
└── go.sum
```

---

## 2. Arsitektur & Enterprise Modules

Setiap layer bergantung pada **Interface** (Dependency Inversion), memungkinkan unit testing menyeluruh tanpa ketergantungan langsung ke external dependencies.

### A. Advanced Outbox Worker & Distributed Locking (`internal/pkg/outbox`)
- **Distributed Locking (`lock.go`)**: Redlock/SETNX via Redis dengan safe unlock menggunakan Lua script agar aman di-deploy pada multiple pod replika di Kubernetes tanpa race condition.
- **Partitioned Batching (`worker.go`)**: Multi-partition worker pool untuk memproses event outbox secara paralel.
- **Dead Letter Queue (DLQ)**: Otomatis memindahkan event yang gagal setelah melebihi `MaxRetries` ke storage DLQ dengan exponential backoff.
- **PostgreSQL Persistence (`postgres_repository.go`)**: Implementasi `Repository` memakai `database/sql`; simpan event melalui transaksi bisnis yang sama. Repository interface tetap mendukung adapter database lain. Lihat [panduan outbox](./outbox_guide.md).

### B. Idempotency Engine (`internal/pkg/idempotency`)
- **Universal Middleware (`middleware.go` & `grpc.go`)**: Mencegah double-spend / duplikasi request HTTP (`X-Idempotency-Key`) dan gRPC metadata (`x-idempotency-key`).
- **Concurrent In-flight Lock & Response Cache**: Request yang sedang berjalan berstatus `STARTED` (menghasilkan status `409 Conflict` atau gRPC `codes.Aborted`), sedangkan request yang sudah selesai berstatus `COMPLETED` akan me-replay cached response secara instan.

### C. Distributed Tracing & Structured Logging (`internal/pkg/telemetry`)
- **W3C TraceContext (`trace.go`)**: Propagasi otomatis header `traceparent` melintasi HTTP Request -> gRPC -> Message Broker Headers (NATS/Kafka) -> Worker.
- **Auto Logger Injection (`slog_handler.go`)**: Menambahkan field `trace_id` dan `span_id` secara otomatis pada structured log (`log/slog`).

### D. Resiliency: Rate Limiting & Circuit Breaker (`internal/pkg/resilience`)
- **Distributed Sliding Window Rate Limiter (`ratelimit.go`)**: Membatasi rate request per tenant (`X-Tenant-ID`), user (`X-User-ID`), atau IP menggunakan Redis Sorted Sets & atomic Lua script.
- **Circuit Breaker (`circuitbreaker.go`)**: Terintegrasi dengan `github.com/sony/gobreaker/v2` (`CLOSED`, `OPEN`, `HALF_OPEN`) untuk melindungi downstream HTTP dan gRPC client dengan fallback handler.

### E. Database Read/Write Splitting & Multi-Tenancy (`internal/pkg/database`)
- **Read/Write Splitting (`DBGroup`)**: Rute query `Write()` ke Primary DB dan `Read()` secara round-robin ke Replica DBs.
- **Dynamic Tenant Router (`DynamicTenantRouter`)**: Resolusi dinamis database pool per tenant berdasarkan `TenantID` di dalam `context.Context`.
- **Embedded Migrations (`migration/`)**: Eksekusi DDL database secara terprogram menggunakan `//go:embed sql/*.sql`.

### F. K8s Health Probes, Metrics & Phased Graceful Shutdown
- **Health Probes (`internal/pkg/health`)**: Endpoint `/healthz/live` dan `/healthz/ready` non-blocking memeriksa status Redis, NATS, Primary DB, dan Replica DB.
- **Prometheus Metrics (`internal/pkg/metrics`)**: Endpoint `/metrics` mengekspos RED metrics, Outbox queue stats, dan DB connection pool stats.
- **Phased Graceful Shutdown (`internal/pkg/shutdown`)**: Menangani `SIGTERM`/`SIGINT` secara bertahap:
  1. Set readiness probe ke Unhealthy (drain ingress).
  2. Stop listener HTTP & gRPC (tuntaskan in-flight requests).
  3. Stop background Outbox Worker.
  4. Tutup koneksi DB, Redis, dan Broker.

---

## 3. Alur Request End-to-End

```text
[Client]
   │
   │ 1. Request (Headers: X-Idempotency-Key, traceparent, X-Tenant-ID)
   ▼
[HTTP / gRPC Middleware Pipeline]
   ├── telemetry.HTTPMiddleware() ──> Injeksi W3C traceparent ke context
   ├── metrics.HTTPMiddleware() ──> Record RED metrics (Rate, Errors, Duration)
   ├── middleware.TimeoutMiddleware() ──> Set context deadline
   ├── resilience.HTTPRateLimitMiddleware() ──> Sliding window rate check via Redis
   └── idempotency.HTTPMiddleware() ──> Atomic SETNX idempotency key di Redis
   │
   ▼
[Delivery Layer: Handler HTTP / gRPC]
   └── handler.GetByID() ──> Parsing request DTO & validasi
   │
   ▼
[Business Logic: Usecase Layer]
   ├── database.TenantIDFromContext(ctx) ──> Resolusi DB tenant
   ├── dbGroup.Write().ExecContext() ──> Mutasi data di Primary DB
   └── outboxRepo.Save() ──> Simpan event outbox + traceparent header
   │
   ▼
[Background Outbox Worker Daemon]
   ├── 1. Acquire Redis Distributed Lock partition P
   ├── 2. Fetch pending batch
   ├── 3. Publish ke NATS/Kafka broker + inject traceparent
   └── 4. Success -> Mark PUBLISHED; Fail Max -> Move to DLQ
```

---

## 4. Perintah Operasional & Pengujian

### Menjalankan Unit Tests
```bash
go test -v -cover ./...
```

### Menjalankan Server Lokal
```bash
go run cmd/api/main.go
```

### Build Binary
```bash
go build -o bin/api cmd/api/main.go
```