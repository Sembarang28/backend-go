# Panduan Developer: PostgreSQL Read Critical Path dan Replication Lag

Dokumen ini menjelaskan jalur baca PostgreSQL yang konsisten dan kapan query
aman diarahkan ke read replica. Untuk perilaku Redis, lihat [Panduan Redis
Replication Lag](./redis_replication_guide.md).

## PostgreSQL: model routing

`database.DBGroup` menyediakan dua jalur:

- `group.Write()` selalu mengembalikan koneksi primary untuk mutasi.
- `group.ReadPrimary()` mengembalikan primary secara eksplisit untuk critical
  reads.
- `group.Read()` memilih replica secara round-robin. Jika tidak ada replica,
  method ini memakai primary.

Pemilihan replica saat ini **tidak** memeriksa replication lag atau kesehatan
replica per query. Karena itu, `Read()` hanya cocok untuk operasi yang dapat
menerima data sedikit tertinggal. Jangan menganggap replica langsung
memperlihatkan hasil write terbaru.

## Kapan read replica aman digunakan

Gunakan `Read()` untuk data yang toleran terhadap eventual consistency,
misalnya:

- laporan atau dashboard yang tidak digunakan untuk keputusan transaksi;
- katalog atau konten yang perubahan terbarunya tidak harus langsung terlihat;
- agregasi dan query baca berat yang boleh menampilkan snapshot beberapa saat
  sebelumnya.

Pastikan UI dan kontrak API sesuai dengan kemungkinan data stale. Jangan
menyatakan operasi berhasil lalu menjanjikan hasilnya langsung muncul jika
read berikutnya diarahkan ke replica.

## Paksa primary untuk PostgreSQL critical path

Untuk kategori berikut, gunakan `ReadPrimary()` agar query baca memakai
primary:

- read-after-write dalam request atau workflow yang sama;
- pemeriksaan saldo, kuota, stok, status pembayaran, atau hak akses;
- validasi kondisi yang menentukan apakah mutasi boleh dilakukan;
- polling status setelah membuat atau mengubah resource;
- alur yang membutuhkan keputusan berdasarkan data yang baru dikomit.

Gunakan `ReadPrimary()` agar maksud critical read terlihat di code review:

```go
func GetResourceForCriticalDecision(
    ctx context.Context,
    dbGroup *database.DBGroup,
    resourceID string,
) (Resource, error) {
    var resource Resource
    err := dbGroup.ReadPrimary().QueryRowContext(
        ctx,
        `SELECT id, status FROM resources WHERE id = $1`,
        resourceID,
    ).Scan(&resource.ID, &resource.Status)
    if err != nil {
        return Resource{}, fmt.Errorf("read resource from primary: %w", err)
    }
    return resource, nil
}
```

Untuk read-after-write di dalam transaksi, lakukan write dan read melalui
transaksi primary yang sama. Jangan membaca lewat `group.Read()` atau membuka
koneksi replica sebelum transaksi dikomit. `ReadPrimary()` memastikan query
baca di luar transaksi memakai primary; transaksi yang sedang aktif tetap
harus dipakai untuk membaca perubahan yang belum dikomit.

```go
tx, err := dbGroup.Write().BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
    return fmt.Errorf("update resource: %w", err)
}

var status string
if err := tx.QueryRowContext(ctx, selectQuery, orderID).Scan(&status); err != nil {
    return fmt.Errorf("read updated order in primary transaction: %w", err)
}

if err := tx.Commit(); err != nil {
    return fmt.Errorf("commit order transaction: %w", err)
}
```

Tangani error transaksi secara eksplisit; jangan melanjutkan alur seolah-olah
data sudah tersimpan jika `Commit` gagal.

## Hindari workaround berbasis waktu

Jangan menambahkan `sleep` lalu mencoba membaca dari replica. Lag berubah-ubah
dan jeda tetap tidak membuktikan replica sudah menerapkan transaksi. Jangan
melakukan retry tanpa batas pada replica untuk mengejar data terbaru.

Jika suatu fitur perlu kembali membaca dari replica setelah write, rancang
mekanisme konsistensi secara eksplisit, misalnya mempertahankan pembacaan pada
primary selama request/workflow tersebut. Implementasi `DBGroup` saat ini
belum menyediakan session stickiness, pembacaan berdasarkan LSN, atau routing
otomatis berdasarkan ambang lag.

## Saat replication lag terjadi

1. **Jaga kebenaran lebih dulu.** Arahkan critical PostgreSQL reads ke
   `group.ReadPrimary()`. Jangan mengalihkan semua query ke primary secara
   otomatis tanpa memperhitungkan kapasitas koneksi dan beban.
2. **Periksa lag di PostgreSQL.** Pantau status streaming replication di
   primary (`pg_stat_replication`), termasuk `state`, `write_lag`,
   `flush_lag`, `replay_lag`, dan selisih WAL LSN. Di replica, periksa status
   receiver dan posisi replay. Nilai lag berbasis timestamp seperti
   `pg_last_xact_replay_timestamp()` dapat bernilai `NULL` atau menyesatkan
   ketika tidak ada transaksi baru; jangan jadikan satu nilai tersebut satu-
   satunya sinyal kesehatan.
3. **Pantau dampak aplikasi.** Bandingkan latency/error query primary dan
   replica, pool connections (`db_open_connections`,
   `db_in_use_connections`, `db_wait_count_total`), serta antrean/beban WAL.
   Atur alert berdasarkan SLO dan toleransi staleness fitur, bukan satu batas
   universal.
4. **Pulihkan replica dan kapasitas.** Cari penyebab seperti jaringan,
   disk/WAL throughput, query panjang, atau replica tertinggal. Jika beban
   primary meningkat, batasi atau kurangi query yang dipindahkan ke primary
   sesuai kebijakan layanan; jangan mengorbankan validasi critical path.
5. **Validasi sebelum kembali.** Pastikan replay mengejar primary dan data yang
   diperlukan sudah terlihat. Karena router saat ini tidak mengeluarkan
   replica tertinggal dari rotasi, keputusan untuk menghentikan atau
   memulihkan trafik replica perlu dilakukan oleh operator/infrastruktur.

Readiness memeriksa konektivitas database, bukan batas maksimum replication
lag. Replica yang dapat di-ping tetapi tertinggal masih dapat dipilih oleh
`Read()`.

## Checklist code review

- Apakah hasil query menentukan keputusan uang, stok, akses, atau mutasi?
  Gunakan PostgreSQL `ReadPrimary()`.
- Apakah query membaca data yang baru saja ditulis oleh request/workflow ini?
  Gunakan transaksi primary yang sama atau `ReadPrimary()`.
- Apakah data boleh stale dan kegagalan konsistensi hanya berdampak kosmetik?
  Replica PostgreSQL dapat digunakan sesuai toleransi staleness.
- Apakah pemanggil diam-diam memakai `Read()` sebagai default untuk semua
  query? Pisahkan jalur berdasarkan kebutuhan konsistensi.
- Apakah retry atau fallback yang dirancang berpotensi membebani primary?
  Tetapkan timeout, batas retry, dan metrik sebelum rollout.
