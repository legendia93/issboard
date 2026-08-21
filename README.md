# issboard

Dashboard kesehatan host untuk server rumahan berbasis **ZFS + Docker**:
satu binary Go statis, frontend ikut ter-*embed*, tanpa runtime apa pun di
server. Read-only di v1.

Yang ditampilkan: pool & dataset ZFS (termasuk **apakah pool pernah di-scrub**
dan **apakah benar-benar redundan**), ringkasan SMART tiap disk, daftar
container beserta port yang ter-*publish*, dan beban host + ARC.

Di atas semuanya ada **satu vonis**: sehat, atau sekian hal yang perlu diurus —
supaya pertanyaan yang sebenarnya dicari terjawab tanpa membaca satu kartu pun.
Vonis yang sama itulah yang dikirim ke HP oleh `issboard-agent`.

> **Status: v1 awal.** Berjalan dan menyajikan data nyata, tapi belum dipakai
> lama di produksi. Belum ada test otomatis.
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

```bash
sudo install -m 0755 issboard              /usr/local/bin/issboard
sudo install -m 0755 issboard-agent        /usr/local/bin/issboard-agent
sudo install -m 0755 libexec/issboard-smart-collect \
                                           /usr/local/libexec/issboard-smart-collect
sudo install -m 0644 issboard.example.yaml /etc/issboard.yaml
sudo install -m 0644 systemd/*             /etc/systemd/system/

sudo useradd --system --no-create-home --shell /usr/sbin/nologin issboard
sudo usermod -aG docker issboard           # hanya untuk MEMBACA socket

sudo systemctl daemon-reload
sudo systemctl enable --now issboard-smart.timer
sudo systemctl enable --now issboard.socket   # socket, BUKAN service
sudo systemctl enable --now issboard-agent.timer
```

Sunting `/etc/issboard.yaml` seperlunya, lalu buka `http://127.0.0.1:9955`.

> ⚠️ **`issboard.service` sengaja tidak untuk di-`enable`.** Socket yang
> menyalakannya. Meng-`enable` service-nya membuat daemon yang jalan terus —
> persis yang dihindari desain ini.

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

- **Bind ke loopback saja.** Akses dari luar lewat SSH tunnel atau reverse
  proxy yang punya autentikasi. Jangan taruh langsung di LAN atau tailnet.
- **issboard tidak punya autentikasi sendiri** di v1. Ini disengaja: read-only
  di belakang loopback. Begitu ada endpoint yang bermutasi, autentikasi wajib
  lebih dulu.
- Jalan sebagai user sistem sendiri, bukan root, dengan pengerasan systemd.
- ⚠️ Keanggotaan grup `docker` **setara root** di kebanyakan sistem. Pembatas
  sebenarnya di v1 adalah tidak adanya jalur mutasi sama sekali.

## API

| Endpoint | Isi |
|---|---|
| `GET /api/v1/health` | liveness, tanpa mengumpulkan apa pun |
| `GET /api/v1/status` | seluruh snapshot: host, pools, datasets, containers, smart, plus `verdict` & `findings[]` |
| `GET /api/v1/history` | ring buffer dua lapis yang ditulis `issboard-agent`; membaca berkas, tidak mengumpulkan apa pun |

Kegagalan per-bagian muncul di `errors[]`, bukan menggagalkan seluruh respons —
dan tetap dilaporkan selama datanya masih dilayani dari cache, bukan cuma di
permintaan yang kebetulan mengambil ulang.

`findings[]` dihitung di server, bukan di JavaScript, supaya pengirim
notifikasi nanti memakai aturan yang persis sama. Tiap temuan punya `key` yang
stabil untuk de-duplikasi alert.

## Lisensi

MIT — lihat [LICENSE](LICENSE).
