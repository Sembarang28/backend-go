# Review Skeleton `backend-go`

> Target: skeleton microservice Go yang **production-ready** dan **idempotent**, melayani **HTTP + gRPC**, dengan **PostgreSQL**, **Redis**, dan **message queue berbasis Redis**.
>
> Tanggal review: 2026-10-15 · Basis: commit `ab6bfef` (branch `main`)
>
> Metode: membaca seluruh kode inti (`cmd/`, `internal/pkg/*`, contoh order/user, migrasi, deployment, CI), lalu verifikasi dengan `go build`, `go vet`, dan `go test -race` (dengan dan tanpa tag `examples`).

---

## 1. Ringkasan

Fondasinya **di atas rata-rata** untuk sebuah skeleton: sudah ada transactional outbox, DLQ, reconciliation, idempotency, rate limiter terdistribusi, circuit breaker, phased graceful shutdown, health probe, Prometheus, audit log, read-replica routing, manifest K8s yang di-hardening, dan pemisahan kode contoh dengan build tag `examples`. Contoh alur order (`example_order_repository.go`) juga sudah menerapkan pola idempotency yang **benar** di level DB.

Tapi **belum production-ready**. Ada beberapa bug yang benar-benar akan muncul di production, terutama:

| # | Masalah | Dampak |
| --- | --- | --- |
| 1 | `TimeoutMiddleware` menjalankan `c.Next()` di goroutine lain | **Data race terkonfirmasi** (`go test -race` FAIL). Bisa membuat proses crash dan response tertukar. |
| 2 | Idempotency gRPC me-replay response dengan tipe **request** | Client menerima pesan dengan tipe yang salah. `CheckGRPCIdempotency` justru menjalankan ulang handler. |
| 3 | Idempotency Redis tidak mencatat siapa pemilik lock (tidak ada fencing token) | Request lambat bisa menimpa hasil request lain, dan lock bisa terhapus oleh request yang bukan pemiliknya. |
| 4 | Outbox: lock 5 detik tanpa perpanjangan, tanpa `SKIP LOCKED`, event `PROCESSING` bisa tertahan selamanya | Publish ganda, atau event tidak pernah terkirim. |
| 5 | CI dan root `Dockerfile` membangun `cmd/api/main.go` sebagai satu file | **Build gagal** (`undefined: wireExamples`, `StartPprofServer`). CI juga memakai Go 1.22 padahal `go.mod` meminta 1.26.1. |
| 6 | gRPC server tidak punya service, port di-hardcode `:50051`, interceptor idempotency tidak dipasang | Sisi gRPC dari skeleton belum benar-benar jalan. |
| 7 | Belum ada abstraksi **consumer** dan **inbox** untuk message queue (dan belum ada Redis Streams sama sekali, yang ada NATS) | Kebutuhan inti "message queue Redis + idempotent consumer" belum tercakup. |

Rekomendasi: kerjakan **P0** (bagian 10) dulu sebelum skeleton ini dipakai untuk service baru.

---

## 2. Yang Sudah Bagus (pertahankan)

