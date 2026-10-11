# Panduan Reconciliation Engine

Package `internal/pkg/reconciliation` menyediakan scheduler untuk menjalankan
pekerjaan rekonsiliasi berkala. Engine tidak mengetahui skema database atau
cara berkomunikasi dengan provider eksternal; aplikasi mengimplementasikan
`reconciliation.Job` untuk mengambil data yang tertunda dan menyelesaikan
rekonsiliasinya.

## Kapan digunakan

Gunakan reconciliation untuk memeriksa transaksi yang statusnya belum pasti,
misalnya pembayaran yang timeout saat meminta konfirmasi ke provider. Job dapat
menanyakan status transaksi ke provider, lalu memperbarui status lokal sesuai
hasilnya.

Reconciliation bukan pengganti transactional outbox dan bukan jaminan exactly
once. Operasi `Reconcile` dapat dipanggil lagi pada siklus berikutnya, jadi
implementasinya harus idempotent dan harus mengubah state lokal agar record yang
sudah ditangani tidak terus-menerus dianggap stale.

## Implementasikan Job

Satu job mewakili satu alur rekonsiliasi. `FetchStaleRecords` memilih paling
banyak `limit` record yang belum selesai dan lebih tua dari `threshold`.
`Reconcile` memeriksa satu record dan mengembalikan outcome.

```go
type PaymentReconciliationJob struct {
    payments PaymentRepository
    provider PaymentProvider
}

func (j *PaymentReconciliationJob) Name() string {
    return "pending-payments"
}

func (j *PaymentReconciliationJob) FetchStaleRecords(
    ctx context.Context,
    threshold time.Duration,
    limit int,
) ([]reconciliation.StaleRecord, error) {
    return j.payments.FetchPendingOlderThan(ctx, time.Now().Add(-threshold), limit)
}

func (j *PaymentReconciliationJob) Reconcile(
    ctx context.Context,
    record reconciliation.StaleRecord,
) (reconciliation.Outcome, error) {
    status, err := j.provider.GetPaymentStatus(ctx, record.Reference)
    if err != nil {
        return reconciliation.OutcomeRetryNeeded, err
    }

    switch status {
    case "succeeded":
        if err := j.payments.MarkSucceeded(ctx, record.ID); err != nil {
            return reconciliation.OutcomeFailed, err
        }
        return reconciliation.OutcomeResolved, nil
    case "failed":
        if err := j.payments.MarkFailed(ctx, record.ID); err != nil {
            return reconciliation.OutcomeFailed, err
        }
        return reconciliation.OutcomeResolved, nil
    default:
        return reconciliation.OutcomeIgnored, nil
    }
}
```

`PaymentRepository` dan `PaymentProvider` di atas adalah interface milik
aplikasi sebagai contoh, bukan bagian dari package reconciliation.
`FetchPendingOlderThan` perlu menerapkan filter waktu dan batas jumlah record di
database, bukan mengambil semua record lalu memfilter di memori. `Reference`
sebaiknya memuat identifier yang dibutuhkan untuk mencari transaksi pada
provider.

Engine saat ini mencatat outcome ke log, tetapi tidak melakukan transisi state
sendiri. Job bertanggung jawab untuk memperbarui status lokal, menyimpan jadwal
retry/counter bila diperlukan, dan membedakan error sementara dari error
permanen. Record yang gagal diproses harus tetap bisa dipilih kembali menurut
kebijakan aplikasi; record yang sudah selesai atau memang tidak perlu diproses
lagi harus dikeluarkan dari hasil fetch berikutnya.

## Daftarkan dan jalankan

Contoh berikut menggunakan lock Redis yang sudah tersedia di package outbox.
Berikan client Redis yang sudah dibuat dan dikelola oleh aplikasi.

```go
cfg := reconciliation.DefaultConfig()
cfg.Interval = 5 * time.Minute
cfg.Threshold = 15 * time.Minute
cfg.BatchSize = 100
cfg.LockTTL = 2 * time.Minute

lock := outbox.NewRedisDistributedLock(redisClient)
engine := reconciliation.NewEngine(cfg, lock)
engine.RegisterJob(&PaymentReconciliationJob{
    payments: paymentRepo,
    provider: paymentProvider,
})

engine.Start(ctx)
```

`Start` membuat satu goroutine per job yang terdaftar. Job pertama kali
dijalankan setelah satu `Interval`, bukan langsung saat startup. Panggil
`RegisterJob` sebelum `Start`.

Hentikan engine saat graceful shutdown:

```go
cancel() // batalkan context aplikasi agar loop berhenti
engine.Stop()
```

`Stop` menunggu goroutine job selesai. Jangan memanggil `Stop` lebih dari
sekali pada instance yang sama.

## Pengaturan dan distributed lock

`reconciliation.DefaultConfig()` menetapkan:

| Opsi | Default | Arti |
| --- | --- | --- |
| `Interval` | 5 menit | Jeda polling untuk setiap job |
| `Threshold` | 15 menit | Umur minimum record sebelum diproses |
| `BatchSize` | 100 | Maksimum record yang diambil per siklus job |
| `LockTTL` | 1 menit | Masa berlaku lock job |

Nilai nol atau negatif saat memanggil `NewEngine` akan diganti dengan default.
Lock menggunakan key `reconciliation:lock:<job-name>`, sehingga replicas
berbeda tidak menjalankan job bernama sama secara bersamaan. Berikan lock
terdistribusi pada deployment multi-instance; `nil` dapat dipakai untuk
development satu proses atau pengujian.

Pilih `LockTTL` lebih panjang dari durasi normal satu batch. Engine belum
memperpanjang lock secara otomatis; jika satu batch bisa berjalan lebih lama
dari TTL, instance lain berpotensi menjalankan job yang sama secara bersamaan.
Karena itu `Reconcile` tetap harus idempotent dan query pemilihan record
sebaiknya aman terhadap pemrosesan berulang.

Nama job harus stabil dan unik di antara job yang memakai lock yang sama.
Jangan mengganti nama sembarang saat rolling deployment; nama berbeda
menghasilkan lock key berbeda dan dapat memungkinkan kedua versi job berjalan
bersamaan.

## Operasional

- Buat filter `FetchStaleRecords` deterministik dan gunakan urutan stabil agar
  batch terus bergerak saat jumlah record melebihi `BatchSize`.
- Simpan status, retry count, dan `next_retry_at` dalam sistem domain bila
  dibutuhkan; `StaleRecord` hanya objek input, bukan penyimpanan.
- Jadikan pemanggilan provider idempotent atau gunakan idempotency key/reference
  yang stabil untuk menangani proses yang berhenti di tengah jalan.
- Pantau log `Failed to fetch stale records for reconciliation`,
  `Reconciliation error on record`, dan outcome job. Engine tidak mengekspos
  metrik rekonsiliasi khusus.
- Kesalahan acquire lock menyebabkan siklus job dilewati. Kesalahan fetch dan
  reconcile dicatat ke log; job akan dicoba lagi pada interval berikutnya,
  sesuai hasil fetch yang dikembalikan aplikasi.
- Test job secara unit dengan repository/provider palsu, lalu test adapter
  persistence dan perilaku idempotency secara terpisah.
