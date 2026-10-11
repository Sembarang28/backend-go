# Panduan Paket Audit

Package `internal/pkg/audit` mencatat aksi penting oleh pengguna atau sistem.
Pilih salah satu implementasi `audit.Auditor` sesuai kebutuhan:

- `audit.NewStructuredAuditor(logger)` menulis event sebagai structured log
  melalui `log/slog`. Gunakan jika audit cukup dikirim ke sistem log aplikasi
  dan tidak perlu disimpan di basis data aplikasi.
- `audit.NewPostgresAuditor(db)` menyimpan event ke tabel `audit_events`.
  Gunakan jika audit perlu dicari dan dipertahankan dalam basis data. Argumennya
  dapat berupa `*sql.DB` atau `*sql.Tx`.

Keduanya digunakan melalui interface `audit.Auditor`, sehingga komponen bisnis
tidak perlu mengetahui pilihan penyimpanannya:

```go
var auditor audit.Auditor

if auditPersistenceEnabled {
    auditor = audit.NewPostgresAuditor(db)
} else {
    auditor = audit.NewStructuredAuditor(logger)
}
```

## Jalankan migrasi untuk PostgreSQL

Jalankan migrasi proyek sebelum menggunakan `PostgresAuditor`. Migrasi versi
`000004` membuat tabel `audit_events` beserta indeks pencarian berdasarkan
aktor dan resource:

```go
if err := migration.NewEngine().Run(ctx, db); err != nil {
    return fmt.Errorf("gagal menjalankan migrasi: %w", err)
}

auditor := audit.NewPostgresAuditor(db)
```

`PostgresAuditor` hanya menambahkan baris baru. ID dan waktu dibuat otomatis
jika tidak diberikan. Context tracing mengisi `TraceID` dan `SpanID` apabila
nilainya tidak disediakan pada event.

## Catat perubahan penting

Catat event setelah nilai sebelum dan sesudah perubahan diketahui. Untuk
memastikan audit persisten hanya tercatat ketika perubahan bisnis berhasil,
gunakan transaksi yang sama:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

if err := userRepository.Update(ctx, tx, user); err != nil {
    return err
}

txAuditor := audit.NewPostgresAuditor(tx)
if err := txAuditor.Log(ctx, audit.AuditEvent{
    ActorID:        actorID,
    Action:         "user.role.update",
    TargetResource: "User",
    ResourceID:     user.ID,
    PayloadBefore:  map[string]string{"role": oldRole},
    PayloadAfter:   map[string]string{"role": user.Role},
    IPAddress:      clientIP,
    UserAgent:      userAgent,
}); err != nil {
    return fmt.Errorf("gagal menyimpan audit event: %w", err)
}

if err := tx.Commit(); err != nil {
    return err
}
```

Tanda tangan `userRepository.Update` pada contoh merupakan ilustrasi; sesuaikan
dengan repository aplikasi. Bila memakai `StructuredAuditor`, pencatatan log
tidak dapat menjadi bagian dari transaksi basis data. Log yang sudah ditulis
tidak dapat di-rollback jika transaksi database kemudian gagal.

## Data yang dicatat dan perlindungannya

`AuditEvent` memuat ID aktor, aksi, jenis dan ID resource, snapshot sebelum dan
sesudah, alamat IP, user-agent, trace ID, span ID, serta waktu pembuatan.
`PayloadBefore` dan `PayloadAfter` disimpan sebagai JSONB di PostgreSQL.

- Catat hanya aksi penting seperti perubahan role/permission, perubahan data
  sensitif, operasi finansial, perubahan konfigurasi, atau akses data yang
  memang perlu ditelusuri.
- Jangan merekam password, token, secret, atau data pribadi yang tidak
  diperlukan. Saring snapshot sebelum dan sesudah sebelum memanggil `Log`.
- Berikan akses tulis minimum kepada akun database aplikasi dan batasi akses
  baca tabel audit. Tabel bersifat append-only melalui adapter ini, tetapi
  administrator basis data tetap dapat mengubah atau menghapus datanya.
- Tentukan retensi, backup, akses audit, dan kebijakan ekspor sesuai kebutuhan
  operasional atau regulasi. Untuk bukti anti-manipulasi, pertimbangkan
  penyimpanan terpisah dengan kontrol akses dan retensi yang sesuai.
- Jika penyimpanan audit persisten gagal, `Log` mengembalikan error. Tentukan
  kebijakan aplikasi secara eksplisit; untuk tindakan yang wajib diaudit,
  jangan mengabaikan error tersebut.

## Memilih implementasi

| Kebutuhan | Implementasi |
| --- | --- |
| Audit cukup tersedia di log terstruktur aplikasi | `NewStructuredAuditor(logger)` |
| Audit harus tersimpan dan dapat dicari di PostgreSQL | `NewPostgresAuditor(db)` |
| Perubahan bisnis dan audit harus commit/rollback bersama | `NewPostgresAuditor(tx)` |

Saat aplikasi berkembang, pilihan implementasi dapat ditentukan lewat
dependency injection atau konfigurasi aplikasi. Tidak perlu mengubah kode
business yang bergantung pada interface `audit.Auditor`.
