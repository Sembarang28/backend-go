# Contoh Developer: Alur Order End-to-End

Panduan ini menunjukkan bagaimana package `database`, `cache`, `idempotency`,
`resilience`, `outbox`, dan `reconciliation` bekerja bersama dalam request API
order. Implementasi ini adalah alur contoh yang benar-benar terpasang pada
server HTTP saat build dengan tag `examples`; bukan integrasi payment gateway.
Jalankan server contoh menggunakan `make run-examples`.

## Prasyarat

Untuk membaca order, service memerlukan PostgreSQL primary dan migrasi terbaru.
GET biasa dapat menggunakan Redis untuk cache jika Redis aktif, dan memilih
read replica PostgreSQL jika replica dikonfigurasi.

Untuk membuat order, service hanya mengaktifkan `POST /orders` jika PostgreSQL,
Redis, dan NATS tersedia saat startup. Redis dipakai untuk rate limit,
idempotency, dan distributed lock. Jika salah satu dependency tersebut tidak
siap, POST akan mengembalikan `503 ORDER_WORKFLOW_UNAVAILABLE` alih-alih
menjalankan workflow tanpa perlindungan.

Jalankan migrasi terhadap primary sebelum rollout:

```sh
go run -tags=examples ./cmd/migrate
```

Di Kubernetes, `deployments/k8s/job-migration.yaml` menjalankan binary migrasi
yang sama. Pastikan `DB_PRIMARY_DSN` tersedia dari Secret dan Job selesai
sebelum Deployment aplikasi diperbarui.

## API dan pilihan konsistensi

### Membuat order

```http
POST /orders
X-Idempotency-Key: checkout-2026-00042
Content-Type: application/json

{
  "customer_id": "customer-17",
  "amount_minor": 2599,
  "currency": "IDR"
}
```

Order dibuat dengan status `PAYMENT_PENDING`. Endpoint ini belum melakukan
charge ke payment provider; event `order.created` menjadi pesan bagi sistem
downstream untuk melanjutkan proses.

Idempotency key wajib dan dibatasi maksimal 128 karakter. Key di-scope oleh
method, route, dan `X-Tenant-ID` (atau `default` bila header tidak ada).
Middleware menyimpan fingerprint request body; penggunaan key yang sama
dengan payload berbeda menerima `409 IDEMPOTENCY_KEY_REUSED`. Request duplikat
dengan key dan payload yang sama mendapatkan response yang tersimpan.

Selain state Redis, tabel order mempunyai unique idempotency key dan fingerprint
request. Ini mencegah duplikasi order bila response Redis kedaluwarsa/hilang
setelah transaksi bisnis berhasil.

### Membaca order

- `GET /orders/{id}` adalah jalur tampilan: Redis cache lebih dulu, lalu
  `DBGroup.Read()` yang dapat memilih replica. Order baru mungkin belum terlihat
  sementara pada replica.
- `GET /orders/{id}/critical` melewati Redis cache dan membaca melalui
  `DBGroup.ReadPrimary()`. Gunakan jalur ini untuk keputusan yang harus
  menggunakan state terbaru.

Jangan memakai GET tampilan untuk memvalidasi saldo, stok, pembayaran, hak
akses, atau kondisi yang memutuskan mutasi.

## Apa yang terjadi saat `POST /orders`

1. Handler memvalidasi JSON dan mensyaratkan `X-Idempotency-Key`.
2. Rate limiter Redis menerapkan sliding window berdasarkan tenant, user, atau
   IP. Jika Redis rate limit gagal, package saat ini fail-open dan menulis
   error log; idempotency tetap fail-closed sehingga order tidak diproses tanpa
   penyimpanan idempotency.
3. Idempotency middleware mengunci key, memeriksa fingerprint payload, dan
   menyimpan response untuk replay.
4. Use case memvalidasi nilai, membuat ID order, menetapkan status
   `PAYMENT_PENDING`, dan menghasilkan payload event.
5. Repository membuka transaksi di PostgreSQL primary. Dalam transaksi yang
   sama, repository menyimpan row order, `order.created` ke
   `outbox_events`, lalu menandai `outbox_enqueued`. Commit atomik memastikan
   order tidak committed tanpa event outbox.
