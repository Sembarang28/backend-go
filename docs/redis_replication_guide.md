# Panduan Developer: Redis Replication Lag

Panduan ini menjelaskan routing baca Redis, dampak replication lag, dan pilihan
untuk critical path. Panduan ini membahas client Redis yang dibuat oleh
`internal/pkg/redisclient`; bukan pengganti dokumentasi topologi managed Redis
yang digunakan oleh deployment.

## Mode client yang didukung

Konfigurasi aplikasi mendukung `REDIS_MODE=single` dan `REDIS_MODE=cluster`.
Alamat seed diatur melalui `REDIS_ADDRESSES`, dan password melalui
`REDIS_PASSWORD`.

### Single

Mode `single` membuat client Redis biasa dengan satu alamat. Client tidak
menentukan apakah endpoint tersebut primary atau replica. Karena itu,
`REDIS_ADDRESSES` harus mengarah ke primary atau endpoint/proxy yang menjamin
read-after-write; jangan arahkan ke replica read-only atau load balancer yang
merutekan perintah baca ke replica tertinggal.

### Cluster

Mode `cluster` membuat `redis.ClusterClient` dari seed addresses. Client saat
ini menetapkan `ReadOnly: false`, sehingga operasi baca normal diarahkan ke
master pemilik slot key, bukan ke replica. Jangan mengubahnya menjadi `true`
untuk data yang memerlukan read-after-write atau keputusan bisnis terkini.
Redis Cluster tetap melakukan failover master; replika cluster umumnya
mereplikasi secara asynchronous.

Redis Cluster tidak menyediakan logical database selain DB 0; oleh sebab itu
`REDIS_DB` wajib `0`.

## Memahami replication lag

Redis replication umumnya asynchronous. Setelah write dikonfirmasi oleh
primary, replica belum tentu langsung menerapkannya. Jika aplikasi membaca
dari replica, ia dapat menerima nilai lama, key yang belum ada, atau state
sebelum operasi terakhir.

Pada konfigurasi aplikasi saat ini:

- Cluster mode tidak mengaktifkan replica reads.
- Single mode memakai tepat endpoint yang diberikan konfigurasi; pilihan
  routing di balik DNS/proxy/load balancer merupakan tanggung jawab
  infrastruktur.
- Health checker Redis melakukan `PING`. Ini menguji keterjangkauan, bukan
  replication lag, konsistensi data, atau kesiapan replica.
- Belum ada metrik aplikasi khusus untuk Redis replication offset/lag.

## Critical path: jangan bergantung pada cache

Untuk data yang menentukan saldo, stok, pembayaran, hak akses, kuota, atau
transisi status:

1. Perlakukan PostgreSQL (atau datastore transaksional yang ditetapkan) sebagai
   source of truth.
2. Lewati pembacaan cache Redis dan muat data dari source of truth. Helper
   `cache.LoadWithoutCache` menjalankan loader tanpa membaca maupun menulis
   Redis.
3. Untuk PostgreSQL, gunakan `DBGroup.ReadPrimary()` atau transaksi primary
   yang sama. Baca panduan [PostgreSQL replication lag](./postgres_replication_guide.md)
   untuk contoh query critical dan pembacaan dalam transaksi.
4. Perbarui atau invalidasi cache setelah perubahan authoritative. Kegagalan
   cache boleh dicatat dan ditangani sesuai kebijakan, tetapi jangan
   mengembalikan cache sebagai jawaban yang seolah-olah authoritative untuk
   keputusan kritis.

Contoh melewati cache:

```go
resource, err := cache.LoadWithoutCache(ctx, func(ctx context.Context) (Resource, error) {
    var resource Resource
    err := dbGroup.ReadPrimary().QueryRowContext(
        ctx,
        `SELECT id, status FROM resources WHERE id = $1`,
        resourceID,
    ).Scan(&resource.ID, &resource.Status)
    if err != nil {
        return Resource{}, fmt.Errorf("read resource from PostgreSQL primary: %w", err)
    }
    return resource, nil
})
if err != nil {
    return err
}
```

Bypass cache hanya memastikan query mencapai loader. Loader tetap harus
mengarah ke source of truth; bypass tidak otomatis mengalihkan PostgreSQL
replica ke primary.

