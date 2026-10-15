# Rekomendasi Struktur Folder — Clean Architecture

> Konteks: skeleton microservice Go (HTTP + gRPC, PostgreSQL, Redis, message queue Redis Streams, transactional outbox, idempotency).
> Referensi utama: Robert C. Martin (Uncle Bob), *Clean Architecture: A Craftsman's Guide to Software Structure and Design* (2017): Bagian III (prinsip SOLID), Bagian IV (prinsip komponen), Bagian V (arsitektur), serta artikel *The Clean Architecture* (2012).
> Dokumen ini melengkapi [review.md](review.md), terutama bagian 10.

---

## 1. Prinsip yang Dipakai

### 1.1 Empat lingkaran Uncle Bob → paket Go

```text
        ┌───────────────────────────────────────────────────────────────┐
        │  4. FRAMEWORKS & DRIVERS                                      │
        │     gin, grpc-go, pgx, go-redis, Redis Streams, OTel, K8s     │
        │     → internal/platform/*, internal/app, cmd/*                │
        │   ┌───────────────────────────────────────────────────────┐   │
        │   │  3. INTERFACE ADAPTERS                                │   │
        │   │     controller, presenter, gateway, repository impl   │   │
        │   │     → internal/modules/<m>/adapter/{inbound,outbound} │   │
        │   │   ┌───────────────────────────────────────────────┐   │   │
        │   │   │  2. USE CASES (Application Business Rules)    │   │   │
        │   │   │     interactor, input/output port, DTO        │   │   │
        │   │   │     → internal/modules/<m>/usecase            │   │   │
        │   │   │   ┌───────────────────────────────────────┐   │   │   │
        │   │   │   │  1. ENTITIES (Enterprise Rules)       │   │   │   │
        │   │   │   │     entity, value object, domain      │   │   │   │
        │   │   │   │     event, domain error               │   │   │   │
        │   │   │   │     → internal/modules/<m>/domain     │   │   │   │
        │   │   │   └───────────────────────────────────────┘   │   │   │
        │   │   └───────────────────────────────────────────────┘   │   │
        │   └───────────────────────────────────────────────────────┘   │
        └───────────────────────────────────────────────────────────────┘
                      Dependency Rule: import HANYA boleh mengarah ke dalam
```

### 1.2 Aturan yang diterapkan

| Prinsip (Uncle Bob) | Penerapan di Go |
| --- | --- |
| **Dependency Rule**: dependensi kode sumber hanya mengarah ke dalam. | `domain` tidak meng-import apa pun dari proyek. `usecase` hanya meng-import `domain` dan `kernel`. Adapter boleh meng-import `usecase` dan `domain`. `platform` tidak boleh meng-import `modules`. Ditegakkan oleh linter (bagian 7). |
| **Dependency Inversion (DIP)**: lingkaran dalam mendefinisikan interface, lingkaran luar mengimplementasikannya. | Interface (**port**) didefinisikan di `usecase/port`, sesuai idiom Go *"accept interfaces, return structs"*: interface dimiliki oleh **pemakai**, bukan oleh implementor. |
| **Screaming Architecture**: struktur harus "meneriakkan" domain, bukan framework. | Level atas `internal/modules/` berisi `order/`, `payment/`, bukan `handler/`, `repository/`, `usecase/`. |
| **Use Case sebagai pusat**: satu interactor untuk satu kasus penggunaan. | Satu file per use case: `create_order.go`, `cancel_order.go`. Input dan output DTO diletakkan di file yang sama. |
| **Data melintasi batas sebagai struktur sederhana**. | Tipe `gin.Context`, `*sql.Tx`, pesan proto, dan `redis.XMessage` **tidak boleh** masuk ke `usecase`/`domain`. Adapter mengonversinya menjadi DTO usecase. |
| **Database, Web, dan Framework adalah detail**. | Postgres, Redis, gin, dan gRPC hanyalah *plugin* di `adapter/outbound` dan `platform`. Use case bisa dites tanpa satu pun dari itu. |
| **Main Component** adalah *plugin* paling kotor yang merakit semuanya. | `internal/app` (composition root) dan `cmd/*`. Hanya di sini semua lapisan bertemu. |
| **Humble Object**: pisahkan bagian yang sulit dites dari logika. | Controller dan consumer hanya parsing → panggil use case → present. Tidak ada logika bisnis di dalamnya. |
| **Common Closure / Common Reuse (CCP/CRP)**: kelompokkan yang berubah bersama. | *Package by component*: semua lapisan satu bounded context berada di bawah `modules/<m>/`. Menambah fitur order tidak menyentuh folder lain. |

### 1.3 Kompromi pragmatis untuk Go

Uncle Bob sendiri menyebut lingkaran itu **skematik**, bukan aturan jumlah folder. Supaya tidak over-engineering:

- **Tidak ada interface untuk input port** per use case kecuali benar-benar dibutuhkan (mis. untuk di-mock di test controller). Controller boleh bergantung pada struct interactor yang konkret, atau pada interface kecil yang didefinisikan **di paket controller**.
- **Presenter digabung ke controller** untuk REST/gRPC (controller mengubah output DTO menjadi JSON/proto). Uncle Bob memisahkannya agar use case tidak tahu format output, dan tujuan itu sudah tercapai selama use case hanya mengembalikan DTO.
- **Tag `json` di entity dilarang**, tapi tag di DTO adapter boleh. Ini memastikan perubahan format API tidak menyentuh domain.
- **Transaksi tetap lewat port**, dibawa di `context.Context`, supaya use case bisa menentukan batas transaksi tanpa meng-import `database/sql`.
- **Cross-cutting concern** (idempotency di edge, rate limit, auth, tracing) berada di `platform` sebagai middleware/interceptor, sedangkan **aturan idempotency bisnis** tetap di use case dan domain.

---

## 2. Prinsip SOLID

Clean Architecture dibangun di atas SOLID. Uncle Bob membahasnya di Bagian III (*Design Principles*) dari buku *Clean Architecture*. SOLID berlaku di level **kode** (struct, interface, fungsi), sedangkan lingkaran di bagian 1 berlaku di level **komponen/paket**. Struktur folder di bagian 3 hanya bekerja jika kode di dalamnya juga SOLID.

Setiap prinsip di bawah dijelaskan dengan pola: arti menurut Uncle Bob, penerapannya di Go, pelanggaran yang **sudah ada** di skeleton ini, dan perbaikannya.

### 2.1 S — Single Responsibility Principle

> *"A module should be responsible to one, and only one, actor."*

Uncle Bob sendiri menegaskan SRP **bukan** "satu fungsi melakukan satu hal". SRP berarti satu modul hanya punya **satu sumber perubahan**: satu aktor/stakeholder yang bisa meminta modul itu berubah.

**Penerapan di Go:**

| Unit | Aktor (alasan berubah) |
| --- | --- |
| `domain/order.go` | Aturan bisnis order (product owner) |
| `usecase/create_order.go` | Alur "buat order" (satu use case = satu file) |
| `adapter/inbound/http/dto.go` | Kontrak API publik (konsumen API) |
| `adapter/outbound/postgres/order_repository.go` | Skema database (DBA/tim data) |
| `platform/idempotency` | Kebijakan retry di edge (tim platform) |
| `internal/app` | Cara merakit dan men-deploy service (tim ops) |

