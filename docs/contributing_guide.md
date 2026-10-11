# Panduan Kontribusi Developer

Panduan ini menjelaskan cara mengembangkan template backend ini agar perubahan
tetap reusable, teruji, dan tidak menambahkan asumsi domain bisnis tertentu.

## Prinsip pengembangan

- Perlakukan repository ini sebagai fondasi untuk service perusahaan
- Package di `internal/pkg/` adalah tempat kemampuan infrastruktur reusable
  seperti database, cache, outbox, idempotency, reconciliation, resilience,
  telemetry, dan shutdown.
- Implementasi `user` dan `order` yang ada adalah **contoh opsional**. File
  contohnya memakai awalan `example_` dan build tag Go `examples`, sehingga
  tidak dimasukkan ke build default.
- Jangan menambahkan aturan domain, skema bisnis, route, dependency, atau
  environment variable contoh ke aplikasi default kecuali perubahan tersebut
  memang merupakan kemampuan generik yang dibutuhkan fondasi.
- Pertahankan kompatibilitas API package dan konfigurasi yang sudah ada. Jika
  ada perubahan breaking, jelaskan dampaknya dan sediakan panduan migrasi.
- Tangani error secara eksplisit. Jangan membuang error, menyamarkan kegagalan
  menjadi hasil sukses, atau menambahkan fallback diam-diam.

## Menyiapkan lingkungan

Persyaratan utama:

- Versi Go sesuai direktif `go` di [go.mod](../go.mod).
- `make` dan `golangci-lint` untuk menjalankan shortcut validasi.
- Docker hanya diperlukan untuk tes integrasi berbasis Testcontainers.

Clone repository, buat branch kerja, lalu pastikan baseline bersih:

```sh
git status --short
go test ./...
```

Jangan menyalin credential ke repository. Gunakan environment lokal atau secret
manager untuk konfigurasi sensitif. `.env.example` hanya untuk nama variabel
dan nilai contoh non-rahasia.

## Memahami struktur kode

| Lokasi | Tanggung jawab |
| --- | --- |
| `cmd/api/` | Bootstrap aplikasi, dependency injection, server dan graceful shutdown |
| `cmd/migrate/` | Menjalankan migrasi skema terhadap PostgreSQL primary |
| `internal/config/` | Membaca dan memvalidasi konfigurasi environment |
| `internal/pkg/` | Komponen infrastruktur dan helper generik |
| `internal/domain/` | Kontrak dan error domain yang memang dimiliki fondasi; domain contoh dipisahkan |
| `internal/handler/` | Adapter transport HTTP atau gRPC; handler contoh memakai build tag `examples` |
| `internal/repository/` | Adapter persistence/cache untuk domain; implementasi contoh diberi awalan `example_` |
| `internal/usecase/` | Alur aplikasi; use case contoh diberi awalan `example_` |
| `internal/pkg/database/migration/sql/` | Migrasi SQL embedded |
| `tests/integration/` | Tes yang membutuhkan dependency nyata melalui Testcontainers |
| `docs/` | Panduan penggunaan, kontribusi, deployment, dan operasi |

Saat menambahkan kemampuan reusable, utamakan interface kecil pada batas
package dan implementasi adapter terpisah. Hindari menghubungkan package
infrastruktur ke entity bisnis contoh.

## Mengembangkan contoh domain

Gunakan contoh bila ingin menunjukkan cara memakai beberapa package bersama,
tanpa menjadikan alur itu bagian dari service dasar:

1. Beri nama file dengan awalan `example_`.
2. Tambahkan `//go:build examples` sebelum deklarasi package pada file Go.
3. Beri prefix `Example` pada tipe, interface, handler, constructor, dan
   fungsi registrasi yang menjadi bagian API contoh.
4. Letakkan dependency injection contoh di `cmd/api/example_wiring.go`.
   Bootstrap default harus tetap dapat dibangun tanpa package domain contoh.
5. Tandai tes contoh dengan build tag yang sama. Untuk tes integrasi contoh,
   gunakan `//go:build integration && examples`.
6. Awali migrasi skema contoh dengan nama migrasi `example_`. Migration engine
   mengabaikan migrasi tersebut pada build default.
7. Simpan dokumentasi yang hanya menerangkan contoh dengan nama
   `example_*.md`.
8. Perbarui panduan contoh dan README bila alur menjalankannya berubah.

Shortcut untuk menjalankan dan menguji contoh:

