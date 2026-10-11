# Panduan Paket Outbox

`internal/pkg/outbox` menyediakan kontrak repository, adapter persistence
PostgreSQL, adapter dalam memori untuk pengujian, worker berpartisi, distributed
lock berbasis Redis, dan pembersih data kedaluwarsa.

## Persistence dan dukungan basis data

Worker dan cleaner hanya bergantung pada `outbox.Repository`. Gunakan
`outbox.NewPostgresRepository` untuk penyimpanan PostgreSQL yang persisten atau
`outbox.NewMemoryRepository` untuk pengujian dan penggunaan lokal saja. Adapter
PostgreSQL menggunakan `database/sql` dari Go; aplikasi mengelola connection
pool dan driver-nya. Dengan begitu, siklus hidup koneksi, kredensial, dan
pengaturan driver tetap dikendalikan oleh aplikasi.

Repository menggunakan tabel `outbox_events` dan `dlq_events` yang didefinisikan
di `internal/pkg/database/migration/sql/000002_create_outbox_table.up.sql` dan
`000003_create_dlq_table.up.sql`. Jalankan migrasi proyek sebelum memulai worker:

```go
if err := migration.NewEngine().Run(ctx, db); err != nil {
    return fmt.Errorf("gagal menjalankan migrasi basis data: %w", err)
}

outboxRepo := outbox.NewPostgresRepository(db)
```

Pengujian integrasi proyek menggunakan driver `database/sql` dari pgx. Daftarkan
dan buka driver di aplikasi (atau pilih driver lain yang kompatibel dengan
PostgreSQL):

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

## Simpan event dalam transaksi bisnis

Agar jaminan transactional outbox tetap terpenuhi, simpan perubahan bisnis dan
event outbox dalam transaksi basis data yang sama. Berikan transaksi tersebut
sebagai `SQLExecutor` repository:

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

Gunakan repository yang terikat ke transaksi hanya untuk operasi tulis dalam
transaksi tersebut. Buat repository terpisah dari `*sql.DB` untuk worker dan
cleaner yang berjalan lama agar keduanya dapat menggunakan koneksinya sendiri.

## Jalankan worker

Worker melakukan polling pada setiap partisi yang dikonfigurasi, mengambil
distributed lock untuk partisi tersebut, lalu menerbitkan event yang siap
diproses. Berikan implementasi `DistributedLock`; untuk deployment beberapa
instance, gunakan Redis lock yang tersedia.

```go
repo := outbox.NewPostgresRepository(db)
lock := outbox.NewRedisDistributedLock(redisClient)
worker := outbox.NewWorker(outbox.DefaultConfig(), repo, lock, publisher)

worker.Start(ctx)
// Saat shutdown, batalkan ctx lalu tunggu worker berhenti.
worker.Stop()
```

Jalankan satu cleaner untuk setiap deployment; Redis lock mencegah beberapa
cleaner berjalan bersamaan:

```go
cleaner := outbox.NewCleaner(outbox.DefaultCleanerConfig(), repo, lock)
cleaner.Start(ctx)
// Saat shutdown:
cleaner.Stop()
```

Cleaner hanya menghapus event berstatus published yang lebih lama dari masa
retensi yang dikonfigurasi. Record DLQ tetap disimpan agar dapat diperiksa dan
dikirim ulang oleh aplikasi.

## Pengiriman dan perilaku operasional

- Pengiriman bersifat **at least once**, bukan exactly once. Proses dapat
  menerbitkan event lalu berhenti sebelum menandainya sebagai published.
  Consumer sebaiknya mendeduplikasi berdasarkan ID event atau dibuat idempoten.
- Kegagalan penerbitan akan dicoba kembali dengan jeda eksponensial. Setelah
  mencapai batas `MaxRetries` worker, event ditandai `DLQ` dan disalin ke
  `dlq_events`.
- `Save` bersifat idempoten berdasarkan ID event (upsert). Gunakan ID event
  yang stabil dan unik.
- `FetchPendingBatch` mengembalikan event pending dan event failed yang waktu
  percobaan ulangnya telah tiba, diurutkan berdasarkan waktu pembuatan dalam
  setiap partisi.
- Atur `PartitionCount` worker agar sesuai dengan rentang partisi yang digunakan
  saat menetapkan partisi event. Perubahan jumlah partisi memerlukan rollout
  terkoordinasi agar producer dan worker menggunakan pemetaan partisi yang sama.

## Tambahkan dukungan untuk basis data lain

Pastikan kode bisnis, `Worker`, dan `Cleaner` tetap menggunakan
`outbox.Repository`; jangan tambahkan cabang khusus basis data ke komponen
tersebut. Untuk mendukung basis data lain:

1. Implementasikan semua method `outbox.Repository` dalam adapter terpisah,
   misalnya `MySQLRepository`.
2. Sesuaikan SQL, sintaks placeholder, mekanisme penguncian/pengambilan record,
   dan migrasi skema dengan basis data tersebut.
3. Pertahankan kontrak perilaku: jadwal percobaan ulang, filter partisi,
   transisi status, perilaku DLQ, batas retensi, serta penerusan context dan
   error.
4. Pastikan pemindahan event ke DLQ dan perubahan status sumber berlangsung
   secara atomik, menggunakan satu statement atau transaksi.
5. Tambahkan pengujian integrasi untuk transaksi, worker yang berjalan
   bersamaan, percobaan ulang, DLQ, dan pembersihan dengan batas jumlah record.

`SQLExecutor` hanya menyediakan permukaan query `database/sql` minimum yang
dibutuhkan adapter; interface ini tidak membuat dialek SQL dapat dipertukarkan.
Untuk basis data yang tidak menggunakan `database/sql`, adapter dapat langsung
mengimplementasikan `Repository` menggunakan client native-nya. Aplikasi
memilih adapter saat dependency injection, tanpa perlu mengubah worker atau
use-case.