6. Worker membaca outbox berdasarkan partition, menggunakan Redis distributed
   lock, lalu memublikasikan event. Circuit breaker membatasi publikasi saat
   NATS gagal; outbox menjadwalkan retry atau memindahkan event ke DLQ sesuai
   kebijakan retry.
7. Idempotency middleware menyimpan status/response. Retry dengan key dan
   payload sama tidak membuat order atau event kedua.

Outbox memberikan pola publish setidaknya-sekali: konsumen harus idempotent
terhadap redelivery. Publisher contoh memakai NATS Core dan `FlushWithContext`;
flush mengonfirmasi komunikasi dengan server NATS, tetapi bukan durable
consumer acknowledgement. Untuk jaminan broker durable, gunakan NATS JetStream
atau broker adapter yang memberi acknowledgement durable.

## Alur reconciliation

`OrderOutboxReconciliationJob` berjalan melalui `reconciliation.Engine` dengan
distributed lock. Job mencari order lama yang `outbox_enqueued = FALSE`, lalu
memulihkan `order.created` dan mengubah flag dalam satu transaksi primary.
ID event deterministik membuat upaya pemulihan aman diulang.

Endpoint pembuatan order normal menyimpan order, event, dan flag di satu
transaksi, sehingga kondisi tersebut tidak diharapkan pada operasi baru.
Reconciliation berguna untuk data legacy/import atau jalur penulisan lama
yang belum mengikuti transactional outbox. Job ini tidak memeriksa status
payment provider dan tidak mengubah status payment.

## Package yang digunakan

| Package | Peran dalam workflow |
| --- | --- |
| `internal/pkg/database` | Primary untuk transaksi dan critical reads; replica round-robin untuk tampilan |
| `internal/pkg/cache` | Cache untuk GET tampilan; critical GET melewati cache |
| `internal/pkg/idempotency` | Redis lock, fingerprint payload, response replay, dan key reuse conflict |
| `internal/pkg/resilience` | Redis sliding-window rate limit dan circuit breaker publisher NATS |
| `internal/pkg/outbox` | Transaksi repository, partition worker, retry, DLQ, distributed lock, retention cleanup |
| `internal/pkg/reconciliation` | Job periodik untuk memperbaiki order yang belum memiliki event outbox |

## Menjalankan dan memverifikasi

Atur `DB_PRIMARY_DSN`, `REDIS_MODE`, `REDIS_ADDRESSES`, `NATS_ADDRESS`, dan
konfigurasi pool sesuai lingkungan. Jalankan migrasi, lalu service. Untuk
pengujian cepat:

```sh
curl -X POST http://127.0.0.1:8080/orders \
  -H 'Content-Type: application/json' \
  -H 'X-Idempotency-Key: checkout-2026-00042' \
  -d '{"customer_id":"customer-17","amount_minor":2599,"currency":"IDR"}'
```

Kirim request identik sekali lagi untuk memeriksa response replay. Kirim
payload berbeda dengan key yang sama untuk memeriksa respons `409`. Pastikan
NATS menerima subject `order.created`, lalu cek status order melalui endpoint
display dan critical.

Tes PostgreSQL dan Redis dengan Testcontainers:

```sh
go test -tags=integration ./tests/integration
```

## Batasan dan langkah pengembangan berikutnya

- Tidak ada payment gateway yang di-charge dalam contoh ini. Tambahkan port
  payment provider dan adapter khusus sebelum memproses transaksi pembayaran.
- NATS Core tidak memberi durable acknowledgement untuk subscriber; gunakan
  JetStream bila diperlukan.
- Circuit breaker dipakai pada publisher NATS. Status open menghasilkan error
  publish dan outbox retry/DLQ; pantau DLQ dan sediakan prosedur redrive.
- Reconciliation hanya memperbaiki keberadaan event outbox, bukan
  menyelesaikan payment pending.
- Idempotency key disimpan unik sepanjang row order dipertahankan. Retensi
  order dan kebijakan penggunaan ulang key perlu diselaraskan sebelum
  menghapus order.
- Replica PostgreSQL tetap dapat lag dan belum dikeluarkan otomatis oleh
  router. Baca [panduan replication PostgreSQL](./postgres_replication_guide.md)
  dan [panduan Redis](./redis_replication_guide.md) untuk detail konsistensi.
