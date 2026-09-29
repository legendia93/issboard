# issboard

Dashboard kesehatan host untuk server rumahan berbasis **ZFS + Docker**:
satu binary Go statis, frontend ikut ter-*embed*, tanpa runtime apa pun di
server. Bisa juga **mengatur**: container (start/stop/restart/hapus), scrub
pool, SMART self-test, batas cache ARC, snapshot, dataset & propertinya, dan
sanoid — semuanya di balik login.

Yang ditampilkan: pool & dataset ZFS (termasuk **apakah pool pernah di-scrub**
dan **apakah benar-benar redundan**), **kebijakan snapshot dibandingkan dengan
dataset yang benar-benar ada**, ringkasan SMART tiap disk, daftar container
beserta port yang ter-*publish*, dan beban host + ARC.

Di atas semuanya ada **satu vonis**: sehat, atau sekian hal yang perlu diurus —
supaya pertanyaan yang sebenarnya dicari terjawab tanpa membaca satu kartu pun.
Vonis yang sama itulah yang dikirim ke HP oleh `issboard-agent`.

> **Status: v1, terpasang dan berjalan.** Di host sungguhan sejak 21 Agustus
> 2026, dengan notifikasi Telegram yang benar-benar terkirim dan pengingat
> 24 jam yang sudah terbukti berulang. Pemasangan pertama itu menemukan enam
> cacat yang tidak satu pun bisa muncul di mesin pengembangan; semuanya
> dicatat di [`docs/plan/05-pemasangan.md`](docs/plan/05-pemasangan.md).
>
> Notifikasi dan sparkline riwayat sudah ada, lewat
> [`issboard-agent`](#notifikasi--riwayat-issboard-agent) yang terpisah.
>
> Rencana yang sedang berjalan ada di [`docs/plan/`](docs/plan/00-index.md);
> alasan di balik bentuknya ada di [`docs/design.md`](docs/design.md).

## Kenapa begini

Tiga keputusan yang membentuk seluruh desainnya:

**Satu binary Go, di host, bukan container.** Dashboard kesehatan harus tetap
hidup justru saat Docker sedang bermasalah. Container yang membaca `zfs`,
`smartctl`, dan socket Docker butuh `privileged` + mount host — meniadakan
isolasinya sambil menambah kerumitan.

**Socket activation, bukan daemon.** Yang di-*enable* adalah
`issboard.socket`; systemd yang memegang port, prosesnya baru hidup saat
halaman dibuka dan keluar sendiri setelah idle. Nol RAM dan nol permukaan
serang saat tidak ada yang melihat. Konsekuensinya diterima sadar: **tidak ada
poll latar belakang, jadi dashboard ini bukan sumber alert.** Yang mengirim
notifikasi adalah [`issboard-agent`](#notifikasi--riwayat-issboard-agent),
unit bertimer terpisah — bukan issboard yang diubah jadi daemon.

**`smartctl` tidak pernah dipanggil di jalur request.** Memanggilnya
**membangunkan HDD yang sedang tidur**. Dengan socket activation, satu kali
buka halaman dari HP = seluruh disk bangun; dibuka sepuluh kali sehari, disk
tidak pernah sempat tidur. Karena itu SMART dikumpulkan unit root terpisah
tiap 6 jam (dengan `-n standby`) ke sebuah file JSON, dan issboard hanya
membaca file itu. Cache yang basi ditandai di UI — timer yang mati adalah
temuan tersendiri.

## Mencoba tanpa memasang apa pun

```bash
go build -o issboard .
./issboard -demo -config /dev/null
# lalu buka http://127.0.0.1:9955
```

Mode demo menyajikan data palsu dan **tidak menyentuh sistem sama sekali** —
tidak memanggil `zpool`, tidak membuka socket container, tidak membaca cache
SMART. Datanya sengaja memuat kondisi sakit (pool stripe, disk dengan
reallocated sector, container `Up` tanpa network, port database ter-*publish*
ke `0.0.0.0`) supaya tampilan peringatannya bisa dilihat tanpa menunggu hal itu
terjadi sungguhan.

Mode ini juga cara yang benar untuk **screenshot atau merekam layar**: halaman
sebenarnya menampilkan hostname, alamat IP, nama pool, dan nama app.

## Tampilan

Tema **Nothing OS**: off-white atau hitam pekat, garis 1px, tanpa gradient,
tanpa glow. Mengikuti tema sistem, dan bisa ditimpa lewat tombol **TEMA**.

Aturan warnanya satu kalimat: **sehat itu pastel, bermasalah itu pekat.**
Pastel dipakai sebagai *identitas* — pool ini yang mana — bukan sebagai status.
Warna pekat (amber, merah) hanya muncul kalau memang ada yang perlu diurus,
jadi layar yang sehat sepenuhnya tenang dan satu warna pekat langsung menarik
mata. Karena itu palet pastelnya tidak memuat kuning, oranye, atau merah sama
sekali.

Angka telemetri selalu memakai font mono, tidak pernah font dot-matrix: pada
font dot yang dipakai, `52` terbaca `92` dan `35` terbaca `39`. Font dot-matrix
hanya untuk kata.

Dirancang untuk layar HP lebih dulu — itu cara host ini paling sering dilihat.
Tidak ada aset dari luar: font ikut ter-*embed*, dan halaman disajikan dengan
`Content-Security-Policy: default-src 'self'`.

## Kebijakan snapshot

Dua pertanyaan yang tidak dijawab Cockpit maupun Portainer, dan dua-duanya
diam kalau jawabannya buruk:

1. **Dataset mana yang tidak tercakup aturan snapshot apa pun.** Dataset baru
   tidak ikut sendiri ke `sanoid.conf`. Yang membuatnya berbahaya bukan
   kelalaiannya, tapi tidak adanya satu pun pesan saat itu terjadi: dataset
   yang tidak terlindungi terlihat persis sama dengan yang terlindungi, sampai
   hari orang membutuhkan snapshot-nya.
2. **Dataset yang tercakup tapi snapshot-nya sudah berhenti.** Timer bisa saja
   `active` sementara tidak menghasilkan apa pun sama sekali.

issboard hanya **membaca** `sanoid.conf`; ia tidak pernah memanggil sanoid dan
tidak pernah membuat snapshot. Yang dibandingkan adalah *apa yang tertulis*
lawan *apa yang ada di ZFS*.

Ambang "sudah terlambat" **diturunkan dari retensi yang tertulis**, bukan dari
satu angka tetap. Dataset dengan `hourly = 0` memang harian, dan menilainya
dengan ambang per jam akan menandai dataset sehat sebagai bermasalah — aturan
yang menyala untuk keadaan normal adalah cara tercepat membuat orang berhenti
membaca seluruh daftarnya.

Berkas yang tidak ada mematikan seluruh aturan ini. Mesin tanpa snapshot
terkelola bukan mesin yang seluruh datasetnya bermasalah.

```yaml
snapshot_policy: /etc/sanoid/sanoid.conf
# snapshot_exempt: kolam/rekaman/*, kolam/scratch
```

`snapshot_exempt` ada supaya satu temuan bisa dimatikan **dengan sadar** —
scratch, rekaman CCTV, apa pun yang dilindungi cara lain. Tanpa jalan itu,
orang mematikan seluruh daftarnya.

## Membangun

```bash
go build -o issboard .                          # dashboard; butuh Go 1.26+
go build -o issboard-agent ./cmd/issboard-agent # notifikasi + riwayat
```

Menjalankan untuk mengembangkan (tanpa systemd, memakai `listen:` dari config):

```bash
./issboard -config issboard.example.yaml
# lalu buka http://127.0.0.1:9955
```

Di mesin tanpa ZFS/Docker, issboard tetap jalan dan melaporkan bagian yang
gagal di field `errors` — bukan mati. Itu bukan kasus tepi, itu cara
mengembangkannya.

**Podman** dipakai dengan mengarahkan `docker_socket` ke
`/run/podman/podman.sock`; API-nya kompatibel. ⚠️ **Belum diuji** — belum ada
mesin untuk memverifikasinya. Laporan dari yang sempat mencoba sangat dihargai.

## Memasang

Jalur yang dianjurkan adalah paket `.deb`:

```bash
./packaging/build-deb.sh                 # → dist/issboard_<versi>_<arch>.deb
sudo apt install ./dist/issboard_*.deb
```

Paketnya membuat user sistem `issboard`, menyiapkan folder cache dan data,
lalu menyalakan `issboard.socket`, `issboard-smart.timer`, dan
`issboard-agent.timer`. Config di `/etc/issboard.yaml` ditandai *conffile*,
jadi suntingan Anda tidak ditimpa saat upgrade.

Distro ber-systemd tanpa dpkg memakai tarball atau langsung dari repo:

```bash
./packaging/build-tarball.sh             # → dist/issboard_<versi>_<arch>.tar.gz
# atau, dari repo:
sudo ./install.sh                        # membangun binary kalau belum ada
```

`install.sh --uninstall` mencopot binary dan unit, dan **sengaja tidak
menghapus** config maupun `/var/lib/issboard` — riwayat dan ingatan alert itu
milik Anda, bukan milik paket.

> ⚠️ **`issboard.service` sengaja tidak untuk di-`enable`.** Socket yang
> menyalakannya. Meng-`enable` service-nya membuat daemon yang jalan terus —
> persis yang dihindari desain ini.

Sunting `/etc/issboard.yaml` seperlunya, lalu buka `http://127.0.0.1:9955`.

### Panel kelola (aksi)

Tombol **kelola** di header membuka **halaman kelola** (`/admin.html`) di tab
baru — halaman sendiri dengan layar masuk dan navigasi per bagian, supaya
dashboard tetap halaman baca tanpa satu pun tombol yang mengubah host. Isinya:
start/stop/restart/hapus container, scrub mulai/jeda/hentikan, SMART tes
singkat/panjang, batas ARC, buat/hapus snapshot, buat dataset anak, ubah
properti dataset, jalankan sanoid (plus potongan `sanoid.conf` untuk dataset
yang belum tercakup — issboard tidak menulis berkas itu), dan daftar jadwal
otomatis (timer systemd, cron, smartd). Semua aksi **mati** sampai kata sandi operator diatur:

```bash
sudo issboard -set-password              # user bawaan "admin"; -user untuk nama lain
```

Aksi yang butuh root dikerjakan `issboard-helper` — dinyalakan per permintaan
oleh `issboard-helper.socket`, yang dinyalakan paket. issboard sendiri tetap
bukan root dan tidak memakai sudo. Jejak aksinya:

```bash
journalctl -u issboard -u 'issboard-helper@*' | grep audit
```

Coba tanpa host sungguhan: `./issboard -demo`, masuk dengan `demo` / `demo` —
aksinya pura-pura.

### Mencoba dengan Docker

```bash
docker compose -f packaging/docker-compose.yml up --build
```

Didukung sebagai **jalur coba-coba**, dengan tiga catatan yang harus jujur:

1. **Kalau Docker mati, dashboard-nya ikut mati** — persis saat paling
   dibutuhkan. issboard ada di host justru supaya tetap hidup saat container
   bermasalah. Ini trade-off yang sadar, bukan bug.
2. **ZFS tidak didukung dari dalam container.** Biner `zfs`/`zpool` di image
   harus cocok versinya dengan modul kernel host, dan versi ZFS mesin orang
   lain tidak bisa dikontrol — jadi image-nya sengaja tidak memasang zfsutils
   sama sekali. Bagian ZFS akan kosong dan melaporkan error.
3. **Tidak ada socket activation di dalam container**, jadi prosesnya jalan
   terus — bukan lagi 0 MB saat menganggur.

### Distro lain

Podman, SELinux, non-systemd, dan jalur unit `/lib` vs `/usr/lib` dibahas di
[`docs/distro.md`](docs/distro.md) — lengkap dengan **apa yang belum pernah
diuji**, ditandai jelas.

## Notifikasi & riwayat (`issboard-agent`)

Dashboard yang harus dibuka dulu baru ketahuan ada masalah bukan sistem
peringatan. `issboard-agent` adalah program **terpisah** yang dijalankan
`issboard-agent.timer` tiap menit: ia mengumpulkan data dengan kode yang sama,
menulis riwayat, mengirim notifikasi kalau ada yang perlu diurus, lalu keluar.

**Kenapa timer, bukan mengubah issboard jadi daemon:**

| | RAM idle | Riwayat | Bisa alert |
|---|---|---|---|
| issboard jadi daemon | ~15–25 MB terus-menerus | ✅ di memori, hilang saat reboot | ✅ |
| issboard sekarang saja | **0 MB** | ❌ | ❌ |
| **issboard + agent bertimer** | **0 MB** | ✅ dari berkas, selamat dari reboot | ✅ |

Aturan temuannya **dipakai bersama** dashboard (`internal/health`), bukan
disalin. Dua salinan aturan akan berbeda pelan-pelan, dan yang gagal duluan
justru jalur alert — satu-satunya yang bekerja saat halaman tidak dibuka.

**Kanal:** ntfy dan/atau Telegram, keduanya cuma HTTP POST, jadi tetap nol
dependensi. Token lewat `/etc/issboard/agent.env` mode 0600 (lihat
[`issboard-agent.env.example`](issboard-agent.env.example)), **bukan** di
`/etc/issboard.yaml` yang boleh dibaca siapa saja.

Mencoba tanpa mengirim apa pun:

```bash
./issboard-agent -config /dev/null -demo -dry-run   # data palsu, cetak saja
./issboard-agent -config /etc/issboard.yaml -dry-run # data nyata, tetap tidak mengirim
```

### Yang menjaga alertnya tetap sepi

Alert yang berisik selalu berakhir sama: diabaikan, lalu dimatikan. Karena itu:

- **Satu pesan per siklus**, bukan satu per temuan. Host yang baru dipasangi
  issboard bisa punya belasan temuan sekaligus.
- **De-duplikasi.** Temuan yang sama tidak dikirim dua kali. Yang menembus:
  temuan baru, tingkat yang naik (perhatian → kritis), dan pengingat setelah
  `alert_repeat` (bawaan 24 jam).
- **Siklus pertama dibingkai sebagai "kondisi saat ini"**, bukan "baru saja
  terjadi" — temuannya bisa saja sudah berbulan-bulan umurnya.
- **Kabar pulih** dikirim sekali, dengan prioritas rendah.
- **Kiriman yang gagal tidak ditandai terkirim.** Wifi yang putus sesaat tidak
  boleh membuat satu alert hilang selamanya; menit berikutnya dicoba lagi, dan
  unitnya terlihat `failed` di `systemctl status`.
- **Siklus yang pengumpulannya error tidak pernah melaporkan "pulih".** Kalau
  `zpool` gagal dipanggil, seluruh temuan pool ikut lenyap dari daftar —
  mengabarkannya sebagai pulih justru saat kita kehilangan kemampuan melihat
  pool adalah kebalikan dari keadaan sebenarnya.

### Riwayat

`history.json` adalah ring buffer dua lapis: **60 titik tiap menit** (1 jam
terakhir) dan **48 titik tiap 30 menit** (24 jam terakhir), ditulis atomik,
di bawah 20 KB. issboard **membacanya**, tidak pernah menulisnya — pola yang
sama dengan cache SMART.

Sparkline di tiap kartu memakai riwayat itu, dan tombol **1 jam / 24 jam** di
pojok kanan atas menukar lapis halus dengan lapis kasar. Grafiknya digambar
tangan sebagai satu `<path>` SVG — tanpa pustaka grafik, supaya janji "tidak
ada aset dari luar" tetap utuh.

Tiga hal yang sengaja tidak dikarang oleh grafik ini:

- **Riwayat yang bolong digambar putus**, bukan disambung lurus. Garis lurus
  palsu justru menyembunyikan hal yang ingin diketahui: ada periode yang tidak
  terpantau.
- **Disk yang sedang tidur tidak punya garis sama sekali.** Suhunya tidak
  terbaca, dan itu bukan 0 °C.
- **Sumbu Y punya rentang minimum.** Pool yang bergerak 0,4% dalam sejam tidak
  boleh tergambar seperti tebing — dashboard yang bikin panik karena skala,
  bukan karena data, adalah kebalikan dari gunanya.

Kalau agent-nya mati, halaman menyebutkannya di bawah judul **host**. Agent
tidak bisa mengabari bahwa dirinya sendiri berhenti jalan, jadi dashboard
adalah satu-satunya tempat hal itu bisa ketahuan.

## Keamanan

- **Bawaannya loopback saja.** Boleh diperluas ke jaringan yang sudah
  mengautentikasi perangkatnya sendiri — tailnet WireGuard, misalnya — dengan
  mengikat ke alamat tailnet yang **spesifik**, bukan `0.0.0.0`. LAN tidak
  termasuk: berada di LAN bukan bukti identitas apa pun. Alternatif yang lebih
  rapi: `tailscale serve` atau reverse proxy berautentikasi di depannya, supaya
  issboard tetap di loopback. Rinciannya di
  [design.md §8.1](docs/design.md).
- **Di belakang proxy di host yang sama** (mis. cloudflared), alamat asli
  pengunjung dibaca dari `CF-Connecting-IP` / `X-Forwarded-For` — tapi HANYA
  kalau koneksinya datang dari loopback. Tanpa itu pembatas login jadi satu
  hitungan global yang bisa dipakai siapa pun untuk mengunci pemiliknya.
  Aset statis dikirim `Cache-Control: no-cache` dan API `no-store`, supaya CDN
  tidak menyajikan CSS lama bersama HTML baru.
- **Bagian baca terbuka, aksi butuh login.** Kata sandi di-hash PBKDF2 di
  `/etc/issboard/auth` (0640 `root:issboard`), sesi berupa cookie ber-HMAC yang
  selamat dari idle-exit, tiap aksi butuh token CSRF, login dibatasi 5 gagal
  per 15 menit, dan semuanya dicatat di journal. Rinciannya di
  [plan/07](docs/plan/07-autentikasi.md).
- Jalan sebagai user sistem sendiri, bukan root, dengan pengerasan systemd.
  Aksi root lewat `issboard-helper`, yang mencocokkan setiap target dengan
  daftar dari sistem sendiri — tidak mempercayai issboard.
- ⚠️ Keanggotaan grup `docker` **setara root** di kebanyakan sistem, dan sejak
  panel kelola dipakai juga untuk aksi container. Pembatasnya autentikasi.

## API

| Endpoint | Isi |
|---|---|
| `GET /api/v1/health` | liveness, tanpa mengumpulkan apa pun |
| `GET /api/v1/status` | seluruh snapshot: host, pools, datasets (termasuk kebijakan snapshot per-dataset), containers, smart, plus `verdict` & `findings[]` |
| `GET /api/v1/history` | ring buffer dua lapis yang ditulis `issboard-agent`; membaca berkas, tidak mengumpulkan apa pun |
| `GET /api/v1/manage` | keadaan ARC, jadwal (timer, cron, smartd), apakah helper terpasang |
| `GET /api/v1/session` | masuk atau belum, dan token CSRF kalau sudah |
| `POST /api/v1/login`, `/logout` | sesi |
| `POST /api/v1/containers/{nama}/{start\|stop\|restart\|remove}` | aksi container |
| `POST /api/v1/pools/{nama}/scrub/{start\|pause\|stop}` | scrub, lewat helper |
| `POST /api/v1/smart/{short\|long\|abort\|refresh}` | SMART, body `{"device": "/dev/sda"}` |
| `POST /api/v1/arc` | batas ARC, body `{"max_bytes": n, "persist": true}`; 0 = bawaan |
| `GET /api/v1/dataset?name=` | properti yang bisa diubah + snapshot satu dataset |
| `POST /api/v1/snapshots` | `{"dataset", "tag", "recursive"}` |
| `POST /api/v1/snapshots/destroy` | `{"snapshot": "pool/ds@nama"}` — satu snapshot, tanpa `-r` |
| `POST /api/v1/datasets` | `{"parent", "name", "props": {...}}` |
| `POST /api/v1/datasets/props` | `{"dataset", "prop", "value"}`; `inherit` untuk kembali mewarisi |
| `POST /api/v1/sanoid/run` | nyalakan `sanoid.service` |

Semua `POST` aksi butuh cookie sesi **dan** header `X-CSRF-Token`; tanpa sesi
jawabannya 401.

Kegagalan per-bagian muncul di `errors[]`, bukan menggagalkan seluruh respons —
dan tetap dilaporkan selama datanya masih dilayani dari cache, bukan cuma di
permintaan yang kebetulan mengambil ulang.

`findings[]` dihitung di server, bukan di JavaScript, supaya pengirim
notifikasi nanti memakai aturan yang persis sama. Tiap temuan punya `key` yang
stabil untuk de-duplikasi alert.

Field yang ketiadaannya berarti sesuatu **dihilangkan**, bukan dikirim sebagai
nilai nol: `last_snapshot` tidak ada berarti dataset itu belum pernah punya
snapshot. Waktu nol yang terkirim apa adanya (`0001-01-01T00:00:00Z`) akan
dibaca penerima sebagai tanggal sungguhan — dan `omitempty` tidak berlaku
untuk struct, jadi ini dikunci test.

## Test

```bash
go test ./...
```

Yang diuji lebih dulu adalah **parser**, karena di situlah data dunia nyata
paling sering mengejutkan: `zpool list`/`zpool status` (termasuk membedakan
**mirror dari stripe**, dan baris `scan:` dalam berbagai bentuk), pembacaan
cache SMART beserta deteksi basi, JSON Docker (network kosong, port `0.0.0.0`,
healthcheck yang cuma menempel di teks status), dan `sanoid.conf` (template
yang saling menimpa, rekursi, bagian paling spesifik yang harus menang).

Selain itu: seluruh aturan vonis di `internal/health` — dengan **data mode demo
sebagai fixture**, karena data itu memang dibuat memuat setiap kondisi sakit —
serta ring buffer riwayat dan mesin de-duplikasi alert, termasuk jalur yang
paling mudah salah: kiriman gagal harus dicoba lagi, dan siklus yang
pengumpulannya error tidak boleh pernah melaporkan "pulih".

Sebagian test justru menjaga aturan agar **tetap diam**: host sehat tanpa
temuan, disk yang tidur tidak pernah jadi temuan, dan dataset harian berumur
9,7 jam tidak boleh terbaca terlambat. Aturan yang menyala untuk keadaan
normal melatih orang mengabaikan seluruh daftarnya.

## Lisensi

MIT — lihat [LICENSE](LICENSE).
