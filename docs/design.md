# Desain issboard

Catatan desain untuk dashboard kesehatan host berbasis ZFS + Docker.
Dokumen ini menjelaskan **kenapa** bentuknya begini — bukan cara memakainya
(itu di [README](../README.md)).

Ditulis generik dengan sengaja: repo ini publik, jadi tidak ada hostname,
alamat IP, atau nama pool milik mesin nyata di sini.

---

## 1. Masalah yang diselesaikan

Server rumahan berbasis ZFS punya beberapa kondisi yang **diam-diam merusak**
dan tidak akan pernah muncul sendiri di layar:

- Pool yang **belum pernah di-scrub.** Mirror tanpa scrub cuma redundansi di
  atas kertas — bit rot baru ketahuan saat resilver, di momen paling rawan.
- Pool yang ternyata **stripe, bukan mirror.** Mudah terjadi kalau disk kedua
  ditambahkan dengan `zpool add` (bukan `attach`). Satu disk mati, pool hilang.
- **Reallocated sector yang merangkak naik** pada disk tua.
- **Container hidup tapi tanpa network** — statusnya `Up`, tapi tak terjangkau.
- **Port database ter-*publish* ke `0.0.0.0`** tanpa disadari.
- Timer pengumpul metrik yang **mati diam-diam**.

Semuanya bisa dijawab satu perintah. Masalahnya bukan ketiadaan perintah, tapi
**tidak ada yang menjalankannya secara rutin**. Satu halaman yang menampilkan
semuanya sekaligus mengubah "harus ingat memeriksa" jadi "kelihatan saat lewat".

## 2. Yang **tidak** dikerjakan

Batas ini menjaga proyeknya tetap kecil:

- **Bukan sistem monitoring.** Tidak ada time-series, tidak ada grafik riwayat,
  tidak ada scraping. Kalau butuh itu, Prometheus + Grafana sudah ada dan
  jauh lebih baik.
- **Bukan sistem alert.** Lihat keputusan socket activation di bawah — dashboard
  ini secara struktural tidak bisa jadi sumber alert, dan itu disengaja.
- **Bukan pengganti Cockpit atau Portainer.** Tidak ada terminal, tidak ada
  manajemen container.
- **Bukan metrik aplikasi.** Kesehatan *host*, bukan kesehatan app di atasnya.

## 3. Tiga keputusan yang membentuk sisanya

### 3.1 Satu binary Go, di host, bukan container

Dashboard kesehatan harus **tetap hidup justru saat Docker bermasalah**.
Container yang membaca `zfs`, `smartctl`, dan socket Docker butuh `privileged`
plus mount host — meniadakan isolasinya sambil menambah kerumitan, demi
menempatkan alat diagnosa di dalam benda yang mungkin sedang rusak.

Go dipilih karena satu binary statis berarti **tidak ada dependency runtime
yang bisa ikut rusak** saat sistem sedang bermasalah — persis saat dashboard
paling dibutuhkan. Tidak ada `node_modules`, venv, atau interpreter di server.
Frontend ikut ter-*embed* lewat `//go:embed`, jadi deploy = salin satu file.

Konsekuensinya: **nol dependensi di luar pustaka standar**, termasuk untuk
parsing config dan protokol socket activation. Keduanya cukup kecil untuk
ditulis langsung, dan itu lebih murah daripada rantai dependensi.

### 3.2 Socket activation, bukan daemon

Yang di-`enable` adalah **`issboard.socket`, bukan `issboard.service`** —
pola yang sama dengan `cockpit.socket`. systemd yang memegang port; prosesnya
baru dinyalakan saat ada koneksi pertama, dan **keluar sendiri setelah idle**.

Untuk dashboard yang dibuka beberapa kali sehari, ini jelas benar: nol RAM dan
nol CPU saat tidak dipakai, tidak ada daemon lama-jalan yang bisa bocor memori,
dan permukaan serangannya hilang saat tidak ada yang melihat.

> **Konsekuensi yang diterima sadar, bukan dilupakan:**
>
> **Tidak ada poll latar belakang.** Data dikumpulkan saat halaman terbuka dan
> di-cache selama proses hidup.
>
> **Dashboard ini tidak bisa jadi sumber alert.** Kalau sesuatu rusak dan
> halamannya tidak pernah dibuka, tidak ada yang tahu. Kalau notifikasi
> diinginkan, itu **unit systemd terpisah** yang kecil dan tetap jalan berkala
> — **jangan** tukar keputusan socket activation demi itu.

### 3.3 `smartctl` tidak pernah dipanggil di jalur request

Ini larangan keras, dan alasannya fisik: **`smartctl` membangunkan HDD yang
sedang standby.**

Digabung dengan socket activation, memanggilnya per-request berarti satu kali
buka halaman dari HP = seluruh HDD bangun. Dibuka sepuluh kali sehari, disk tua
tidak pernah sempat tidur — dashboard yang dimaksudkan menjaga kesehatan disk
justru memperpendek umurnya.

