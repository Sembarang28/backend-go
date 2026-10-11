# Panduan Deployment dan Operasional Infrastruktur

Dokumen ini ditujukan untuk tim platform/infrastruktur yang membangun image,
men-deploy service ke Kubernetes, menghubungkan monitoring, serta menjalankan
operasional dan troubleshooting.

> **Status manifest:** berkas di `deployments/k8s/` adalah contoh dasar, bukan
> paket produksi siap-terapkan tanpa penyesuaian. Sesuaikan dependency,
> manifest, dan secret dengan kebutuhan aplikasi yang akan dibangun. Bagian
> [Batasan deployment yang diketahui](#batasan-deployment-yang-diketahui)
> wajib diperiksa sebelum rollout produksi.

## Ringkasan service

| Komponen | Port | Kegunaan |
| --- | ---: | --- |
| HTTP | 8080 | API, health probes, dan endpoint Prometheus |
| gRPC | 50051 | Endpoint gRPC internal |
| pprof | 6060 | Diagnostik runtime Go; server terpisah, bind default ke loopback |

Endpoint HTTP:

| Endpoint | Kegunaan | Perilaku |
| --- | --- | --- |
| `/healthz/live` | Liveness/startup probe | Mengembalikan HTTP 200 selama handler hidup |
| `/healthz/ready` | Readiness probe | HTTP 503 jika aplikasi ditandai belum siap atau checker terdaftar gagal |
| `/metrics` | Scrape Prometheus | Ekspos metrik Prometheus dalam format teks |

Readiness memeriksa checker yang didaftarkan aplikasi: primary PostgreSQL,
setiap replica PostgreSQL yang dikonfigurasi, serta Redis dan NATS jika
koneksinya berhasil saat startup. Primary dan seluruh replica PostgreSQL harus
sehat agar readiness `UP`. Jika koneksi awal Redis atau NATS gagal, aplikasi
mencatat peringatan dan melanjutkan startup tanpa mendaftarkan checker
dependency tersebut. Akibatnya, readiness dapat tetap `UP` walaupun Redis atau
NATS tidak tersedia saat startup.

Koneksi PostgreSQL dibuka hanya jika `DB_PRIMARY_DSN` diberikan. Untuk operasi
baca/tulis, `DBGroup.Write()` mengarah ke primary dan `DBGroup.Read()` memilih
replica secara round-robin; jika tidak ada replica, pembacaan diarahkan ke
primary. Replica yang gagal saat startup membuat startup gagal. Kegagalan replica
setelah startup akan membuat readiness gagal, bukan otomatis mengeluarkan
replica tersebut dari pemilihan round-robin.

## Prasyarat sebelum deployment

1. Sediakan cluster Kubernetes dengan dukungan `Deployment`, `Service`,
   `HorizontalPodAutoscaler` API `autoscaling/v2`, `PodDisruptionBudget`, dan
   `NetworkPolicy`.
2. Siapkan PostgreSQL, Redis, dan NATS sesuai fitur yang akan digunakan.
   Sediakan DNS/network route, TLS, autentikasi, backup, dan monitoring untuk
   layanan tersebut di luar manifest aplikasi ini.
3. Buat namespace dan sesuaikan namespace pada setiap manifest. Contoh bawaan
   menggunakan `default`.
4. Bangun dan dorong image ke registry internal. Gunakan tag immutable atau
   digest untuk release; jangan mengandalkan `latest` di produksi.
5. Sesuaikan pod labels dan namespace selector di NetworkPolicy dengan label
   yang benar-benar dipakai cluster, termasuk ingress controller dan Prometheus.
6. Tentukan secret management. Jangan menaruh DSN, password, atau token di
   ConfigMap maupun Git.

## Build dan rilis image

Dockerfile berada di `deployments/docker/Dockerfile`. Pola build dan push yang
umum:

```sh
docker build -f deployments/docker/Dockerfile -t registry.example.com/backend-go:<tag> .
docker push registry.example.com/backend-go:<tag>
```

Ganti image pada `deployment.yaml` dan `job-migration.yaml` dengan tag release
atau digest yang sama. Konfigurasikan autentikasi registry cluster
(`imagePullSecrets`) bila diperlukan.

Dockerfile membangun package `./cmd/api` dan binary migrator `./cmd/migrate`.
Pastikan CI membangun kedua target dan memakai image yang sama untuk Job migrasi
serta Deployment aplikasi.

## Konfigurasi dan secret

Entry point membaca environment variables melalui `internal/config`. Nilai
konfigurasi utama:

| Variabel | Default | Catatan |
| --- | --- | --- |
| `APP_ENV` | `development` | Gunakan `production` untuk Gin release mode |
| `SERVER_ADDRESS` | `127.0.0.1:8080` | Di container harus bind ke `:8080` atau `0.0.0.0:8080` |
| `GRPC_ADDRESS` | `127.0.0.1:50051` | Terdefinisi di config, tetapi listener saat ini memakai `:50051` secara langsung |
| `PPROF_ADDRESS` | `127.0.0.1:6060` | Loopback default; tidak dapat discrape dari pod lain |
| `LOG_LEVEL` | `info` di production | Pilihan: `debug`, `info`, `warn`, `error` |
| `READ_TIMEOUT` | `10s` | Timeout pembacaan HTTP |
| `WRITE_TIMEOUT` | `10s` | Timeout penulisan HTTP |
| `SHUTDOWN_TIMEOUT` | `15s` | Batas waktu keseluruhan konfigurasi shutdown |
| `DB_PRIMARY_DSN` | kosong | DSN primary PostgreSQL; jika diisi koneksi gagal akan menghentikan startup |
| `DB_REPLICA_DSN` | kosong | Satu DSN replica untuk kompatibilitas lama |
| `DB_REPLICA_DSNS` | kosong | Daftar DSN replica dipisahkan koma; jika diatur, mengungguli `DB_REPLICA_DSN` |
| `DB_MAX_OPEN_CONNS` | `25` | Batas koneksi terbuka per pool primary/replica |
| `DB_MAX_IDLE_CONNS` | `10` | Batas koneksi idle per pool |
| `DB_CONN_MAX_LIFETIME` | `30m` | Masa hidup maksimum koneksi pada pool |
| `DB_CONN_MAX_IDLE_TIME` | `5m` | Masa idle maksimum koneksi pada pool |
| `REDIS_MODE` | `single` | Pilihan `single` atau `cluster` |
| `REDIS_ADDRESSES` | alamat `REDIS_ADDRESS` atau `127.0.0.1:6379` | Satu alamat untuk mode single; satu atau lebih seed address dipisahkan koma untuk cluster. Nilai kosong eksplisit menonaktifkan client Redis |
| `REDIS_ADDRESS` | `127.0.0.1:6379` | Nama variabel lama untuk satu alamat Redis; gunakan `REDIS_ADDRESSES` untuk konfigurasi baru |
| `REDIS_PASSWORD` | kosong | Password Redis; disediakan ke client single maupun cluster |
| `REDIS_DB` | `0` | Database logis Redis single; Redis Cluster mewajibkan nilai `0` |
| `CACHE_TTL` | `5m` | TTL cache repository |
| `NATS_ADDRESS` | `nats://127.0.0.1:4222` | Kosongkan untuk tidak mencoba koneksi NATS; default localhost tidak cocok untuk service Kubernetes |
| `OUTBOX_PARTITIONS` | `8` | Jumlah partition worker dan penentuan partition event |
| `OUTBOX_BATCH_SIZE` | `100` | Batas batch fetch worker |
| `OUTBOX_POLL_INTERVAL` | `200ms` | Frekuensi polling outbox |
| `OUTBOX_MAX_RETRIES` | `5` | Percobaan sebelum event dipindahkan ke DLQ |
| `IDEMPOTENCY_TTL` | `24h` | TTL response idempotency Redis |
| `RATE_LIMIT_REQUESTS` | `100` | Batas request pada sliding-window rate limiter |
| `RATE_LIMIT_WINDOW` | `1m` | Jendela rate limit |

ConfigMap contoh `deployments/k8s/configmap.yaml` mengatur mode Redis single
dan alamat service contoh. Untuk Redis Cluster, ubah ke seed addresses dan
`REDIS_MODE: cluster`; set `REDIS_DB: "0"` karena Redis Cluster tidak mendukung
database logis selain database 0. DSN primary
PostgreSQL, daftar DSN replica, dan password Redis harus berasal dari secret
manager cluster. Buat Kubernetes Secret `backend-go-secrets` dari secret manager
cluster, tetapi perhatikan bahwa referensi Secret di Deployment saat ini
`optional: true`. Untuk dependency wajib, ubah menjadi mandatory dan pastikan
semua key tersedia sebelum rollout.

Jangan memasukkan data sensitif pada command line, dokumentasi, atau output
pipeline CI. Batasi RBAC pembacaan Secret dan rotasikan kredensial sesuai
kebijakan organisasi.

## Urutan deployment Kubernetes

### 1. Tinjau dan sesuaikan manifest

Manifest dasar:

- `configmap.yaml`: konfigurasi non-rahasia.
- Secret eksternal/Secret Kubernetes: DSN dan kredensial.
- `deployment.yaml`: aplikasi, probes, resource requests/limits, dan
  Prometheus annotations.
- `service.yaml`: ClusterIP untuk port HTTP dan gRPC.
- `networkpolicy.yaml`: ingress dan egress.
- `hpa.yaml`: autoscaling berbasis CPU, memory, dan external metric.
- `pdb.yaml`: availability saat voluntary disruption.
- `job-migration.yaml`: menjalankan binary migrasi terhadap PostgreSQL primary.

### 2. Terapkan konfigurasi dan jaringan

Setelah namespace, labels, image, ConfigMap, dan referensi Secret disesuaikan:

```sh
kubectl apply -f deployments/k8s/configmap.yaml
kubectl apply -f deployments/k8s/networkpolicy.yaml
```

Pastikan policy cluster mendukung NetworkPolicy dan mengizinkan:

- HTTP ingress dari ingress controller dan Prometheus.
- gRPC ingress dari service/pod yang berwenang.
- Egress DNS serta port PostgreSQL, Redis, dan NATS sesuai alamat aktual.
- Pprof hanya dari jalur admin/diagnostik yang dibatasi.

Policy contoh membuka egress berdasarkan port tanpa membatasi tujuan IP/pod.
Perketat egress ke namespace, pod selector, atau CIDR layanan dependency sesuai
topologi cluster.

### 3. Migrasi skema

Jalankan migrasi database sebagai langkah terkontrol sebelum rollout aplikasi
yang membutuhkan skema baru. Verifikasi versi migrasi dan backup/restore sesuai
prosedur tim database.

Job menjalankan `/app/migrate`, yang membaca `DB_PRIMARY_DSN` dan menjalankan
migrasi embedded secara berurutan. Pastikan Secret tersedia (referensi Secret
di manifest saat ini `optional: true`), batasi izin Job pada primary, pantau
status Job, dan jangan menjalankan migrasi destruktif tanpa backup/rollout plan.

### 4. Terapkan workload dan autoscaling

Setelah image build, secret, migrasi, probe, dan selector diverifikasi:

```sh
kubectl apply -f deployments/k8s/deployment.yaml
kubectl apply -f deployments/k8s/service.yaml
kubectl apply -f deployments/k8s/pdb.yaml
kubectl apply -f deployments/k8s/hpa.yaml
kubectl rollout status deployment/backend-go
```

Perintah di atas mengasumsikan namespace saat ini sama dengan namespace
manifest. Tambahkan `-n <namespace>` bila tidak.

Manifest awal menetapkan 3 replica, request CPU `250m`/memory `256Mi`, limit
CPU `1000m`/memory `1Gi`, dan HPA 3–20 replica. Ini hanya titik awal; sesuaikan
dengan beban hasil uji performa dan kapasitas PostgreSQL, Redis, serta NATS.
PDB `minAvailable: 2` juga mengasumsikan minimal 3 replica siap.

## Probes dan shutdown

- Startup/liveness probes memanggil `/healthz/live` pada port 8080.
- Readiness probe memanggil `/healthz/ready`; readiness mengecek primary dan
  seluruh replica PostgreSQL serta dependency lain yang terdaftar, dengan
  timeout checker 2 detik.
- Jangan gunakan liveness untuk memeriksa dependency eksternal: liveness hanya
  menjawab apakah proses masih hidup. Gangguan sementara dependency sebaiknya
  memengaruhi readiness, bukan memicu restart massal.
- Saat menerima SIGTERM/SIGINT, shutdown engine menunggu drain delay 2 detik,
  lalu menjalankan beberapa fase: menurunkan readiness, menghentikan listener,
  menghentikan worker, dan menutup client.
- `terminationGracePeriodSeconds` manifest adalah 30 detik. Pastikan nilai ini
  lebih panjang dari drain delay ditambah batas shutdown aplikasi. Config
  `SHUTDOWN_TIMEOUT` saat ini 15 detik.
- Shutdown phase pertama menurunkan readiness setelah drain delay. Endpoint
  dapat tetap dianggap siap selama jendela drain tersebut; sesuaikan perilaku
  bila cluster membutuhkan deregistrasi lebih awal.
- `SHUTDOWN_TIMEOUT` memberi context deadline kepada task shutdown, tetapi
  `grpc.Server.GracefulStop()` tidak menggunakan context tersebut. Uji durasi
  drain dengan workload nyata dan siapkan batas waktu/strategi paksa untuk
  gRPC sebelum mengandalkan `terminationGracePeriodSeconds` sebagai batas keras.

## Prometheus dan metrik

Deployment memberi annotation scrape:

```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "8080"
prometheus.io/path: "/metrics"
```

Prometheus harus dikonfigurasi untuk menemukan pod berdasarkan annotation
tersebut atau melalui `PodMonitor`/`ServiceMonitor` yang sesuai dengan
instalasi. Pastikan NetworkPolicy mengizinkan namespace monitoring mengakses
port 8080. Endpoint `/metrics` tersedia melalui HTTP service/pod; jangan
mengekspos endpoint ini ke internet publik tanpa kebutuhan dan kontrol akses.

Metrik yang saat ini direkam middleware:

| Nama | Jenis/label | Pemakaian |
| --- | --- | --- |
| `http_requests_total` | Counter: `method`, `path`, `status` | Volume request HTTP dan error berdasarkan status |
| `http_request_duration_seconds` | Histogram: `method`, `path` | Latensi HTTP, dapat digunakan untuk p50/p95/p99 |
| `http_request_errors_total` | Counter: `method`, `path`, `code` | Request HTTP dengan status 400 atau lebih |
| `grpc_requests_total` | Counter: `service`, `method`, `code` | Volume request gRPC; implementasi saat ini mengisi label `service` dan `method` dengan full method yang sama |
| `grpc_request_duration_seconds` | Histogram: `service`, `method` | Latensi gRPC; label saat ini juga sama-sama berisi full method |

Contoh PromQL untuk panel dasar:

```promql
sum by (status) (rate(http_requests_total[5m]))
```

```promql
histogram_quantile(
  0.95,
  sum by (le) (rate(http_request_duration_seconds_bucket[5m]))
)
```

```promql
sum(rate(http_request_errors_total[5m]))
/
clamp_min(sum(rate(http_requests_total[5m])), 1)
```

Gunakan panel/alert per service dan route yang stabil. Middleware menggunakan
route template Gin bila tersedia, tetapi fallback ke URL path mentah untuk
route yang tidak terdaftar; pastikan tidak ada identifier dinamis ber-cardinality
tinggi yang masuk sebagai label.

### Metrik yang belum aktif

Metrik pool PostgreSQL yang sudah diperbarui collector:

- `db_open_connections`
- `db_idle_connections`
- `db_in_use_connections`
- `db_wait_count_total`

Package metrics juga mendefinisikan metrik outbox berikut. Worker outbox sudah
tersedia sebagai package, tetapi collector belum memperbarui metrik ini:

- `outbox_unprocessed_events_count`
- `outbox_processing_latency_seconds`
- `outbox_dlq_events_total`

Collector entry point memperbarui metrik pool DB untuk setiap koneksi
PostgreSQL, dengan label `role="primary"` atau `role="replica-N"`, setiap 15
detik. Metrik DB tidak memiliki sample jika PostgreSQL tidak dikonfigurasi.
Metrik outbox belum diperbarui oleh entry point; jangan membuat alert
berdasarkan metrik tersebut sebelum collector diimplementasikan.
Metrik gRPC juga memiliki kesalahan pemetaan label yang perlu diperbaiki
sebelum membuat dashboard dengan pecahan service/method terpisah.

Metric `http_requests_total` adalah counter, bukan nilai request-per-second
langsung. External metric di `hpa.yaml` bernama `http_requests_total` dan
memerlukan Metrics Adapter serta pemetaan external metric yang benar. Jangan
menerapkan scaling rule tersebut tanpa konfigurasi adapter dan normalisasi
rate yang sesuai; autoscaling CPU/memory dapat digunakan sebagai baseline.

## pprof dan diagnostik

pprof berjalan pada server terpisah, default `127.0.0.1:6060`, dengan endpoint
`/debug/pprof/`. Karena bind default loopback, pod lain tidak dapat mengaksesnya.
Jika perlu mengambil profile:

1. Aktifkan bind pada interface pod melalui konfigurasi `PPROF_ADDRESS` yang
   sesuai.
2. Batasi akses menggunakan NetworkPolicy dan jalur admin sementara.
3. Gunakan `kubectl port-forward` hanya untuk akses insidental yang telah
   diotorisasi, lalu ambil `/debug/pprof/heap` atau
   `/debug/pprof/profile?seconds=30`.
4. Kembalikan akses ke konfigurasi aman setelah investigasi.

Jangan mengekspos pprof ke ingress publik; endpoint ini dapat mengungkap detail
runtime dan mengonsumsi resource signifikan saat profiling CPU.

## Log dan alert operasional

Aplikasi menulis structured JSON log ke stdout. Kumpulkan melalui logging agent
cluster dan kaitkan dengan metadata pod, namespace, image digest, serta trace ID.
Gunakan alert yang mencakup:

- kenaikan rasio error HTTP/gRPC dan p95/p99 latency;
- pod restart, readiness gagal, rollout yang macet, dan CPU/memory mendekati
  limit;
- kegagalan scrape Prometheus atau target yang hilang;
- saturation/error koneksi PostgreSQL, Redis, dan NATS dari monitoring
  dependency masing-masing;
- error log terkait publish outbox atau rekonsiliasi, bila fitur tersebut
  benar-benar diaktifkan.

Adapter PostgreSQL untuk outbox tersedia, tetapi aplikasi perlu mengaktifkan
dan menghubungkannya sesuai kebutuhan. Log publish/reconciliation dapat
dikumpulkan dari stdout jika komponennya diaktifkan; metrik backlog dan DLQ
belum tersedia sampai collector outbox diimplementasikan.

## Runbook singkat

### Rollout gagal atau pod tidak Ready

```sh
kubectl get pods -l app.kubernetes.io/name=backend-go
kubectl describe pod <pod>
kubectl logs <pod> --previous
kubectl get events --sort-by=.lastTimestamp
```

Periksa image pull, binding `SERVER_ADDRESS`, status `/healthz/ready`, Secret,
NetworkPolicy, serta konektivitas dependency. Liveness `/healthz/live` selalu
200 selama handler dapat dijalankan sehingga tidak menunjukkan readiness
dependency.

### Target Prometheus tidak terlihat

Periksa annotation/selector scrape, namespace monitoring, NetworkPolicy ingress
port 8080, lalu akses `/metrics` dari jaringan cluster. Pastikan Prometheus
memang mendukung discovery melalui annotation atau pasang `ServiceMonitor` /
`PodMonitor` sesuai operator yang digunakan.

### Scale atau koneksi dependency bermasalah

Bandingkan replica aplikasi dan request rate dengan kapasitas database/broker.
Naikkan replica aplikasi tidak selalu menaikkan throughput jika pool koneksi
atau dependency sudah jenuh. Setiap replica aplikasi membuka pool primary dan
pool untuk masing-masing replica PostgreSQL. Perkiraan batas koneksi:
`jumlah pod × (DB_MAX_OPEN_CONNS × (1 + jumlah replica DB))`, ditambah koneksi
migrasi dan workload lain. Atur limit pool berdasarkan batas koneksi server DB.
Read routing saat ini round-robin, bukan health-aware; jika replica DB gagal
setelah startup, readiness gagal tetapi query pembacaan masih bisa memilih
replica tersebut sampai pod di-restart.

## Batasan deployment yang diketahui

Sebelum rollout, tim platform/aplikasi perlu menilai dan menangani gap berikut:

1. Read routing PostgreSQL saat ini tidak mengeluarkan replica yang gagal
   setelah startup; sediakan mekanisme failover/health-aware routing bila
   workload memerlukannya.
2. Publisher NATS saat ini menggunakan NATS Core tanpa durable acknowledgement.
   Jika aplikasi memerlukan jaminan durabilitas setelah publish, gunakan adapter
   JetStream atau broker dengan acknowledgement yang sesuai.
3. Implementasikan collector metrik outbox dan pantau ukuran backlog/DLQ
   sebelum menjadikan metrik tersebut dasar alert atau HPA.
4. Perbaiki gRPC metric label dan atur pprof bind/security serta HPA external
   metric agar sesuai dengan
   platform observability cluster.