**Pelanggaran saat ini:**

- [cmd/api/main.go](cmd/api/main.go): satu fungsi `main()` sepanjang 300+ baris mengurus logging, koneksi DB/Redis/NATS, router, gRPC, metrics collector, dan shutdown. Setiap perubahan infra apa pun menyentuh file yang sama.
- [idempotency/middleware.go:41-147](internal/pkg/idempotency/middleware.go#L41-L147): satu closure membaca body, menghitung fingerprint, mengambil lock, merekam response, memutuskan kebijakan cache, dan menyimpan hasil.
- [usecase/example_order_usecase.go:51-67](internal/usecase/example_order_usecase.go#L51-L67): use case juga menentukan format JSON event (aktor: konsumen event), di samping alur bisnis.

**Perbaikan:** pecah `main()` ke `internal/app/{infra,http,grpc,worker}.go`. Pecah middleware idempotency menjadi `Fingerprinter`, `Store`, `ResponseRecorder`, dan `CachePolicy`. Pindahkan serialisasi event ke adapter outbox.

### 2.2 O — Open/Closed Principle

> *"A software artifact should be open for extension but closed for modification."*

Di level arsitektur, Uncle Bob menyebut OCP sebagai **alasan utama** keberadaan Dependency Rule: komponen tingkat tinggi (use case) dilindungi dari perubahan komponen tingkat rendah (DB, web, broker).

**Penerapan di Go:**

- **Menambah delivery mechanism** (HTTP → gRPC → consumer Redis Stream) cukup dengan menambah adapter di `adapter/inbound/`. Use case tidak disentuh.
- **Mengganti broker** (NATS → Redis Streams) cukup dengan menambah implementasi `messaging.Publisher` baru dan mengganti wiring di `app/`.
- **Decorator** untuk cross-cutting concern di sisi outbound:

```go
// adapter/outbound/redis/cached_order_repository.go
type CachedOrderRepository struct {
    next  port.OrderRepository // repository Postgres asli
    cache *cache.Manager
}

func (r *CachedOrderRepository) GetByID(ctx context.Context, id string, c port.Consistency) (*domain.Order, error) {
    if c == port.Strong {
        return r.next.GetByID(ctx, id, c) // bypass cache
    }
    return cache.GetOrSet(ctx, r.cache, "order:"+id, ttl, func(ctx context.Context) (*domain.Order, error) {
        return r.next.GetByID(ctx, id, c)
    })
}
// Create/Update diteruskan ke next + invalidate cache.
```

Menambah cache, metrics, atau tracing **tidak mengubah** repository Postgres maupun use case. Cukup bungkus di `app/` atau `module.go`.

**Pelanggaran saat ini:**

- [idempotency/middleware.go:83-87](internal/pkg/idempotency/middleware.go#L83-L87): type assertion `storage.(FingerprintStorage)` untuk memilih perilaku. Setiap kemampuan baru akan menambah cabang `if` di middleware. Ini *code smell* OCP yang klasik.
- [example_order_repository.go:43](internal/repository/example_order_repository.go#L43): cache dibangun **di dalam** repository, sehingga perilaku cache tidak bisa diganti atau dimatikan tanpa mengubah repository.

**Perbaikan:** satukan interface `Store` (fingerprint selalu menjadi bagian kontrak). Ubah cache menjadi decorator seperti contoh di atas.

### 2.3 L — Liskov Substitution Principle

> *"To build software systems from interchangeable parts, those parts must adhere to a contract that allows those parts to be substituted one for another."*

Di Go, LSP berarti **setiap implementasi sebuah interface harus berperilaku sama sesuai kontraknya**, bukan hanya punya method dengan signature yang sama. Ini krusial karena skeleton ini punya banyak pasangan implementasi (memory vs Redis, Postgres vs memory, NATS vs Redis Streams).

**Pelanggaran saat ini:**

| Interface | Implementasi A | Implementasi B | Masalah |
| --- | --- | --- | --- |
| `idempotency.Storage` | `RedisStorage` dengan client nil: `TryLock` mengembalikan `acquired=true` ([idempotency.go:70-72](internal/pkg/idempotency/idempotency.go#L70-L72)) | `MemoryStorage`: benar-benar mengunci | Mengganti satu dengan yang lain mengubah perilaku dari "terkunci" menjadi "tidak pernah terkunci". |
| `idempotency.Storage` | `RedisStorage`: `SaveResponse` me-*marshal* record | `MemoryStorage`: menyimpan **pointer** record yang sama | Caller yang mengubah record setelah save akan mengubah data tersimpan, tapi hanya di versi memory. |
| `outbox.DistributedLock` | `Acquire` dengan client nil → error | `Release`/`Extend` dengan client nil → `nil` (sukses) ([lock.go:53-78](internal/pkg/outbox/lock.go#L53-L78)) | Satu implementasi, kontrak tidak konsisten antar method. |

Akibatnya, test yang lulus dengan implementasi memory **tidak membuktikan** apa pun tentang perilaku production.

**Perbaikan: contract test.** Tulis satu suite test per port, lalu jalankan untuk **setiap** implementasi:

```go
// platform/idempotency/storetest/contract.go
func RunStoreContract(t *testing.T, newStore func(t *testing.T) idempotency.Store) {
    t.Run("second acquire with same key is rejected", func(t *testing.T) { ... })
    t.Run("complete by non-owner returns ErrLeaseLost", func(t *testing.T) { ... })
    t.Run("release by non-owner keeps the lease", func(t *testing.T) { ... })
    t.Run("stored record is not aliased", func(t *testing.T) { ... })
}

// memory_test.go
func TestMemoryStore(t *testing.T) { storetest.RunStoreContract(t, newMemory) }
// redis_test.go (build tag integration)
func TestRedisStore(t *testing.T)  { storetest.RunStoreContract(t, newRedisContainer) }
```

Pola yang sama berlaku untuk `port.OrderRepository` (fake vs Postgres) dan `messaging.Publisher`/`Consumer` (memory vs Redis Streams). Dokumentasikan kontraknya di komentar interface (error apa saja yang dikembalikan, perilaku saat key tidak ada, idempotent atau tidak).

### 2.4 I — Interface Segregation Principle

> *"Don't depend on things you don't use."*

Di Go, ini selaras dengan pepatah *"The bigger the interface, the weaker the abstraction"* (Rob Pike). Interface didefinisikan **oleh pemakai** dan berisi hanya method yang dipakai pemakai itu.

**Pelanggaran saat ini:** [outbox/repository.go](internal/pkg/outbox/repository.go) memiliki 8 method, padahal tiap pemakai hanya butuh sebagian:

| Pemakai | Method yang benar-benar dipakai |
| --- | --- |
| Repository order (produsen event) | `Save` |
| `outbox.Worker` | `FetchPendingBatch`, `MarkProcessing`, `MarkPublished`, `MarkFailed`, `MoveToDLQ` |
| `outbox.Cleaner` | `DeletePublishedBefore` |
| *(tidak ada)* | `SaveDLQ` |

Akibatnya fake untuk test `Cleaner` tetap harus mengimplementasikan 8 method, dan perubahan signature di method worker memaksa semua implementasi ikut berubah.

**Perbaikan:**

```go
package outbox

type Writer interface {               // dipakai adapter outbound modul (produsen)
    Enqueue(ctx context.Context, events ...Event) error
}

type Claimer interface {              // dipakai Relay
    Claim(ctx context.Context, partition, limit int, lease time.Duration) ([]Event, error)
    MarkPublished(ctx context.Context, id, owner string) error
    MarkFailed(ctx context.Context, id, owner, reason string, next time.Time) error
    MoveToDLQ(ctx context.Context, id, owner, reason string) error
}

type Purger interface {               // dipakai Cleaner
    DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}

// PostgresStore boleh mengimplementasikan ketiganya — pemakai tetap hanya melihat yang dibutuhkan.
```

Hal yang sama berlaku untuk dependensi library besar. Adapter Redis Streams sebaiknya tidak menerima `redis.UniversalClient` (ratusan method) di level logika, tapi interface sempit:

```go
type streamClient interface {
    XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
    XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd
    XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
    XAutoClaim(ctx context.Context, a *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
}
```

Di lapisan use case, **satu port per kebutuhan**: `port.OrderRepository`, `port.EventOutbox`, `port.TxManager`, `port.PaymentGateway`. Jangan buat satu `port.Store` raksasa.

### 2.5 D — Dependency Inversion Principle

> *"The most flexible systems are those in which source code dependencies refer only to abstractions, not to concretions."*

DIP adalah mekanisme yang **membuat Dependency Rule mungkin**. Aliran kontrol berjalan dari use case ke database, tapi dependensi kode sumber dibalik: use case mendefinisikan `port.OrderRepository`, dan adapter Postgres yang bergantung padanya.

```text
Aliran kontrol :  HTTP handler ──► CreateOrder ──► PostgresOrderRepository ──► DB
Dependensi kode:  HTTP handler ──► CreateOrder ◄── PostgresOrderRepository
                                        │
                                        └── mendefinisikan port.OrderRepository
```

Semua objek konkret dibuat di **satu tempat**, yaitu composition root (`internal/app`). Uncle Bob menyebutnya *Main Component*.

**Pelanggaran saat ini:**

- [usecase/example_order_reconciliation.go](internal/usecase/example_order_reconciliation.go): use case meng-import `internal/pkg/reconciliation` (detail infra). Panahnya mengarah keluar.
- [example_order_repository.go:43](internal/repository/example_order_repository.go#L43): repository membuat dependensinya sendiri (`cache.NewManager(redisClient)`) alih-alih menerimanya.
- [usecase/example_order_usecase.go:48](internal/usecase/example_order_usecase.go#L48) memanggil `time.Now()` langsung, dan `newOrderID()` memakai `crypto/rand` langsung. Waktu dan ID adalah dependensi tersembunyi, sehingga use case sulit dites secara deterministik.
- [metrics.go:43](internal/pkg/metrics/metrics.go#L43) memakai state global `globalMetrics`, dan seluruh kode memakai `slog.Default()` global. Keduanya adalah dependensi konkret yang tersembunyi.

**Perbaikan:**

- Inject semua dependensi lewat konstruktor: `NewCreateOrder(tx, repo, outbox, ids, clock)` (lihat contoh di bagian 5.3).
- `kernel/clock.Clock` dan `port.IDGenerator` menggantikan `time.Now()` dan `rand` langsung.
- Logger dan metrics di-inject (atau minimal diambil dari context), bukan dari variabel global.
- **Jangan pakai DI container berbasis refleksi.** Wiring manual di `internal/app` (atau `google/wire` yang berbasis code generation) sudah cukup dan lebih mudah dibaca.

### 2.6 Prinsip Komponen (pendamping SOLID di level paket)

Di Bagian IV bukunya, Uncle Bob menerjemahkan SOLID ke level komponen. Prinsip-prinsip ini menjelaskan **kenapa** struktur di bagian 3 berbentuk seperti itu:

| Prinsip | Arti singkat | Penerapan di struktur ini |
| --- | --- | --- |
| **REP** (Reuse/Release Equivalence) | Yang di-reuse bersama dirilis bersama. | `platform/*` adalah unit yang di-reuse oleh semua microservice turunan skeleton. Kelak bisa dipisah menjadi modul Go sendiri. |
| **CCP** (Common Closure) | Yang berubah bersama dikelompokkan bersama (SRP untuk komponen). | Semua lapisan satu bounded context ada di `modules/<m>/`. |
| **CRP** (Common Reuse) | Jangan memaksa pemakai bergantung pada yang tidak dipakai (ISP untuk komponen). | `platform/` dipecah kecil (`cache`, `outbox`, `messaging`, ...), bukan satu paket `infra` besar. |
| **ADP** (Acyclic Dependencies) | Tidak boleh ada siklus dependensi antar komponen. | Compiler Go sudah menolak import cycle. Aturan antar modul di bagian 4 mencegah siklus logis lewat `app/`. |
| **SDP** (Stable Dependencies) | Bergantunglah ke arah yang lebih stabil. | `domain` (paling stabil) ← `usecase` ← `adapter` ← `app` (paling sering berubah). |
| **SAP** (Stable Abstractions) | Komponen yang stabil harus abstrak. | `usecase/port` berisi interface (abstrak dan stabil). Implementasi konkretnya berada di `adapter` (tidak stabil, mudah diganti). |

### 2.7 Checklist SOLID untuk Code Review

- [ ] **S**: Apakah file atau struct ini punya lebih dari satu aktor yang bisa memintanya berubah?
- [ ] **O**: Apakah menambah fitur ini memaksa mengubah use case atau domain yang sudah ada, padahal seharusnya cukup menambah adapter atau decorator?
- [ ] **O**: Apakah ada `switch`/type assertion pada tipe implementasi untuk memilih perilaku?
- [ ] **L**: Apakah implementasi baru dari sebuah port lulus *contract test* yang sama dengan implementasi lain?
- [ ] **L**: Apakah implementasi diam-diam mengubah perilaku saat dependensinya nil atau mati (fail-open tersembunyi)?
- [ ] **I**: Apakah interface ini punya method yang tidak dipakai oleh pemakainya? Apakah interface didefinisikan di paket pemakai?
- [ ] **D**: Apakah ada `New...()` objek konkret, `time.Now()`, `rand`, atau variabel global di dalam `domain`/`usecase`?
- [ ] **D**: Apakah semua dependensi masuk lewat konstruktor dan dirakit di `internal/app`?

---

## 3. Struktur Folder Lengkap

```text
backend-go/
├── cmd/                                   # [L4] entrypoint saja — tipis, tanpa logika
│   ├── api/main.go                        #   HTTP + gRPC server
│   ├── worker/main.go                     #   outbox relay + Redis Stream consumer
│   └── migrate/main.go                    #   jalankan migrasi
│
├── api/                                   # KONTRAK PUBLIK (bukan kode Go)
│   ├── proto/
│   │   ├── buf.yaml
│   │   ├── buf.gen.yaml
│   │   └── order/v1/order_service.proto
│   ├── openapi/order.v1.yaml
│   └── events/order.v1.schema.json        #   skema payload event (kontrak MQ)
│
├── gen/                                   # kode hasil generate (buf/protoc) — JANGAN diedit
│   └── proto/order/v1/*.pb.go             #   hanya boleh di-import oleh adapter/inbound/grpc
│
├── internal/
│   ├── app/                               # [L4] MAIN COMPONENT / composition root
│   │   ├── app.go                         #   App{Run(ctx) error} + errgroup
│   │   ├── infra.go                       #   buka DB, Redis, OTel, dsb.
│   │   ├── http.go                        #   rakit router + middleware chain
│   │   ├── grpc.go                        #   rakit grpc.Server + interceptors + register service
│   │   └── worker.go                      #   rakit outbox relay + consumers
│   │
│   ├── config/                            #   load + Validate(); hanya dipakai app/ & cmd/
│   │   └── config.go
│   │
│   ├── kernel/                            # [L1] SHARED KERNEL — murni, tanpa dependensi eksternal
│   │   ├── apperr/                        #   kategori error: NotFound, Conflict, Invalid, ...
│   │   │   └── apperr.go
│   │   ├── money/                         #   value object lintas modul (opsional)
│   │   └── clock/                         #   interface Clock{Now()} (untuk test deterministik)
│   │
│   ├── modules/                           # SCREAMING ARCHITECTURE: satu folder = satu bounded context
│   │   └── order/
│   │       ├── domain/                    # [L1] ENTITIES — Enterprise Business Rules
│   │       │   ├── order.go               #   aggregate Order + invariant + transisi status
│   │       │   ├── order_status.go        #   state machine
│   │       │   ├── money.go               #   value object (bila tidak di kernel)
│   │       │   ├── events.go              #   OrderCreated, OrderPaid (domain event, struct murni)
│   │       │   └── errors.go              #   ErrOrderNotFound, ErrInvalidAmount, ...
│   │       │
│   │       ├── usecase/                   # [L2] USE CASES — Application Business Rules
│   │       │   ├── port/                  #   OUTPUT PORTS (interface, didefinisikan di sini)
│   │       │   │   ├── repository.go      #     OrderRepository
│   │       │   │   ├── tx.go              #     TxManager
│   │       │   │   ├── events.go          #     EventOutbox (enqueue domain event)
│   │       │   │   ├── cache.go           #     OrderReadCache (opsional)
│   │       │   │   └── gateway.go         #     PaymentGateway (service lain)
│   │       │   ├── create_order.go        #   interactor + CreateOrderInput/Output
│   │       │   ├── get_order.go
│   │       │   ├── cancel_order.go
│   │       │   ├── handle_payment_succeeded.go  # dipicu oleh event dari MQ
│   │       │   ├── reconcile_outbox.go    #   self-healing job (dipanggil scheduler)
│   │       │   └── create_order_test.go   #   test dengan fake port — tanpa DB/Redis
│   │       │
│   │       ├── adapter/                   # [L3] INTERFACE ADAPTERS
│   │       │   ├── inbound/               #   driving adapters (memanggil use case)
│   │       │   │   ├── http/
│   │       │   │   │   ├── handler.go     #     controller + presenter (gin ↔ DTO)
│   │       │   │   │   ├── dto.go         #     request/response JSON (tag json DI SINI)
│   │       │   │   │   └── routes.go      #     Register(r gin.IRouter, ...)
│   │       │   │   ├── grpc/
│   │       │   │   │   ├── server.go      #     implementasi orderv1.OrderServiceServer
│   │       │   │   │   └── mapper.go      #     proto ↔ DTO
│   │       │   │   ├── consumer/
│   │       │   │   │   └── payment_events.go  # Redis Stream message → use case
│   │       │   │   └── job/
│   │       │   │       └── reconcile.go   #     adaptasi reconciliation.Job → use case
│   │       │   │
│   │       │   └── outbound/              #   driven adapters (diimplementasikan untuk port)
│   │       │       ├── postgres/
│   │       │       │   ├── order_repository.go  # implement port.OrderRepository
│   │       │       │   ├── outbox.go      #     implement port.EventOutbox (tulis ke outbox_events)
│   │       │       │   ├── queries.sql    #     (jika memakai sqlc)
│   │       │       │   └── row.go         #     row struct ↔ domain.Order
│   │       │       ├── redis/
│   │       │       │   └── order_cache.go #     implement port.OrderReadCache
│   │       │       └── paymentclient/
│   │       │           └── grpc.go        #     implement port.PaymentGateway via grpcclient
│   │       │
│   │       └── module.go                  # konstruktor modul: New(deps) *Module{HTTP, GRPC, Consumers, Jobs}
│   │
│   └── platform/                          # [L4] FRAMEWORKS & DRIVERS — domain-agnostic, reusable
│       ├── httpserver/                    #   NewServer(cfg) + timeouts + middleware standar
│       │   └── middleware/                #   requestid, logger, recovery, cors, security, timeout
│       ├── grpcserver/                    #   NewServer(opts) + interceptors + health + reflection
│       ├── grpcclient/                    #   NewConn(target) + retry + breaker + otel + deadline
│       ├── database/
│       │   ├── postgres.go                #   pool, read/write split
│       │   ├── tx.go                      #   TxManager impl (tx di context) + retry 40001/40P01
│       │   └── migrate/                   #   runner migrasi (advisory lock)
│       ├── redisclient/                   #   single / sentinel / cluster + TLS + timeout
│       ├── cache/                         #   GetOrSet + singleflight
│       ├── messaging/
│       │   ├── messaging.go               #   Message, Handler, Publisher, Consumer (generik)
│       │   ├── redisstream/               #   XADD / XREADGROUP / XACK / XAUTOCLAIM / DLQ stream
│       │   └── inbox/                     #   dedupe consumer (tabel inbox_messages)
│       ├── outbox/                        #   relay: klaim lease → messaging.Publisher; cleaner
│       ├── idempotency/                   #   L1 edge: Store(lease/token), HTTP mw, gRPC interceptor
│       ├── reconciliation/                #   engine penjadwal job + lock
│       ├── resilience/                    #   circuit breaker, rate limiter
│       ├── auth/                          #   Principal di context, JWT/mTLS middleware & interceptor
│       ├── observability/                 #   OTel setup, slog handler, Prometheus
│       ├── health/
│       ├── shutdown/
│       └── audit/
│
├── migrations/                            # SQL (dipisah dari kode Go; di-embed oleh platform/database/migrate)
│   ├── platform/                          #   tabel infra: outbox, dlq, inbox, audit
│   │   ├── 000001_create_outbox.up.sql
│   │   └── 000001_create_outbox.down.sql
│   └── order/                             #   tabel milik modul order
│       └── 000001_create_orders.up.sql
│
├── tests/
│   ├── integration/                       #   testcontainers: postgres, redis
│   └── e2e/                               #   HTTP + gRPC melawan binary yang jalan
│
├── deployments/
│   ├── docker/Dockerfile
│   └── k8s/{base,overlays/{dev,staging,prod}}/   # kustomize
│
├── docs/
│   ├── adr/                               #   Architecture Decision Records
│   └── *.md
│
├── .golangci.yml                          #   termasuk depguard (penegak Dependency Rule)
├── .go-arch-lint.yml                      #   (opsional) penegak arsitektur yang lebih ketat
├── docker-compose.yml
├── Makefile
└── go.mod
```

### Kenapa `modules/<m>/{domain,usecase,adapter}` dan bukan `domain/`, `usecase/`, `handler/` di root?

Struktur sekarang (`internal/domain`, `internal/usecase`, `internal/handler`, `internal/repository`) adalah **package by layer**. Uncle Bob (bab 34, *The Missing Chapter*, oleh Simon Brown) menunjukkan bahwa pendekatan ini:

- tidak "meneriakkan" domain: dari luar yang terlihat hanya "ini aplikasi gin";
- membuat satu fitur tersebar di 4–5 folder;
- membuat semua tipe harus `public` agar bisa diakses lintas folder, sehingga batas antar modul tidak bisa ditegakkan compiler.

Dengan **package by component** + lapisan di dalamnya, menghapus atau memindahkan satu bounded context ke microservice lain cukup dengan memindahkan satu folder.

> **Satu microservice = satu atau beberapa modul.** Untuk microservice kecil, biasanya hanya ada satu modul. Struktur tetap sama supaya semua service turunan skeleton ini seragam.

---

## 4. Aturan Dependensi (yang boleh meng-import apa)

| Paket | Boleh meng-import | DILARANG meng-import |
| --- | --- | --- |
| `modules/*/domain` | stdlib, `kernel/*` | `usecase`, `adapter`, `platform`, `gen`, `config`, semua library eksternal (gin, grpc, sql, redis) |
| `modules/*/usecase` (+ `port`) | stdlib, `kernel/*`, `domain` milik modulnya | `adapter`, `platform`, `gen`, `config`, `database/sql`, gin, grpc, redis |
| `modules/*/adapter/inbound/*` | `usecase`, `domain`, `kernel`, `platform/*` (middleware, messaging, apperr mapping), `gen/*` (hanya grpc), gin, grpc | `adapter/outbound`, modul lain |
| `modules/*/adapter/outbound/*` | `usecase/port`, `domain`, `kernel`, `platform/*` (database, redisclient, cache, grpcclient), driver | `adapter/inbound`, modul lain |
| `modules/*/module.go` | semua sub-paket modulnya, `platform/*` | modul lain |
| `platform/*` | stdlib, library eksternal, `kernel/*`, sesama `platform` | **`modules/*`**, `app`, `config` |
| `kernel/*` | stdlib saja | semua yang lain |
| `app`, `cmd/*` | semua | — |

**Komunikasi antar modul** (jika satu service punya beberapa modul):

1. **Asinkron via event** (dianjurkan): modul A mempublikasi event lewat outbox, dan modul B mengonsumsinya lewat consumer.
2. **Sinkron**: modul B mendefinisikan port `port.OrderReader` di usecase-nya, lalu `app/` menyambungkannya ke interactor modul A lewat adapter kecil. Jangan pernah meng-import `modules/a/...` langsung dari `modules/b/...`.

---

## 5. Contoh Kode per Lapisan

### 5.1 [L1] Entity — `modules/order/domain/order.go`

```go
package domain

import (
    "errors"
    "time"
)

var (
    ErrOrderNotFound  = errors.New("order not found")
    ErrInvalidOrder   = errors.New("invalid order")
    ErrInvalidStatus  = errors.New("invalid order status transition")
)

type Status string

const (
    StatusPaymentPending Status = "PAYMENT_PENDING"
    StatusPaid           Status = "PAID"
    StatusCancelled      Status = "CANCELLED"
)

// Order adalah aggregate root. Tanpa tag json/db: format persistence & API adalah detail.
type Order struct {
    id          string
    customerID  string
    amount      Money
    status      Status
    createdAt   time.Time
    events      []Event // domain events yang belum dipublikasi
}

func NewOrder(id, customerID string, amount Money, now time.Time) (*Order, error) {
    if id == "" || customerID == "" || !amount.IsPositive() {
        return nil, ErrInvalidOrder
    }
    o := &Order{id: id, customerID: customerID, amount: amount, status: StatusPaymentPending, createdAt: now}
    o.record(OrderCreated{OrderID: id, CustomerID: customerID, Amount: amount, OccurredAt: now})
    return o, nil
}

func (o *Order) MarkPaid(now time.Time) error {
    if o.status != StatusPaymentPending {
        return ErrInvalidStatus
    }
    o.status = StatusPaid
    o.record(OrderPaid{OrderID: o.id, OccurredAt: now})
    return nil
}

func (o *Order) PullEvents() []Event { e := o.events; o.events = nil; return e }
func (o *Order) record(e Event)       { o.events = append(o.events, e) }

// Rehydrate dipakai repository untuk membangun ulang dari storage (tanpa memicu event).
func Rehydrate(id, customerID string, amount Money, status Status, createdAt time.Time) *Order {
    return &Order{id: id, customerID: customerID, amount: amount, status: status, createdAt: createdAt}
}

func (o *Order) ID() string         { return o.id }
func (o *Order) Status() Status     { return o.status }
// ... getter lain
```

### 5.2 [L2] Output Ports — `modules/order/usecase/port/*.go`

```go
package port

import (
    "context"
    "backend-go/internal/modules/order/domain"
)

type OrderRepository interface {
    // Create mengembalikan ErrIdempotencyHit berisi order lama jika key sudah dipakai
    // dengan fingerprint yang sama (idempotency L2 — sumber kebenaran).
    Create(ctx context.Context, o *domain.Order, idemKey, fingerprint string) error
    FindByIdempotencyKey(ctx context.Context, idemKey string) (*domain.Order, string, error)
    GetByID(ctx context.Context, id string, consistency Consistency) (*domain.Order, error)
    Update(ctx context.Context, o *domain.Order) error
}

type Consistency int

const (
    EventuallyConsistent Consistency = iota // boleh cache / replica
    Strong                                  // wajib primary, tanpa cache
)

// TxManager: batas transaksi ditentukan use case; tx dibawa di ctx oleh implementasi.
type TxManager interface {
    WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// EventOutbox menulis domain event ke outbox DALAM transaksi yang sama.
type EventOutbox interface {
    Enqueue(ctx context.Context, aggregateID string, events ...domain.Event) error
}

type IDGenerator interface{ NewID() string }
```

### 5.3 [L2] Interactor — `modules/order/usecase/create_order.go`

```go
package usecase

import (
    "context"
    "errors"

    "backend-go/internal/kernel/clock"
    "backend-go/internal/modules/order/domain"
    "backend-go/internal/modules/order/usecase/port"
)

var ErrIdempotencyKeyReused = errors.New("idempotency key reused with different request")

// Input/Output DTO: struktur data sederhana yang melintasi batas (tanpa tag json).
type CreateOrderInput struct {
    IdempotencyKey string
    CustomerID     string
    AmountMinor    int64
    Currency       string
}

type CreateOrderOutput struct {
    OrderID  string
    Status   string
    Replayed bool
}

type CreateOrder struct {
    tx     port.TxManager
    repo   port.OrderRepository
    outbox port.EventOutbox
    ids    port.IDGenerator
    clock  clock.Clock
}

func NewCreateOrder(tx port.TxManager, repo port.OrderRepository, outbox port.EventOutbox,
    ids port.IDGenerator, clk clock.Clock) *CreateOrder {
    return &CreateOrder{tx: tx, repo: repo, outbox: outbox, ids: ids, clock: clk}
}

func (uc *CreateOrder) Execute(ctx context.Context, in CreateOrderInput) (CreateOrderOutput, error) {
    amount, err := domain.NewMoney(in.AmountMinor, in.Currency)
    if err != nil {
        return CreateOrderOutput{}, err
    }
    fp := fingerprint(in) // hash deterministik dari input bisnis

    // Idempotency L2: aturan bisnis "key sama → hasil sama" milik use case.
    if existing, existingFP, err := uc.repo.FindByIdempotencyKey(ctx, in.IdempotencyKey); err == nil {
        if existingFP != fp {
            return CreateOrderOutput{}, ErrIdempotencyKeyReused
        }
        return CreateOrderOutput{OrderID: existing.ID(), Status: string(existing.Status()), Replayed: true}, nil
    } else if !errors.Is(err, domain.ErrOrderNotFound) {
        return CreateOrderOutput{}, err
    }

    order, err := domain.NewOrder(uc.ids.NewID(), in.CustomerID, amount, uc.clock.Now())
    if err != nil {
        return CreateOrderOutput{}, err
    }

    err = uc.tx.WithinTx(ctx, func(ctx context.Context) error {
        if err := uc.repo.Create(ctx, order, in.IdempotencyKey, fp); err != nil {
            return err // UNIQUE(idempotency_key) tetap jadi pengaman race
        }
        return uc.outbox.Enqueue(ctx, order.ID(), order.PullEvents()...)
    })
    if err != nil {
        return CreateOrderOutput{}, err
    }
    return CreateOrderOutput{OrderID: order.ID(), Status: string(order.Status())}, nil
}
```

Perhatikan apa yang **tidak ada**: `gin`, `sql`, `redis`, `json`, `proto`. Use case ini bisa dites dengan fake in-memory dalam hitungan milidetik.

### 5.4 [L3] Inbound adapter (controller + presenter) — `adapter/inbound/http/handler.go`

```go
package http

import (
    nethttp "net/http"

    "backend-go/internal/modules/order/usecase"
    "backend-go/internal/platform/httpserver/respond"

    "github.com/gin-gonic/gin"
)

// Interface kecil milik controller (idiom Go: didefinisikan oleh pemakai).
type orderCreator interface {
    Execute(ctx context.Context, in usecase.CreateOrderInput) (usecase.CreateOrderOutput, error)
}

type Handler struct{ create orderCreator }

type createOrderRequest struct { // DTO transport — tag json hanya di lapisan ini
    CustomerID  string `json:"customer_id" binding:"required"`
    AmountMinor int64  `json:"amount_minor" binding:"required"`
    Currency    string `json:"currency" binding:"required,len=3"`
}

func (h *Handler) Create(c *gin.Context) {
    var req createOrderRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        respond.Invalid(c, err)
        return
    }
    out, err := h.create.Execute(c.Request.Context(), usecase.CreateOrderInput{
        IdempotencyKey: c.GetHeader("Idempotency-Key"),
        CustomerID:     req.CustomerID,
        AmountMinor:    req.AmountMinor,
        Currency:       req.Currency,
    })
    if err != nil {
        respond.Error(c, err) // map domain/usecase error → HTTP status (via kernel/apperr)
        return
    }
    status := nethttp.StatusCreated
    if out.Replayed {
        status = nethttp.StatusOK
    }
    respond.JSON(c, status, gin.H{"id": out.OrderID, "status": out.Status})
}
```

Adapter gRPC (`adapter/inbound/grpc/server.go`) memanggil **interactor yang sama**. Perbedaannya hanya pada mapping `orderv1.CreateOrderRequest` → `CreateOrderInput` dan error → `codes.*`. Consumer Redis Stream (`adapter/inbound/consumer`) juga memanggil use case, misalnya `HandlePaymentSucceeded`, setelah melewati dedupe inbox.

### 5.5 [L3] Outbound adapter — `adapter/outbound/postgres/order_repository.go`

```go
package postgres

import (
    "context"

    "backend-go/internal/modules/order/domain"
    "backend-go/internal/modules/order/usecase/port"
    "backend-go/internal/platform/database"
)

var _ port.OrderRepository = (*OrderRepository)(nil) // compile-time check

type OrderRepository struct{ db *database.Group }

func (r *OrderRepository) Create(ctx context.Context, o *domain.Order, key, fp string) error {
    q := database.Executor(ctx, r.db.Write()) // pakai tx dari ctx bila ada
    _, err := q.ExecContext(ctx, `
        INSERT INTO orders (id, customer_id, amount_minor, currency, status, idempotency_key, request_fingerprint, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
        o.ID(), o.CustomerID(), o.Amount().Minor(), o.Amount().Currency(), o.Status(), key, fp, o.CreatedAt())
    return mapPgError(err) // unique_violation → domain error yang relevan
}
```

### 5.6 [L4] Composition root — `internal/app/app.go` (cuplikan)

```go
func (a *App) wireOrder() *order.Module {
    return order.New(order.Deps{
        Tx:        database.NewTxManager(a.db.Write()),
        DB:        a.db,
        Cache:     a.cache,
        Outbox:    outbox.NewWriter(a.cfg.Outbox.Partitions),
        Payment:   grpcclient.MustConn(a.cfg.PaymentAddr, a.grpcClientOpts...),
        Clock:     clock.System{},
        IDs:       ids.UUIDv7{},
    })
}

// app/http.go
orderMod.RegisterHTTP(api.Group("/v1/orders",
    idempotency.HTTPMiddleware(a.idemStore, a.cfg.Idempotency), // L1 edge — cross-cutting
))
// app/grpc.go
orderv1.RegisterOrderServiceServer(a.grpcSrv, orderMod.GRPCServer())
// app/worker.go
a.consumers = append(a.consumers, orderMod.Consumers(a.streamFactory, a.inbox)...)
```

---

## 6. Di Mana Setiap Concern Diletakkan

| Concern | Lapisan | Lokasi |
| --- | --- | --- |
| Invariant bisnis (amount > 0, transisi status) | L1 | `modules/<m>/domain` |
| Domain event (struct) | L1 | `modules/<m>/domain/events.go` |
| Orkestrasi + batas transaksi | L2 | `modules/<m>/usecase/*.go` via `port.TxManager` |
| **Idempotency bisnis** (key + fingerprint → hasil sama) | L2 + L3 | Aturan di use case; `UNIQUE` constraint di repository Postgres |
| **Idempotency edge** (retry/concurrent HTTP & gRPC) | L4 | `platform/idempotency` (middleware/interceptor) dipasang di `app/` |
| **Idempotency consumer** (inbox) | L3 + L4 | `platform/messaging/inbox` dipakai oleh `adapter/inbound/consumer` |
| Menulis event ke outbox | L3 | `adapter/outbound/postgres/outbox.go` (implement `port.EventOutbox`) |
| Relay outbox → Redis Streams | L4 | `platform/outbox` + `platform/messaging/redisstream` |
| Serialisasi event (JSON/proto, versi skema) | L3 | `adapter/outbound/postgres/outbox.go` (mapper domain event → payload) |
| Read-after-write vs eventual | L2 memutuskan, L3 menjalankan | `port.Consistency` → repository memilih primary/replica/cache |
| Cache | L3 | `adapter/outbound/redis` (atau decorator repository) |
| Panggilan ke service lain | L2 port, L3 implementasi | `port.PaymentGateway` → `adapter/outbound/paymentclient` |
| Mapping error → HTTP status / gRPC code | L3 | `platform/httpserver/respond`, `platform/grpcserver/grpcerr` berbasis `kernel/apperr` |
| Auth & principal | L4 + L3 | `platform/auth` menaruh `Principal` di ctx; adapter meneruskannya ke input DTO |
| Validasi format (JSON shape, panjang header) | L3 | DTO adapter |
| Validasi bisnis | L1/L2 | Konstruktor entity / interactor |
| Rate limit, circuit breaker, tracing, metrics | L4 | `platform/resilience`, `platform/observability` |
| Konfigurasi | L4 | `internal/config`, hanya dibaca oleh `app/` lalu diteruskan sebagai nilai |

---

## 7. Menegakkan Dependency Rule secara Otomatis

Struktur folder saja tidak cukup; aturan dependensi harus **gagal di CI** jika dilanggar.

### 7.1 `depguard` di `.golangci.yml`

```yaml
linters:
  enable: [depguard]
linters-settings:
  depguard:
    rules:
      domain:
        files: ["**/internal/modules/*/domain/**"]
        allow: ["$gostd", "backend-go/internal/kernel"]
      usecase:
        files: ["**/internal/modules/*/usecase/**"]
        allow:
          - "$gostd"
          - "backend-go/internal/kernel"
          - "backend-go/internal/modules/*/domain"   # depguard memakai prefix; lihat 7.2 untuk aturan per-modul
          - "backend-go/internal/modules/*/usecase"
        deny:
          - { pkg: "database/sql", desc: "usecase harus memakai port.TxManager / port.*Repository" }
      platform:
        files: ["**/internal/platform/**"]
        deny:
          - { pkg: "backend-go/internal/modules", desc: "platform tidak boleh tahu tentang domain" }
          - { pkg: "backend-go/internal/app",     desc: "platform tidak boleh bergantung pada composition root" }
          - { pkg: "backend-go/internal/config",  desc: "terima nilai config lewat parameter, bukan import" }
      kernel:
        files: ["**/internal/kernel/**"]
        allow: ["$gostd"]
```

### 7.2 (Opsional) `go-arch-lint` untuk aturan yang lebih presisi

[`go-arch-lint`](https://github.com/fe3dback/go-arch-lint) mendukung definisi komponen dan `mayDependOn`, termasuk larangan antar modul:

```yaml
version: 3
workdir: internal
components:
  kernel:    { in: kernel/** }
  domain:    { in: modules/*/domain }
  usecase:   { in: modules/*/usecase/** }
  inbound:   { in: modules/*/adapter/inbound/** }
  outbound:  { in: modules/*/adapter/outbound/** }
  module:    { in: modules/* }
  platform:  { in: platform/** }
  app:       { in: app }
deps:
  domain:   { mayDependOn: [kernel] }
  usecase:  { mayDependOn: [kernel, domain] }
  inbound:  { mayDependOn: [kernel, domain, usecase, platform], canUse: [gin, grpc] }
  outbound: { mayDependOn: [kernel, domain, usecase, platform], canUse: [pgx, goredis] }
  module:   { mayDependOn: [kernel, domain, usecase, inbound, outbound, platform] }
  platform: { mayDependOn: [kernel, platform] }
  app:      { anyProjectDeps: true }
```

Jalankan `go-arch-lint check` di CI bersama `golangci-lint`.

### 7.3 Test sebagai bukti arsitektur

- `usecase/*_test.go` **tidak boleh** butuh Docker. Jika butuh, berarti ada detail yang bocor ke use case.
- Repository dites dengan integration test (testcontainers) di `adapter/outbound/postgres/*_test.go` dengan build tag `integration`.

---

## 8. Pemetaan dari Struktur Sekarang

### 8.1 Pelanggaran Dependency Rule yang ada sekarang

| Lokasi sekarang | Masalah | Perbaikan |
| --- | --- | --- |
| [domain/example_order.go](internal/domain/example_order.go), [domain/example_user.go](internal/domain/example_user.go) | Interface `ExampleOrderUsecase` dan `ExampleOrderRepository` didefinisikan di **domain**. Domain jadi tahu tentang lapisan di luarnya, dan entity punya tag `json`. | Pindahkan repository ke `usecase/port`. Input port cukup berupa struct interactor. Hapus tag `json` dari entity. |
| [usecase/example_order_reconciliation.go](internal/usecase/example_order_reconciliation.go) | Use case meng-import `internal/pkg/reconciliation` (framework/infra). Panahnya mengarah keluar. | Use case menyediakan `ReconcileOutbox.Execute(ctx, cutoff, limit)`. Adapter `adapter/inbound/job` yang mengimplementasikan `reconciliation.Job`. |
| [usecase/example_order_usecase.go](internal/usecase/example_order_usecase.go) | Use case melakukan `json.Marshal` payload event (detail serialisasi). | Use case/entity menghasilkan `domain.OrderCreated`, dan serialisasi dilakukan di adapter outbox. |
| [repository/example_order_repository.go](internal/repository/example_order_repository.go) | Repository membuka transaksi sendiri dan memutuskan kapan outbox ditulis (orkestrasi bisnis ada di adapter). | Batas transaksi dipindah ke use case via `port.TxManager`. Repository hanya `Create`, dan `port.EventOutbox.Enqueue` dipanggil oleh use case. |
| [handler/http/example_order_handler.go](internal/handler/http/example_order_handler.go) | Handler merakit middleware sendiri (`RegisterExampleOrderRoutes` menerima storage idempotency, limiter, dan flag `workflowReady`). | Perakitan middleware dipindah ke `app/http.go`. Handler hanya mendaftarkan route ke `gin.IRouter` yang diberikan. |
| [handler/grpc/example_user_grpc_handler.go](internal/handler/grpc/example_user_grpc_handler.go) | DTO gRPC dibuat tangan, bukan hasil generate dari proto. | Gunakan tipe dari `gen/proto/...` dan mapper di `adapter/inbound/grpc/mapper.go`. |
| [cmd/api/main.go](cmd/api/main.go) | Composition root dan siklus hidup server bercampur dalam satu `main()` sepanjang 300+ baris. | `cmd/api/main.go` cukup sekitar 10 baris memanggil `app.New(cfg).Run(ctx)`. |
| `internal/pkg/*` | Paket infra dan paket murni bercampur (`response`, `idempotency`, `cache`, `outbox`). | Infra dipindah ke `platform/*`. Yang murni (kategori error) dipindah ke `kernel/*`. |

### 8.2 Tabel pindah file

| Sekarang | Menjadi |
| --- | --- |
| `cmd/api/main.go` (wiring) | `internal/app/{app,infra,http,grpc,worker}.go` |
| `cmd/api/example_wiring.go`, `no_examples.go` | `internal/modules/order/module.go` + `internal/app/*` (build tag examples dipertahankan per modul) |
| `cmd/api/pprof.go` | `internal/platform/observability/pprof.go` |
| `internal/domain/example_order.go` (struct) | `internal/modules/order/domain/order.go` |
| `internal/domain/example_order.go` (interfaces) | `internal/modules/order/usecase/port/repository.go` |
| `internal/domain/example_errors.go` | `internal/modules/{order,user}/domain/errors.go` |
| `internal/domain/errors.go` | `internal/kernel/apperr/apperr.go` |
| `internal/usecase/example_order_usecase.go` | `internal/modules/order/usecase/{create_order,get_order}.go` |
| `internal/usecase/example_order_reconciliation.go` | `usecase/reconcile_outbox.go` + `adapter/inbound/job/reconcile.go` |
| `internal/usecase/example_consistency.go` | `port.Consistency` di `usecase/port/repository.go` |
| `internal/handler/http/example_order_handler.go` | `internal/modules/order/adapter/inbound/http/{handler,dto,routes}.go` |
| `internal/handler/grpc/*` | `internal/modules/user/adapter/inbound/grpc/*` |
| `internal/repository/example_order_repository.go` | `internal/modules/order/adapter/outbound/postgres/{order_repository,outbox}.go` |
| `internal/repository/example_user_repository.go` | `internal/modules/user/adapter/outbound/{postgres,redis}/*` |
| `internal/events/nats_outbox_publisher.go` | `internal/platform/messaging/nats/` (adapter alternatif) |
| *(baru)* | `internal/platform/messaging/{redisstream,inbox}/` |
| `internal/pkg/database/*` | `internal/platform/database/*` (+ `tx.go`) |
| `internal/pkg/database/migration/sql/*` | `migrations/{platform,order,user}/*` |
| `internal/pkg/middleware/*` | `internal/platform/httpserver/middleware/*` (+ interceptor gRPC → `platform/grpcserver`) |
| `internal/pkg/telemetry`, `internal/pkg/metrics` | `internal/platform/observability/*` |
| `internal/pkg/response` | `internal/platform/httpserver/respond` |
| `internal/pkg/{idempotency,outbox,cache,redisclient,resilience,health,shutdown,audit,reconciliation}` | `internal/platform/<sama>` |
| `proto/example_user/v1/*.proto` | `api/proto/user/v1/*.proto` (output generate → `gen/proto/...`) |
| `Dockerfile` (root) | dihapus, pakai `deployments/docker/Dockerfile` |
| `deployments/k8s/*.yaml` | `deployments/k8s/base/*` + `overlays/{dev,staging,prod}` |

### 8.3 Urutan migrasi yang aman (bertahap, setiap langkah tetap bisa di-build)

1. Buat `internal/platform/` dan `internal/kernel/`, lalu pindahkan `internal/pkg/*` (murni rename import, tanpa perubahan perilaku).
2. Buat `internal/app/` dan pindahkan wiring dari `main.go`.
3. Pindahkan contoh **order** ke `modules/order/`: domain → port → usecase → adapter. Perbaiki pelanggaran di 8.1 sekaligus.
4. Pindahkan contoh **user** ke `modules/user/`.
5. Pisahkan migrasi ke `migrations/` dan proto ke `api/proto/` + `gen/`.
6. Aktifkan `depguard`/`go-arch-lint` di CI. Mulai di tahap ini, pelanggaran baru akan gagal build.
7. Tambahkan `cmd/worker` dan `platform/messaging/redisstream` (lihat [review.md](review.md) bagian 4).

---

## 9. Anti-Pattern yang Harus Dihindari

- **`internal/interfaces/` atau `internal/contracts/` raksasa** yang berisi semua interface. Ini melanggar idiom Go dan CCP. Interface diletakkan di paket yang **memakainya**.
- **Entity anemik + use case gemuk**: semua aturan bisnis ada di interactor sementara entity hanya struct berisi field. Invariant milik entity harus berada di entity.
- **Satu use case besar `OrderService` dengan 20 method.** Pecah per use case (*Single Responsibility* menurut Uncle Bob: satu alasan untuk berubah = satu aktor).
- **Melempar `*sql.Tx`, `*gin.Context`, atau tipe proto ke use case.**
- **Paket `utils`/`helpers`/`common`.** Beri nama sesuai fungsinya (`clock`, `apperr`, `money`).
- **Repository generik `Repository[T]`** dengan `FindAll`/`Save` untuk semua entity. Port harus mengekspresikan kebutuhan use case, bukan operasi CRUD tabel.
- **Mock semua hal.** Untuk port sederhana, *fake* in-memory lebih mudah dibaca dan dirawat daripada mock hasil generate.
- **Lapisan demi lapisan** untuk endpoint yang hanya membaca data. Query read-only yang tidak punya aturan bisnis boleh memakai *query service* tipis (gaya CQRS ringan) di `usecase/query_*.go` yang langsung mengembalikan DTO, selama tetap melalui port.

---

## 10. Ringkasan

```text
cmd/        → tipis, hanya memanggil app
internal/
  app/      → composition root (Main Component) — satu-satunya tempat semua bertemu
  config/   → hanya dibaca oleh app
  kernel/   → [L1 shared] murni, tanpa dependensi
  modules/  → SCREAMING ARCHITECTURE per bounded context
    <m>/domain     → [L1] Entities
    <m>/usecase    → [L2] Use Cases + output ports
    <m>/adapter    → [L3] Interface Adapters (inbound: http/grpc/consumer/job; outbound: postgres/redis/client)
    <m>/module.go  → konstruktor modul
  platform/ → [L4] Frameworks & Drivers, reusable, tidak tahu domain
api/        → kontrak (proto, openapi, event schema)
gen/        → kode hasil generate
migrations/ → SQL per platform & per modul
```

Tujuan akhirnya adalah yang dikatakan Uncle Bob: *"The architecture should make the use cases obvious and allow you to defer decisions about frameworks, databases, and delivery mechanisms."* HTTP atau gRPC, NATS atau Redis Streams, Postgres dengan `database/sql` atau `pgxpool`: semuanya adalah **detail** yang bisa diganti di `adapter/` dan `platform/` tanpa menyentuh `domain/` dan `usecase/`.