- **Idempotency di level DB untuk order** ([example_order_repository.go:63-93](internal/repository/example_order_repository.go#L63-L93)): `UNIQUE(idempotency_key)` + `request_fingerprint` + `ON CONFLICT DO NOTHING`, lalu outbox ditulis dalam transaksi yang sama. Ini pola yang benar dan harus jadi **sumber kebenaran**. Redis cukup sebagai lapisan cepat di depannya.
- **Transactional outbox + DLQ + reconciler + cleaner**, lengkap dengan partisi dan lock terdistribusi.
- **Distributed lock** memakai token acak dan release/extend lewat Lua yang atomik ([lock.go](internal/pkg/outbox/lock.go)).
- **Rate limiter sliding window** atomik lewat Lua, aman untuk Redis Cluster karena hanya memakai satu key.
- **Cache** dengan singleflight, plus `LoadWithoutCache` / `ReadPrimary` untuk keputusan yang butuh konsistensi.
- **Phased shutdown**, readiness yang bisa di-flip, `automaxprocs`, dan pprof yang hanya di-bind ke localhost.
- **K8s**: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop ALL`, PDB, HPA, NetworkPolicy, migrasi lewat Job terpisah.
- Kode contoh dipisah dengan build tag `examples` dan target `make remove-examples`.
- Sudah ada integration test dengan testcontainers.

---

## 3. Idempotency (fokus utama)

### 3.1 Bug yang ditemukan

#### A. gRPC replay memakai tipe request, bukan response (KRITIS)
[grpc.go:64-71](internal/pkg/idempotency/grpc.go#L64-L71)

```go
if protoMsg, ok := req.(proto.Message); ok {
    replyMsg := protoMsg.ProtoReflect().New().Interface() // ← tipe REQUEST
    protojson.Unmarshal(existing.ResponseBody, replyMsg)
```

Body response di-unmarshal ke tipe request. Akibatnya gRPC akan gagal marshal, atau client menerima pesan dengan field yang salah. Fallback ke `map[string]interface{}` juga tidak bisa di-serialize oleh gRPC.

**Perbaikan:** simpan response sebagai `anypb.Any` (bytes protobuf ditambah type URL), lalu replay dengan `anypb.UnmarshalNew`. Jangan pakai protojson, karena lebih lambat dan tidak *lossless* untuk sebagian tipe.

#### B. `CheckGRPCIdempotency` menjalankan ulang handler saat status COMPLETED
[middleware.go:160-190](internal/pkg/idempotency/middleware.go#L160-L190): jika `existing.Status == StatusCompleted`, kode jatuh ke `handler(ctx)` dan **mengeksekusi ulang operasinya**. Ini kebalikan dari idempotency. Response juga tidak disimpan. **Hapus fungsi ini** dan tipe `GRPCUnaryInterceptor` / `GRPCServerHandler` yang tidak dipakai.

#### C. Tidak ada fingerprint di gRPC
`UnaryServerInterceptor` memanggil `TryLock` tanpa fingerprint, jadi key yang sama dengan payload berbeda langsung di-replay. HTTP sudah punya pengecekan ini, gRPC belum. Untuk gRPC, fingerprint dihitung dari `info.FullMethod + proto.MarshalOptions{Deterministic: true}.Marshal(req)`.

#### D. Lock tanpa kepemilikan (tidak ada fencing token)
[idempotency.go:105-126](internal/pkg/idempotency/idempotency.go#L105-L126): `SaveResponse` memakai `SET` biasa dan `ReleaseLock` memakai `DEL` biasa.

Skenario: request A memegang lock dengan TTL 30 detik, tapi prosesnya 35 detik. Lock A expire, request B (retry dari client) mendapat lock dan mulai jalan. Lalu A selesai dan:
- `SaveResponse` milik A menimpa state B, atau
- jika A error 5xx, `ReleaseLock` milik A **menghapus lock B**, sehingga request C bisa masuk dan mengeksekusi untuk ketiga kalinya.

**Perbaikan:** `TryLock` mengembalikan `token`. `SaveResponse` dan `ReleaseLock` dijalankan lewat Lua yang hanya mengubah key jika `token` cocok (contoh kode di 3.3).

#### E. Context request dipakai untuk menyimpan hasil
[middleware.go:137](internal/pkg/idempotency/middleware.go#L137): jika client disconnect atau timeout, `ctx` sudah di-cancel sehingga `SaveResponse` gagal. Key tertahan di status `STARTED` sampai TTL habis, dan selama itu semua retry dapat `409 REQUEST_IN_PROGRESS`. Padahal operasinya **sudah sukses**.
**Perbaikan:** `ctx := context.WithoutCancel(c.Request.Context())` ditambah timeout pendek (mis. 2 detik) khusus untuk save/release.

#### F. In-flight TTL tidak sinkron dengan timeout request
`DefaultInFlightTTL = 30s`, sedangkan timeout HTTP/gRPC = 10 detik dan `WriteTimeout` = 10 detik. Ini aman selama handler menghormati `ctx`. Tapi kalau handler memanggil downstream yang tidak menghormati ctx, lock bisa expire saat proses masih jalan.
**Perbaikan:** turunkan `inFlightTTL` dari konfigurasi (`>= requestTimeout + margin`), tambahkan heartbeat/extend untuk operasi panjang, dan validasi di `Config.Validate()`.

#### G. Kebijakan cache status code
[middleware.go:127](internal/pkg/idempotency/middleware.go#L127): semua `< 500` di-cache, termasuk `429`, `408`, `409` dari dependency, dan `401/403`. Retry dengan token yang sudah diperbaiki tetap akan mendapat `401` yang tersimpan sampai 24 jam.
**Perbaikan:** cache hanya `2xx` dan 4xx yang **deterministik** dari validasi bisnis (400, 404, 409 domain, 422). Untuk `401/403/408/429` dan semua `5xx`, lepas lock.

#### H. Replay tidak setia
[middleware.go:113](internal/pkg/idempotency/middleware.go#L113): `Content-Type` di-hardcode `application/json` dan hanya satu header yang disimpan. Sebaiknya simpan `Content-Type`, `Location`, dan header bisnis yang relevan. Tambahkan juga header `Idempotent-Replayed: true` (konvensi draf IETF), lalu hapus `X-Cache-Lookup`.

#### I. Perilaku saat Redis mati tidak konsisten
- `RedisStorage` dengan `client == nil` mengembalikan `acquired=true` ([idempotency.go:70-72](internal/pkg/idempotency/idempotency.go#L70-L72)), yaitu **fail-open diam-diam**.
- HTTP middleware dengan `storage == nil` mengembalikan 503 (fail-closed).
- gRPC interceptor dengan `storage == nil` akan **panic** karena method dipanggil pada nil interface ([grpc.go:53](internal/pkg/idempotency/grpc.go#L53)).
- `main.go` lanjut jalan tanpa Redis jika ping gagal ([main.go:137-141](cmd/api/main.go#L137-L141)).

**Perbaikan:** jadikan kebijakan eksplisit lewat config (`IDEMPOTENCY_FAILURE_MODE=closed|open`, default `closed`), dan terapkan sama di HTTP dan gRPC. Untuk endpoint uang atau stok, wajib `closed`.

#### J. Lain-lain
- Key HTTP generik tidak di-scope per tenant/user/route. Scoping hanya ada di contoh order (`scopeIdempotencyKey`). Akibatnya dua user dengan key dan body yang sama akan **saling menerima response milik yang lain** (kebocoran data). Pindahkan scoping ke middleware inti, dengan principal diambil dari **auth context**, bukan dari header `X-Tenant-ID` yang bisa dipalsukan.
- Panjang dan karakter key tidak divalidasi di middleware inti.
- `StatusFailed`, `ErrInvalidKey`, dan `TryLock` tanpa fingerprint tidak terpakai. Rapikan interface-nya.
- `MemoryStorage` tidak pernah membersihkan entri yang sudah expire (memory leak di test jangka panjang). Ini minor.
- Gunakan nama header standar `Idempotency-Key` (draf IETF `draft-ietf-httpapi-idempotency-key-header`), dengan `X-Idempotency-Key` tetap diterima demi kompatibilitas.
- Body dibatasi 1 MiB di middleware, tapi itu hardcode. Jadikan konfigurasi.

### 3.2 Desain idempotency yang direkomendasikan (3 lapis)

```
              ┌───────────────────────────────────────────────────────────┐
 Client ──►   │ L1  Edge (HTTP middleware / gRPC interceptor) — Redis     │  cepat; dedupe
 retry        │     key = svc:route:principal:Idempotency-Key             │  retry & concurrent
              │     state STARTED(token) → COMPLETED(resp, fingerprint)   │
              └───────────────────────────────────────────────────────────┘
                                   │
              ┌───────────────────────────────────────────────────────────┐
              │ L2  Domain/DB — UNIQUE(idempotency_key) + fingerprint     │  sumber kebenaran;
              │     business write + outbox INSERT dalam 1 transaksi      │  aman saat Redis
              └───────────────────────────────────────────────────────────┘  failover/evict
                                   │ outbox worker (at-least-once)
              ┌───────────────────────────────────────────────────────────┐
              │ L3  Consumer — tabel inbox (consumer, message_id) PK      │  dedupe pesan
              │     INSERT inbox + efek bisnis dalam 1 transaksi → XACK   │  duplikat dari MQ
              └───────────────────────────────────────────────────────────┘
```

Aturannya:
1. **L2 wajib** untuk setiap operasi yang menulis state. Redis bisa kehilangan key (eviction, failover dengan replikasi async, `FLUSHALL`), jadi L1 saja tidak cukup.
2. **L3 wajib** untuk setiap consumer, karena outbox dan Redis Streams sama-sama *at-least-once*.
3. Event ID harus **deterministik** dari aggregate (contoh: `order-created-<orderID>`, yang sudah benar di contoh) supaya consumer bisa dedupe.
4. Panggilan keluar ke service lain juga harus meneruskan idempotency key turunan (mis. `<key>:payment`) supaya retry berantai tetap aman.

### 3.3 Sketsa kode L1 yang diperbaiki

```go
// internal/pkg/idempotency/redis.go
var (
    // KEYS[1]=key ARGV[1]=token ARGV[2]=startedRecord ARGV[3]=ttlMs
    lockScript = redis.NewScript(`
        if redis.call("SET", KEYS[1], ARGV[2], "NX", "PX", ARGV[3]) then return {1, ARGV[2]} end
        return {0, redis.call("GET", KEYS[1])}`)

    // Hanya pemilik token yang boleh menyimpan hasil.
    completeScript = redis.NewScript(`
        local cur = redis.call("GET", KEYS[1])
        if not cur then return 0 end
        if cjson.decode(cur).token ~= ARGV[1] then return 0 end
        redis.call("SET", KEYS[1], ARGV[2], "PX", ARGV[3]) return 1`)

    // Hanya pemilik token yang boleh melepas lock.
    releaseScript = redis.NewScript(`
        local cur = redis.call("GET", KEYS[1])
        if cur and cjson.decode(cur).token == ARGV[1] then return redis.call("DEL", KEYS[1]) end
        return 0`)
)

type Store interface {
    Acquire(ctx context.Context, key, fingerprint string, ttl time.Duration) (Lease, *Record, error)
    Complete(ctx context.Context, l Lease, rec *Record, ttl time.Duration) error
    Release(ctx context.Context, l Lease) error
    Extend(ctx context.Context, l Lease, ttl time.Duration) error // heartbeat untuk proses panjang
}

type Lease struct{ Key, Token string }
```

```go
// di middleware, setelah handler selesai:
saveCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
defer cancel()
if shouldCache(status) {
    err = store.Complete(saveCtx, lease, rec, completedTTL)
} else {
    err = store.Release(saveCtx, lease)
}
if errors.Is(err, ErrLeaseLost) { metrics.IdempotencyLeaseLost.Inc() } // alarm: TTL terlalu pendek
```

```go
// gRPC: simpan & replay dengan anypb
anyResp, _ := anypb.New(res.(proto.Message))
payload, _ := proto.Marshal(anyResp)
...
var a anypb.Any
_ = proto.Unmarshal(existing.ResponseBody, &a)
msg, err := a.UnmarshalNew() // tipe response yang benar
```

Simpan juga **status error gRPC yang deterministik** (`InvalidArgument`, `NotFound`, `FailedPrecondition`, `AlreadyExists`) sebagai `status.Status` proto, supaya retry mendapat error yang sama.

### 3.4 Test yang perlu ditambahkan untuk idempotency
- N goroutine paralel dengan key yang sama: handler hanya boleh dieksekusi **tepat 1x**. Jalankan dengan `-race`.
- Lease expire di tengah proses: request lama tidak boleh menimpa atau menghapus lease baru.
- Client disconnect setelah handler sukses: retry harus mendapat replay, bukan 409.
- Key sama + body beda: 422/409 di HTTP, `InvalidArgument`/`FailedPrecondition` di gRPC.
- Key sama di tenant berbeda: tidak boleh saling replay.
- Redis mati: perilaku sesuai `FAILURE_MODE`, dan **tidak panic**.
- Redis di-flush di tengah: L2 (DB unique) tetap mencegah duplikasi.
- Replay gRPC menghasilkan tipe response yang benar.

---

## 4. Message Queue Redis (belum ada, perlu ditambahkan)

Saat ini broker yang dipakai adalah **NATS core** (`nats.Conn.Publish`). Ini *fire-and-forget*: tidak ada persistence, ack, atau redelivery, jadi pesan hilang saat consumer down. Karena target Anda Redis, rekomendasinya **Redis Streams** dengan consumer group.

### 4.1 Yang perlu dibuat

```
internal/pkg/messaging/
  messaging.go        // interface Publisher, Consumer, Message, Handler
  redisstream/
    publisher.go      // XADD MAXLEN ~ N, header (event_id, type, traceparent, tenant)
    consumer.go       // XREADGROUP BLOCK, XACK, XAUTOCLAIM (reclaim pending), retry + DLQ stream
  inbox/
    postgres.go       // dedupe consumer (L3)
```

```go
type Message struct {
    ID       string            // event ID deterministik dari outbox, BUKAN stream ID
    Type     string
    Key      string            // aggregate ID → ordering/partition
    Payload  []byte
    Headers  map[string]string
    Attempt  int
}

type Handler func(ctx context.Context, m Message) error

type Consumer interface {
    Run(ctx context.Context) error   // block sampai ctx done; graceful drain
}
```

### 4.2 Aturan konsumsi yang production-ready
1. **Consumer group per service** (`XGROUP CREATE ... MKSTREAM`), dengan nama consumer = nama pod (`HOSTNAME`).
2. **Ack setelah commit DB**, jangan sebelumnya. Urutannya: `BEGIN → INSERT inbox ON CONFLICT DO NOTHING → (jika baru) efek bisnis → COMMIT → XACK`.
3. **Reclaim**: jalankan `XAUTOCLAIM` berkala untuk pesan pending yang idle lebih dari `visibilityTimeout` (pod mati di tengah proses).
4. **Retry + DLQ**: pakai counter delivery dari `XPENDING`. Jika lebih dari `maxDeliveries`, `XADD` ke `<stream>.dlq`, lalu `XACK` pesan aslinya.
5. **Poison message**: error permanen (decode gagal, validasi) langsung masuk DLQ tanpa retry.
6. **Backpressure**: jumlah worker per consumer dibatasi (`semaphore`), dan `COUNT` pada `XREADGROUP` dibuat terkonfigurasi.
7. **Trimming**: `XADD ... MAXLEN ~ <n>` atau `MINID`, supaya memori Redis tidak tumbuh tanpa batas.
8. **Ordering**: jika urutan per aggregate penting, gunakan beberapa stream ter-partisi (`orders:{p}`) dengan satu consumer aktif per partisi, atau terima urutan *eventual* dan pakai `version` di payload.
9. **Graceful shutdown**: berhenti membaca, tunggu in-flight selesai, lalu berhenti. Pesan yang belum di-ack akan di-reclaim oleh pod lain.
10. **Durability Redis**: aktifkan `appendonly yes` + `appendfsync everysec`, dan `min-replicas-to-write` jika memakai replica. Terima bahwa failover **bisa kehilangan sekitar 1 detik** pesan. Karena itu **outbox di Postgres tetap sumber kebenaran**, dan reconciler harus bisa me-republish.

### 4.3 Tabel inbox

```sql
CREATE TABLE inbox_messages (
    consumer      VARCHAR(100) NOT NULL,
    message_id    VARCHAR(128) NOT NULL,
    received_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);
CREATE INDEX idx_inbox_received_at ON inbox_messages (received_at); -- untuk retensi
```

```go
func (c *Consumer) handle(ctx context.Context, m messaging.Message) error {
    return database.WithTx(ctx, c.db, func(tx *sql.Tx) error {
        res, err := tx.ExecContext(ctx,
            `INSERT INTO inbox_messages (consumer, message_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
            c.name, m.ID)
        if err != nil { return err }
        if n, _ := res.RowsAffected(); n == 0 { return nil } // duplikat → ack saja
        return c.handler(ctx, tx, m)
    })
}
```

### 4.4 Publisher outbox → Redis Streams
Buat `events.NewRedisStreamOutboxPublisher(client, breaker)` yang mengimplementasikan `outbox.MessagePublisher`. Simpan `ev.ID` sebagai field `event_id` (jangan andalkan stream ID). Nama stream diturunkan dari `AggregateType`/`EventType` lewat *topic mapper*, bukan langsung `ev.EventType`.

Pertahankan NATS sebagai **adapter alternatif**, tapi jika tetap dipakai, gunakan **JetStream** (ada `Nats-Msg-Id` untuk dedupe di sisi broker), bukan core NATS.

---

## 5. Concurrency & HTTP

### 5.1 `TimeoutMiddleware` — data race (KRITIS, terkonfirmasi)
[timeout.go:15-38](internal/pkg/middleware/timeout.go#L15-L38)

```
$ go test -race ./internal/pkg/middleware/...
--- FAIL: TestTimeoutMiddleware
    testing.go:1867: race detected during execution of test
```

`c.Next()` berjalan di goroutine lain. Saat timeout, goroutine utama menulis 504 sementara handler **masih menulis** ke `c.Writer` yang sama. Setelah middleware return, gin mengembalikan `*gin.Context` ke pool, padahal handler masih memakainya, sehingga **response antar-request bisa tertukar**. Panic di goroutine itu juga **tidak tertangkap** `RecoveryMiddleware` dan langsung meng-crash proses. Interaksinya dengan idempotency juga buruk: client menerima 504, tapi handler tetap sukses dan cache menyimpan 201.

**Perbaikan:** jangan pakai goroutine. Cukup pasang deadline di context dan biarkan handler/DB/Redis menghormatinya:

```go
func Timeout(d time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx, cancel := context.WithTimeout(c.Request.Context(), d)
        defer cancel()
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

Untuk *hard limit*, gunakan `http.TimeoutHandler` di level `http.Server` (thread-safe, buffer sendiri).

### 5.2 `main.go`
- [main.go:199-204](cmd/api/main.go#L199-L204): tidak ada `ReadHeaderTimeout` (rentan Slowloris; `gosec G112`), `IdleTimeout`, maupun `MaxHeaderBytes`.
- [main.go:206-211](cmd/api/main.go#L206-L211): jika `ListenAndServe` gagal (port terpakai), error hanya di-log dan **proses tetap hidup** tanpa melayani apa pun. Liveness juga gagal di port itu, jadi pod restart loop tanpa pesan yang jelas.
- [main.go:214](cmd/api/main.go#L214): port gRPC di-hardcode `:50051`, padahal `cfg.GRPCAddress` ada tapi tidak dipakai. Error `net.Listen` juga **ditelan** diam-diam.
- Redis dan NATS gagal saat startup tetap lanjut (*degraded*), tapi tidak ada reconnect atau re-register health. Untuk service yang butuh Redis demi idempotency, ini sebaiknya **fail fast** (atau mode yang dikonfigurasi).
- `main()` terlalu panjang (300+ baris). Pecah ke `internal/app` dengan pola `App.Run(ctx) error` dan `errgroup`:

```go
g, ctx := errgroup.WithContext(signalCtx)
g.Go(func() error { return serveHTTP(ctx, httpSrv) })
g.Go(func() error { return serveGRPC(ctx, grpcSrv, lis) })
g.Go(func() error { return outboxWorker.Run(ctx) })
g.Go(func() error { return consumer.Run(ctx) })
return g.Wait() // listener gagal → seluruh app shutdown dengan exit code ≠ 0
```

### 5.3 Shutdown
[shutdown.go:59-62](internal/pkg/shutdown/shutdown.go#L59-L62) melakukan `time.Sleep(drainDelay)` **sebelum** phase 1 (`SetReady(false)`) dijalankan. Akibatnya selama drain delay readiness masih `UP`, sehingga drain delay itu sia-sia. Urutan yang benar: **flip readiness → sleep drain → shutdown server → stop worker → close pool**.

Hal lain:
- `drainDelay` (2s) ditambah `preStop sleep 3` ditambah `ShutdownTimeout` (15s) harus `< terminationGracePeriodSeconds` (30s). Validasi ini dan dokumentasikan.
- Sinyal kedua (SIGTERM/SIGINT berulang) sebaiknya memaksa exit.
- `grpcSrv.GracefulStop()` bisa hang selamanya jika ada stream terbuka. Bungkus dengan timeout lalu `Stop()`.
- `ListenAndServe` selalu mengembalikan `nil`, sehingga exit code tidak mencerminkan kegagalan shutdown.
- Worker outbox menerima ctx dari `context.Background()` dan `Stop()` menunggu `wg` **tanpa batas waktu** (publish yang hang akan menahan shutdown).

### 5.4 Middleware lain
- **CORS** [security.go:36-39](internal/pkg/middleware/security.go#L36-L39): `AllowedOrigins: ["*"]` + `AllowCredentials: true` + me-*reflect* origin berarti **situs mana pun bisa memanggil API dengan cookie user**. Default harus *deny*, dengan origin diambil dari config. Tambahkan `Vary: Origin`. Untuk service internal (service-to-service), CORS tidak perlu sama sekali.
- **Recovery gRPC** [recovery.go:78](internal/pkg/middleware/recovery.go#L78) membocorkan isi panic ke client (`internal server error: %v`). Kembalikan pesan generik saja.
- **RequestID**: nilai dari header client diterima apa adanya (panjang/karakter tidak divalidasi, berisiko log injection), tidak dimasukkan ke `context.Context`, dan tidak ikut di log `slog` maupun outbox headers. Request ID juga perlu diteruskan ke gRPC metadata dan pesan MQ.
- **Logger** memakai `slog.Error(...)`, bukan `slog.ErrorContext(ctx, ...)`, jadi `trace_id` dari `TraceHandler` **tidak pernah muncul** di access log. Logger juga mencatat raw query string yang bisa berisi token. Sebaiknya *redact* query, atau log path saja.
- **SecurityHeaders**: `X-XSS-Protection` sudah deprecated (set `0` atau hapus). HSTS `preload` sebaiknya opt-in. CSP `default-src 'self'` pada JSON API tidak berbahaya, tapi tidak terlalu berguna.
- **Metrics** [metrics.go:188-191](internal/pkg/metrics/metrics.go#L188-L191): fallback ke `URL.Path` untuk route yang tidak dikenal menyebabkan **cardinality explosion** (scanner mengirim ribuan path acak). Gunakan label konstan `"unmatched"`.
- **Response envelope**: tidak ada `request_id`/`trace_id` di body error, dan tidak ada `details` untuk error validasi. Pertimbangkan **RFC 9457 (Problem Details)**.
- **Validasi input**: ada `binding:"required"`, tapi tidak ada `DisallowUnknownFields` dan tidak ada batas ukuran body global (hanya di idempotency).

### 5.5 Auth tidak ada
Belum ada middleware/interceptor autentikasi (JWT/OIDC untuk edge, mTLS atau token service untuk internal). Rate limit dan scoping idempotency saat ini mengandalkan header `X-Tenant-ID` / `X-User-ID` yang **bisa dipalsukan client**. Tambahkan paket `internal/pkg/auth` yang menaruh `Principal{TenantID, UserID, Scopes}` ke context, lalu ambil tenant dan user **hanya** dari sana.

---

## 6. gRPC (belum production-ready)

| Item | Status | Perbaikan |
| --- | --- | --- |
| Service terdaftar | ❌ tidak ada `Register*Server` | Generate dari proto (`buf generate`). Handler contoh masih memakai DTO buatan tangan, bukan tipe dari proto. |
| Codegen | ❌ `make proto` ada di `.PHONY` tapi tidak ada rule | Tambahkan `buf.yaml`, `buf.gen.yaml`, `buf lint`, `buf breaking` di CI. |
| Address | ❌ hardcode `:50051` | Pakai `cfg.GRPCAddress`, dan listener yang gagal harus fatal. |
| Idempotency interceptor | ❌ tidak dipasang | Pasang setelah bug di 3.1 diperbaiki. |
| Rate limit interceptor | ⚠️ ada tapi tidak dipasang; panic jika limiter nil | Pasang dan tambahkan nil-guard. |
| Health service | ❌ | `grpc_health_v1` + sinkron dengan `healthMgr` (K8s `grpc` probe). |
| Reflection | ❌ | Aktifkan di non-production saja. |
| Keepalive / MaxRecvMsgSize / MaxConcurrentStreams | ❌ | `grpc.KeepaliveParams`, `KeepaliveEnforcementPolicy`, dan batas pesan. |
| Stream interceptors | ❌ hanya unary | Recovery, trace, metrics, auth untuk stream. |
| Logging interceptor + request ID | ❌ | Tambahkan. |
| Error mapping domain → `codes` | ⚠️ manual per handler | Buat helper `grpcerr.From(err)` yang konsisten dengan HTTP. |
| Client factory | ❌ | `grpcclient.New(target)` dengan retry policy (service config), circuit breaker, trace propagation, deadline default, dan idempotency key propagation. |
| TLS / mTLS | ❌ | Minimal opsi config, atau dokumentasikan bahwa mTLS ditangani service mesh. |
| Proto versioning | ✅ `example_user.v1` | Pertahankan. |

Satu catatan desain: buat **satu lapisan usecase yang sama** dipakai HTTP dan gRPC (ini sudah diterapkan), lalu satukan *error taxonomy* (`domain.ErrNotFound`, `ErrConflict`, `ErrInvalid`, ...) yang di-map ke HTTP status dan gRPC code di satu tempat.

---

## 7. Database & Outbox

### 7.1 Outbox worker
- **Lock tanpa perpanjangan** [worker.go:121](internal/pkg/outbox/worker.go#L121): TTL 5 detik, batch 100–200 event, publish dengan flush per event. Jika batch butuh lebih dari 5 detik, pod lain mengambil partisi yang sama dan **mem-publish ulang** event yang sama. `Extend` sudah ada di `DistributedLock` tapi tidak pernah dipanggil.
- **Event `PROCESSING` tertahan selamanya**: `MarkProcessing` lalu pod crash, dan `FetchPendingBatch` hanya mengambil `PENDING`/`FAILED` ([postgres_repository.go:121](internal/pkg/outbox/postgres_repository.go#L121)). Event tidak akan pernah terkirim, dan reconciler order juga tidak menangkapnya karena `outbox_enqueued` sudah `TRUE`.
- **Mengandalkan Redis lock untuk exclusivity DB**: jika Redis mati, outbox berhenti total walaupun DB sehat.

**Perbaikan:** klaim berbasis lease di Postgres, sehingga Redis lock tidak diperlukan:

```sql
ALTER TABLE outbox_events ADD COLUMN locked_until TIMESTAMPTZ, ADD COLUMN locked_by VARCHAR(100);

WITH claimable AS (
    SELECT id FROM outbox_events
    WHERE partition = $1
      AND (
            (status IN ('PENDING','FAILED') AND next_retry_at <= now())
         OR (status = 'PROCESSING' AND locked_until < now())      -- reclaim pod mati
      )
    ORDER BY created_at, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events o
SET status = 'PROCESSING', locked_by = $3, locked_until = now() + $4::interval, updated_at = now()
FROM claimable c WHERE o.id = c.id
RETURNING o.*;
```

Status `MarkPublished`/`MarkFailed` hanya boleh diubah oleh pemegang lease (`WHERE id=$1 AND locked_by=$2`).

- **Retry**: `MaxRetries` per event (kolom `max_retries`) diabaikan dan yang dipakai config global ([worker.go:185](internal/pkg/outbox/worker.go#L185)). Backoff `1<<retryCount` tanpa **jitter** dan tanpa **cap**.
- **Ordering**: jika event N gagal dan di-backoff, event N+1 untuk aggregate yang sama tetap terkirim, sehingga **urutan rusak**. Jika butuh urutan per aggregate, hentikan pemrosesan aggregate itu selama ada event sebelumnya yang belum terkirim.
- **`Save` upsert menimpa semua kolom** [postgres_repository.go:83-96](internal/pkg/outbox/postgres_repository.go#L83-L96): memanggil ulang `Save` dengan ID yang sama (mis. dari reconciler) akan **me-reset event yang sudah `PUBLISHED` menjadi `PENDING`**, sehingga terkirim ganda. Ganti dengan `ON CONFLICT (id) DO NOTHING`.
- **Topic = `ev.EventType`** langsung. Tambahkan *topic mapper*.
- **Metrics outbox** (`OutboxUnprocessedEvents`, `OutboxDLQEventsTotal`, `OutboxProcessingLatency`) sudah diregistrasi tapi **tidak pernah diisi**. Tambahkan juga gauge *oldest pending age*, karena itu alert terpenting untuk outbox.
- **Index**: tambahkan index untuk cleaner `(status, updated_at)` dan partial index `WHERE status IN ('PENDING','FAILED','PROCESSING')`.
- Pertimbangkan `LISTEN/NOTIFY` atau polling adaptif sebagai pengganti polling 100–200 ms × 16 partisi × N pod (beban query konstan ke primary).
- Ada dua salinan `outbox.NewPostgresRepository(tx).Save` + `crc32` di repo order. Buat helper `outbox.Enqueue(ctx, tx, event)` dan `outbox.PartitionFor(key, n)`.

### 7.2 Database umum
- **Tidak ada helper transaksi/Unit of Work.** Setiap repository menulis `BeginTx/Rollback/Commit` sendiri. Tambahkan:

```go
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
    tx, err := db.BeginTx(ctx, nil)
    if err != nil { return err }
    defer func() {
        if p := recover(); p != nil { _ = tx.Rollback(); panic(p) }
        if err != nil { _ = tx.Rollback(); return }
        err = tx.Commit()
    }()
    return fn(tx)
}
```

  Tambahkan juga retry otomatis untuk `40001` (serialization) dan `40P01` (deadlock).
- **`database/sql` + pgx stdlib** sudah cukup, tapi `pgxpool` langsung memberi tracing hook, batch, `COPY`, dan performa lebih baik. Pertimbangkan **sqlc** untuk query yang type-safe.
- **Tidak ada `statement_timeout` / `idle_in_transaction_session_timeout`.** Set lewat DSN (`?options=-c statement_timeout=5000`) atau `AfterConnect`.
- **Read replica di readiness probe** [main.go:91-96](cmd/api/main.go#L91-L96): satu replica down membuat **semua pod tidak ready**, sehingga terjadi outage total walaupun primary sehat. Replica sebaiknya hanya dilaporkan sebagai *degraded*, dan `Read()` harus melewati replica yang tidak sehat.
- **Replication lag**: `Read()` round-robin tanpa memeriksa lag. Untuk read-after-write sudah ada `ReadPrimary()` (bagus), tapi dokumentasikan kapan wajib dipakai.
- `DynamicTenantRouter` fallback ke default DB untuk tenant yang tidak dikenal. Di sistem multi-tenant dengan isolasi DB, ini **kebocoran data antar tenant**. Jadikan fallback opsional, dan default-nya error.
- `DBMaxOpenConns=50` × replicas HPA (sampai N pod) bisa melebihi `max_connections` Postgres. Dokumentasikan rumusnya, atau gunakan PgBouncer.

### 7.3 Migrasi
- Tidak ada **advisory lock** (`pg_advisory_lock`), sehingga dua Job/pod yang menjalankan migrasi bersamaan bisa saling tabrakan.
- Tidak ada **down migration** maupun **checksum**. File yang sudah diterapkan bisa diubah diam-diam tanpa terdeteksi.
- Setiap migrasi selalu dibungkus transaksi, sehingga `CREATE INDEX CONCURRENTLY` tidak bisa dipakai (penting untuk tabel besar).
- Penomoran versi contoh dan inti bercampur (`000001_example`, `000002` inti, `000005_example`). Saat `remove-examples` dijalankan, ada lubang versi. Jika examples diaktifkan belakangan, versi lama akan diterapkan setelah versi yang lebih baru. Pisahkan direktori atau rentang versinya.
- Rekomendasi: pakai `golang-migrate` atau `goose` (embed FS didukung) daripada engine buatan sendiri.

---

## 8. Redis

- [client.go](internal/pkg/redisclient/client.go): tidak ada konfigurasi `DialTimeout`, `ReadTimeout`, `WriteTimeout`, `PoolSize`, `MinIdleConns`, `PoolTimeout`, `MaxRetries`, **TLS**, maupun **Username (ACL)**. Default go-redis (read timeout 3 detik) terlalu lama untuk jalur idempotency.
- Tidak ada mode **Sentinel** (`redis.NewFailoverClient`), padahal ini setup HA paling umum selain Cluster. `docs/redis_replication_guide.md` perlu disinkronkan.
- `.env.production` memakai `REDIS_ADDRESS=redis-cluster.internal` tapi **tidak** men-set `REDIS_MODE=cluster`, sehingga client berjalan dalam mode single ke endpoint cluster dan akan menerima error `MOVED`.
- **Key prefix per service** (`<service>:idempotency:...`) belum ada. Jika beberapa microservice berbagi Redis, key bisa bertabrakan.
- Pisahkan Redis untuk **cache** (eviction `allkeys-lru` boleh) dan untuk **idempotency/lock/stream** (`noeviction` + AOF). Kalau digabung, eviction akan menghapus key idempotency dan lock.
- `Eval` dengan script string di setiap panggilan sebaiknya diganti `redis.NewScript(...).Run()` (EVALSHA, hemat bandwidth).
- Instrumentasi: `redisotel` untuk tracing dan metrics, plus `PoolStats()` ke Prometheus.
- **Cache**: `Set` memakai ctx request di dalam singleflight, padahal komentar menyebut "asynchronously" (kenyataannya sinkron). Jika caller pertama cancel, semua caller yang menunggu ikut gagal. Gunakan `context.WithoutCancel` di dalam `sf.Do`. Tidak ada negative caching (risiko penetrasi cache) dan tidak ada jitter TTL.

---

## 9. Observability, Config, Deployment

### 9.1 Observability
- **Tracing buatan sendiri** ([telemetry/trace.go](internal/pkg/telemetry/trace.go)): tidak ada exporter, sampler, atau span untuk DB/Redis/MQ/HTTP client. Server juga **memakai ulang span ID dari parent** sebagai span-nya sendiri (tidak membuat child span), sehingga trace tree rusak. Ganti dengan **OpenTelemetry SDK** + OTLP exporter, `otelgin`, `otelgrpc` (stats handler), `otelsql`/`pgx` tracer, dan `redisotel`. Dependensi OTel sudah ada secara indirect di `go.sum`.
- **Log**: tambahkan atribut tetap `service`, `version`, `env`, `pod` lewat `slog.With`. Tambahkan juga redaction untuk field sensitif.
- **Metrics yang belum ada**: idempotency (hit/replay/conflict/lease_lost), rate limit (allowed/denied/error), circuit breaker state, outbox lag, consumer lag (`XPENDING`), DLQ size, Redis pool, build info (`version`, `commit`).
- Endpoint `/metrics` ada di port publik. Pindahkan ke port admin terpisah (bersama pprof/health), atau batasi lewat NetworkPolicy.
- Readiness body membocorkan pesan error internal (`"DOWN: " + err.Error()`), mis. hostname DB. Tampilkan hanya `UP`/`DOWN` di endpoint publik.

### 9.2 Config
- **Tidak ada validasi** ([config.go:79-113](internal/config/config.go#L79-L113)). Nilai yang salah (`DB_MAX_OPEN_CONNS=abc`, `READ_TIMEOUT=10`) hanya memunculkan warning lalu diam-diam memakai default ([config.go:143-147](internal/config/config.go#L143-L147)). Di production, nilai yang salah harus **gagal start**. Tambahkan `func (c *Config) Validate() error`.
- Default dev ikut terbawa ke production: `NatsAddress` default `nats://127.0.0.1:4222`, `ServerAddress` default `127.0.0.1:8080` (tidak bisa diakses dari luar container jika ConfigMap lupa men-set nilainya).
- `DBReplicaDSN` dan `DBReplicaDSNs` duplikat. Pilih salah satu.
- `godotenv.Load` di production sebaiknya dimatikan, karena file `.env` di image bisa menimpa env dari K8s secara tak terduga. Load `.env` hanya jika `APP_ENV != production`.
- `.env.production` di-commit dengan placeholder `CHANGE_ME_SECRET`. Lebih aman berupa `.env.production.example` saja, dengan secret dari Secret Manager / External Secrets.
- Tambahkan `SERVICE_NAME`, `SERVICE_VERSION`, `IDEMPOTENCY_INFLIGHT_TTL`, `IDEMPOTENCY_FAILURE_MODE`, `HTTP_REQUEST_TIMEOUT`, `GRPC_REQUEST_TIMEOUT`, `CORS_ALLOWED_ORIGINS`, dan `REDIS_*` timeout/TLS.

### 9.3 CI / Build / Deploy
- **CI pasti gagal** ([ci.yml](.github/workflows/ci.yml)):
  - `go-version: '1.22'`, padahal `go.mod` meminta `go 1.26.1`. Gunakan `go-version-file: go.mod`.
  - `go build ./cmd/api/main.go` gagal (terverifikasi: `undefined: StartPprofServer`, `undefined: wireExamples`). Gunakan `./cmd/api`.
  - Test tidak memakai `-race` (padahal ada race nyata). Test tag `examples` dan `integration` juga tidak dijalankan.
  - `golangci-lint-action@v3` + `version: latest` tidak reproducible. Pin versinya dan tambahkan `.golangci.yml` (govet, staticcheck, errcheck, gosec, bodyclose, contextcheck, noctx, sqlclosecheck).
  - Tambahkan `govulncheck`, `go mod tidy` diff check, `buf lint/breaking`, docker build + **Trivy** scan, dan build SBOM.
- **Root `Dockerfile`** juga memakai `go build ... cmd/api/main.go` (gagal), memakai `alpine:3.19`, dan dipakai oleh `docker-compose.yml`. Hapus root Dockerfile dan gunakan satu file saja (`deployments/docker/Dockerfile`).
- `deployments/docker/Dockerfile`: `GOARCH=amd64` di-hardcode (gagal di node ARM/Graviton). Gunakan `TARGETARCH` dari buildx. Pertimbangkan base `gcr.io/distroless/static:nonroot`. Tambahkan `-ldflags "-X main.version=..."`.
- `docker-compose.yml` hanya berisi `api`. Tambahkan `postgres`, `redis` (AOF on), opsional `otel-collector` + `jaeger`, dan service `migrate`.
- K8s:
  - Image `:latest` + `IfNotPresent` membuat rollout tidak deterministik. Gunakan tag immutable atau digest.
  - Tidak ada probe gRPC, dan port 9090 di-`EXPOSE` tapi tidak dipakai.
  - `readOnlyRootFilesystem: true` tanpa `emptyDir` untuk `/tmp`.
  - Tidak ada `topologySpreadConstraints` / anti-affinity.
  - Tidak ada `GOMEMLIMIT` (set sekitar 90% dari memory limit via `resourceFieldRef`).
  - `startupProbe` memakai `/healthz/live`, yang selalu 200 bahkan sebelum dependency siap.
  - Namespace `default`. Gunakan Kustomize/Helm overlay per environment.
- `Makefile`: target `proto` dan `mock` di-declare tapi rule `proto` tidak ada. `mockery --all` berat; lebih baik pakai `.mockery.yaml`.

---

## 10. Rekomendasi Struktur Tambahan

```
cmd/
  api/            # HTTP + gRPC server
  worker/         # outbox relay + consumer (opsional: deploy terpisah dari API)
  migrate/
internal/
  app/            # composition root: New(cfg) → App{Run(ctx) error}, errgroup
  config/         # + Validate()
  platform/       # (rename dari pkg/) infra lintas service
    auth/         # Principal, JWT/mTLS middleware & interceptor
    database/     # pool, WithTx, retry serialization
    redisclient/  # single/sentinel/cluster + TLS + timeout
    idempotency/  # Store(lease/token), HTTP mw, gRPC interceptor, anypb replay
    messaging/    # Publisher/Consumer interface
      redisstream/
      inbox/
    outbox/       # lease-based claim, relay → messaging.Publisher
    grpcserver/   # NewServer(opts): interceptors, health, reflection, keepalive
    grpcclient/   # NewClient(target): retry, breaker, otel, deadline
    httpserver/   # NewServer(cfg): timeouts, middleware chain
    observability/# otel setup, slog, metrics
    apperror/     # error taxonomy → HTTP status & gRPC code
  <domain>/       # per bounded context: handler/, usecase/, repository/
api/proto/        # buf.yaml, buf.gen.yaml; generated code ke gen/
```

`internal/pkg` → `internal/platform` hanya saran penamaan, tidak wajib. Yang penting: **composition root terpisah** dari `main`, dan **server builder** HTTP/gRPC yang bisa dipakai ulang oleh setiap microservice turunan.

---

## 11. Roadmap Prioritas

### P0 — wajib sebelum dipakai (bug / keamanan)
- [ ] Perbaiki `TimeoutMiddleware`: hapus goroutine, pastikan `go test -race ./...` hijau.
- [ ] Idempotency: lease token + CAS (Lua) untuk complete/release.
- [ ] Idempotency: `context.WithoutCancel` saat save/release.
- [ ] Idempotency gRPC: replay via `anypb`, fingerprint, nil-guard; hapus `CheckGRPCIdempotency`.
- [ ] Idempotency: scoping key per principal di middleware inti; kebijakan cache status code; `FAILURE_MODE` eksplisit.
- [ ] Outbox: `ON CONFLICT DO NOTHING` di `Save`; klaim lease + `SKIP LOCKED` + reclaim `PROCESSING`.
- [ ] CORS default deny; recovery gRPC tidak membocorkan panic.
- [ ] `main`: `ReadHeaderTimeout`/`IdleTimeout`; listener gagal → exit; pakai `cfg.GRPCAddress`.
- [ ] Perbaiki urutan shutdown (flip readiness → drain → stop).
- [ ] CI: `go-version-file`, `go build ./cmd/api`, `-race`; hapus atau perbaiki root `Dockerfile`.
- [ ] `.env.production`: tambahkan `REDIS_MODE=cluster` (atau sesuaikan).

### P1 — kebutuhan inti target (HTTP + gRPC + Redis MQ + DB)
- [ ] `messaging` + `redisstream` publisher/consumer (consumer group, XAUTOCLAIM, retry, DLQ stream, trimming).
- [ ] Tabel `inbox_messages` + helper consumer idempotent.
- [ ] Outbox publisher ke Redis Streams; topic mapper.
- [ ] gRPC: buf codegen, register service, health, reflection (non-prod), keepalive, stream interceptors, idempotency + rate limit interceptor.
- [ ] `grpcclient` factory (retry, breaker, otel, deadline, propagasi request-id & idempotency key).
- [ ] Auth middleware/interceptor + `Principal` di context.
- [ ] `database.WithTx` + retry `40001/40P01`; `statement_timeout`.
- [ ] `Config.Validate()` + fail fast di production.
- [ ] Redis client: timeouts, pool, TLS, ACL, Sentinel; pisahkan Redis cache dan Redis state.
- [ ] Readiness: replica tidak membuat pod unready; tidak membocorkan error.

### P2 — kualitas operasional
- [ ] OpenTelemetry penuh (otelgin, otelgrpc, otelsql/pgx, redisotel) + OTLP.
- [ ] Metrics idempotency, outbox lag, consumer lag, DLQ, breaker; label `unmatched` untuk 404.
- [ ] Migrasi: advisory lock, checksum, dukungan `CONCURRENTLY` (atau pakai goose/golang-migrate).
- [ ] Composition root `internal/app` + `errgroup`; pisahkan `cmd/worker`.
- [ ] Docker multi-arch, distroless, version ldflags; compose lengkap (pg, redis, otel).
- [ ] K8s: image digest, gRPC probe, `GOMEMLIMIT`, topology spread, `emptyDir /tmp`, overlay per env.
- [ ] CI: golangci config, govulncheck, Trivy, buf breaking, integration test di CI.
- [ ] Error taxonomy terpadu + RFC 9457 untuk HTTP.

---

## 12. Catatan Verifikasi

| Perintah | Hasil |
| --- | --- |
| `go build -o /dev/null cmd/api/main.go` (cara build di CI dan root Dockerfile) | ❌ `undefined: StartPprofServer`, `undefined: wireExamples` |
| `go vet ./...` dan `go vet -tags=examples ./...` | ✅ bersih |
| `go test -race ./...` | ❌ `internal/pkg/middleware` — **race detected** di `TestTimeoutMiddleware` |
| `go test -race -tags=examples ./...` | ❌ race yang sama; paket lain lulus |

Integration test (`-tags=integration`) tidak dijalankan dalam review ini karena butuh Docker/testcontainers.