Karena itu SMART dikumpulkan **unit systemd milik root yang terpisah**, tiap
6 jam, dengan `-n standby` supaya disk yang tidur dibiarkan tidur. Hasilnya
ditulis atomik ke sebuah file JSON, dan issboard **hanya membaca file itu**.

Kebetulan solusi yang sama juga menyelesaikan masalah lain: `smartctl` butuh
root, sedangkan issboard sengaja tidak jalan sebagai root. Dua alasan berbeda,
satu jawaban.

**Cache yang basi ditandai di UI.** Timer yang mati adalah temuan tersendiri,
bukan sekadar ketiadaan data.

## 4. Interval pengambilan data

"Berkala" di sini berarti **selama halaman terbuka saja**.

| Data | Interval | Alasan |
|---|---|---|
| Host / ARC | 15 detik | dibaca dari `/proc`, hampir gratis |
| Container | 30 detik | satu panggilan ke socket Docker |
| Pool & dataset ZFS | 60 detik | murah, dilayani dari ARC |
| SMART | 6 jam, **di unit root terpisah** | membangunkan disk; issboard hanya membaca cache |

## 5. Struktur

```
main.go                 wiring, socket activation, idle-exit
internal/config/        config sederhana "kunci: nilai", tanpa dependensi
internal/collector/
  collector.go          cache per-bagian dengan TTL masing-masing
  zfs.go                zpool list/status, zfs list, hitung snapshot
  smart.go              BACA cache JSON — tidak pernah memanggil smartctl
  docker.go             socket Docker lewat unix transport
  host.go               /proc + /proc/spl/kstat/zfs/arcstats
internal/api/           GET /api/v1/*
web/                    vanilla, ter-embed, tanpa build step
systemd/                socket + service + timer SMART
libexec/                pengumpul SMART milik root
```

**Kegagalan per-bagian tidak menggagalkan seluruh respons.** Setiap collector
yang gagal menaruh pesannya di `errors[]`, dan sisanya tetap disajikan. Di
mesin tanpa ZFS atau tanpa Docker, issboard tetap jalan dan berguna — itu
bukan kasus tepi, itu cara mengembangkannya.

## 6. API

Semua JSON. **v1 hanya `GET`**, tapi router sengaja **tidak dikunci** ke GET
saja: endpoint yang bermutasi akan menyusul, dan bentuknya sudah disiapkan
supaya tidak perlu dibongkar.

| Endpoint | Isi |
|---|---|
| `GET /api/v1/health` | liveness, tanpa mengumpulkan apa pun |
| `GET /api/v1/status` | snapshot penuh: host, pools, datasets, containers, smart |

## 7. Keamanan

**Aturan tetap: batasi akses, bukan kemampuan.** Dashboard yang dilumpuhkan
sampai tidak berguna akan diganti orang dengan shell — dan shell itu jauh
lebih berbahaya daripada dashboard yang dirancang benar.

Penerapannya di v1:

- **Bind ke loopback saja.** Akses dari luar lewat SSH tunnel atau reverse
  proxy yang punya autentikasi sendiri. Jangan pernah ditaruh langsung di LAN
  atau VPN mesh.
- **Tidak ada autentikasi bawaan** di v1 — disengaja, karena read-only di balik
  loopback. **Begitu ada satu endpoint yang bermutasi, autentikasi wajib lebih
  dulu**, bukan menyusul.
- Jalan sebagai **user sistem sendiri**, bukan root, dengan pengerasan systemd
  (`ProtectSystem=strict`, `NoNewPrivileges`, `SystemCallFilter`, dan seterusnya).
- ⚠️ Keanggotaan grup `docker` **setara root** di kebanyakan sistem. Grup itu
  diberikan hanya untuk membaca socket; pembatas sebenarnya di v1 adalah
  **tidak adanya jalur mutasi sama sekali.**

### Kalau nanti ada CRUD

Dua hal yang harus dipegang sejak sekarang, karena mahal kalau ditambal
belakangan:

1. **Hak akses per kebutuhan, bukan `NOPASSWD: ALL`.** Godaannya besar saat
   menambahkan aksi pertama yang butuh root. Tuliskan perintah spesifiknya.
2. **Validasi nama dataset/pool dengan daftar-putih, bukan regex.** Regex
   meloloskan hal seperti `pool/app@../..`. Ambil daftar nyata dari sistem,
   cocokkan persis, tolak sisanya.

## 8. Status & yang belum ada

v1 berjalan dan menyajikan data nyata. Yang belum:

- Perbandingan konfigurasi snapshot (mis. `sanoid.conf`) dengan dataset nyata
- Panel versi app + deteksi drift antara config dan container yang jalan
- Fase arsip & unduhan backup
- Test otomatis
- Autentikasi (lihat bagian 7)