```sh
make run-examples
make test-examples
make test-example-integration
```

Tes integrasi contoh membutuhkan Docker. `make remove-examples` menghapus file
yang dinamai sebagai contoh secara permanen dari working tree. Gunakan hanya
ketika memang ingin membuat fork tanpa contoh, dan tinjau daftar file yang akan
dihapus sebelum menjalankannya. Build tag tidak menggantikan penamaan file:
keduanya diperlukan agar scope contoh jelas dan cleanup terarah.

## Menambahkan atau mengubah migrasi

- Beri setiap migrasi nomor urut unik dan deskripsi yang jelas, misalnya
  `000006_add_feature_table.up.sql`.
- Migrasi bisnis khusus contoh harus memakai segmen `example_`, misalnya
  `000007_example_create_sample_table.up.sql`.
- Hindari mengubah migrasi yang sudah pernah diterapkan di lingkungan bersama.
  Tambahkan migrasi baru untuk perubahan lanjutan.
- Rancang perubahan produksi dengan pola expand/contract bila beberapa versi
  aplikasi dapat berjalan bersamaan saat rollout.
- Pertimbangkan lock, index, durasi DDL, ukuran tabel, kompatibilitas rollback,
  dan backup sebelum meminta migrasi diterapkan.
- Tes migrasi pada PostgreSQL melalui integration test. Build migrator tidak
  membuktikan bahwa SQL berhasil dieksekusi.

Jalankan migrator hanya terhadap database development yang telah dikonfirmasi:

```sh
DB_PRIMARY_DSN='postgres://...' go run ./cmd/migrate
```

Jangan menaruh DSN nyata ke terminal yang terekam, issue, log, atau dokumentasi.

## Pengujian dan pemeriksaan

Mulai dengan tes package terkait, lalu jalankan pemeriksaan yang sesuai dengan
perubahan:

```sh
go test ./internal/pkg/<package>
go test ./...
make test-integration
make test-example-integration
make lint
make build
git diff --check
```

Gunakan tes paling sempit selama iterasi, tetapi jalankan `go test ./...`,
`make lint`, dan build sebelum mengajukan pull request bila perubahan
memengaruhi kode. Untuk perubahan migrasi, repository, konektivitas, atau
transaksi, jalankan integration test yang relevan juga.

Jika mengubah aturan konfigurasi, uji nilai default, nilai valid, serta input
tidak valid. Jika mengubah middleware atau package generik, pertahankan tes
regresi untuk perilaku lama dan tambahkan kasus untuk perilaku baru.

## Kualitas implementasi

- Ikuti format Go (`gofmt`) dan pola package yang sudah ada.
- Gunakan `context.Context` untuk operasi I/O dan hormati pembatalan/deadline.
- Bungkus error dengan konteks menggunakan `%w` agar pemanggil tetap dapat
  memeriksanya lewat `errors.Is` atau `errors.As`.
- Tutup resource dan tangani error penutupan jika dapat memengaruhi hasil
  operasi.
- Jangan mencatat credential, token, payload sensitif, atau data pribadi.
- Hindari label metric dengan cardinality tinggi seperti ID resource atau path
  mentah yang berisi identifier.
- Jangan menjadikan cache atau read replica sebagai sumber authoritative untuk
  keputusan kritis.
- Dokumentasikan perubahan konfigurasi, kontrak, perilaku fail-open/fail-closed,
  dan kebutuhan operasi.

## Checklist pull request

Sebelum meminta review, pastikan:

- [ ] Perubahan menyelesaikan satu tujuan yang jelas dan tidak mencampur
      refactor yang tidak terkait.
- [ ] Infrastruktur reusable tidak bergantung pada domain contoh.
- [ ] Kode contoh diberi nama dan build tag yang sesuai serta tidak masuk build
      default.
- [ ] Error, timeout, shutdown, concurrency, dan perilaku saat dependency gagal
      telah dipertimbangkan.
- [ ] Tes unit, tes integrasi yang relevan, lint, dan build berhasil.
- [ ] Dokumentasi, README, `.env.example`, serta manifest diperbarui jika
      kontrak atau konfigurasi berubah.
- [ ] Tidak ada credential, file hasil build, perubahan generated yang tidak
      diperlukan, atau perubahan worktree lain yang ikut masuk.
- [ ] Deskripsi PR menjelaskan konteks, pendekatan, validasi, dampak operasional,
      serta langkah migrasi jika diperlukan.