## Jika Redis sendiri menyimpan state authoritative

Jangan samakan “membaca dari master” dengan jaminan durability linearizable.
Primary routing menghindari pembacaan dari replica biasa, tetapi asynchronous
replication dapat menyebabkan write yang sudah di-acknowledge hilang saat
failover. Ini penting untuk Redis-backed locks, idempotency records, atau
rate-limit state apabila correctness bisnis bergantung pada tidak hilangnya
state tersebut.

- Jangan gunakan cache Redis sebagai satu-satunya penyimpanan untuk invariant
  finansial/transaksional.
- Untuk lock, rancang operasi bisnis agar tetap aman terhadap retry dan
  duplikasi; Redis lock sendiri tidak menggantikan constraint/transaksi pada
  source of truth.
- Untuk idempotency kritis, pastikan efek bisnis durable dan atomik di
  database; kehilangan cache idempotency tidak boleh menyebabkan efek
  non-idempotent yang tidak dapat dipulihkan.
- Jika deployment mengandalkan pengakuan replikasi (mis. `WAIT`) atau kebijakan
  durability tertentu, validasi semantik dan batasnya terhadap topologi Redis
  yang digunakan. Pengakuan replica tidak menjadikan Redis setara dengan
  transaksi PostgreSQL dan tidak meniadakan semua skenario failover.

## Diagnosis dan respons lag

1. **Tentukan topologi aktif.** Verifikasi mode aplikasi, endpoint yang
   di-resolve DNS, endpoint primary, role setiap node, dan apakah proxy
   merutekan read ke replica.
2. **Periksa state server.** Gunakan `INFO replication` pada primary dan
   replica. Bandingkan `master_repl_offset` dengan offset replica
   (`slave_repl_offset` pada versi yang masih menggunakan nama tersebut),
   serta periksa role, jumlah replica yang terhubung, dan status link.
   Untuk Redis Cluster, periksa setiap shard/master dan replica-nya; satu
   node sehat tidak membuktikan semua slot terkini.
3. **Bandingkan nilai aktual.** Untuk key yang aman dan terkontrol, lakukan
   verifikasi write/read pada endpoint primary dan replica. Hindari
   pemeriksaan yang mengubah data produksi atau mengekspos nilai sensitif.
4. **Lindungi critical path.** Jika aplikasi dikonfigurasi untuk replica
   reads melalui endpoint eksternal, hentikan routing tersebut untuk jalur
   critical. Untuk cache PostgreSQL, bypass cache dan baca primary.
5. **Periksa penyebab dan dampak.** Tinjau jaringan, CPU, disk/persistence,
   throughput, ukuran backlog replikasi, failover, latency command, dan error
   Redis. Pantau metrik server/managed service; metrik aplikasi saat ini hanya
   menunjukkan health ping dan bukan lag.
6. **Pulihkan secara bertahap.** Kembalikan replica reads hanya setelah
   offset/status dan perilaku data sesuai toleransi staleness aplikasi.
   Perubahan routing dan failover dilakukan melalui konfigurasi/operator
   deployment, bukan otomatis oleh `redis.UniversalClient`.

Jangan menambahkan `sleep` tetap atau retry tanpa batas untuk menunggu replica.
Lag bervariasi, dan pemeriksaan `PING` saja tidak menunjukkan bahwa data sudah
direplikasi.

## Checklist code review

- Apakah cache hit dapat memengaruhi uang, stok, hak akses, pembayaran, atau
  transisi state? Bypass cache dan baca source of truth.
- Apakah loader yang dipakai `LoadWithoutCache` memakai PostgreSQL replica?
  Alihkan ke `ReadPrimary()`.
- Apakah mode single menunjuk ke endpoint yang benar-benar memberi pembacaan
  konsisten?
- Apakah ada konfigurasi replica reads pada Redis Cluster? Jangan aktifkan
  untuk key critical.
- Apakah data Redis merupakan satu-satunya rekaman untuk efek bisnis yang
  tidak boleh hilang saat failover? Pindahkan invariant ke transaksi
  authoritative atau desain ulang recovery.
- Apakah tim memantau lag di sisi server/provider? `PING` dan health readiness
  bukan replication-lag monitoring.
