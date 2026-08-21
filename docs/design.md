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
- **Dashboard-nya bukan sumber alert.** Lihat keputusan socket activation di
  bawah — halaman ini secara struktural tidak bisa memberi tahu apa pun saat
  tidak dibuka, dan itu disengaja. Notifikasi ada, tapi dikerjakan program
  terpisah yang bertimer (bagian 9) — bukan dengan menghidupkan dashboard
  terus-menerus.
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

Sejak fase 2, unit terpisah itu ada: `issboard-agent`, dijalankan timer tiap
menit. Ia memakai ulang `internal/collector` dan `internal/health`, menulis
riwayat ke berkas, mengirim notifikasi, lalu keluar — RSS kembali nol. Lihat
bagian 9.

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

**Disk yang tidur tidak pernah dinilai.** Konsekuensi `-n standby` adalah
disk yang tertidur mengembalikan berkas kosong: suhu 0, dan `passed` yang
kosongnya berarti `false`. Menilai itu apa adanya menghasilkan alarm KRITIS
"SMART gagal" untuk disk yang sebenarnya baik-baik saja — ketiadaan data
dibaca sebagai data buruk. Di mesin sungguhan hal ini muncul pada menit
pertama pemasangan, dan alarm palsu di hari pertama adalah cara tercepat
membuat orang berhenti memercayai alatnya. Aturannya sekarang: **disk
standby dilewati seluruhnya**; yang menjaga kita tidak buta adalah penanda
cache basi, bukan menebak-nebak dari berkas kosong.

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
cmd/issboard-agent/     binary KEDUA: riwayat + notifikasi, bertimer
internal/config/        config sederhana "kunci: nilai", tanpa dependensi
internal/collector/
  collector.go          cache per-bagian dengan TTL masing-masing
  zfs.go                zpool list/status, zfs list, hitung snapshot
  smart.go              BACA cache JSON — tidak pernah memanggil smartctl
  docker.go             socket Docker lewat unix transport
  host.go               /proc + /proc/spl/kstat/zfs/arcstats
internal/health/        aturan vonis — dipakai ulang pengirim notifikasi
internal/history/       ring buffer 2 lapis; agent menulis, issboard membaca
internal/alert/         de-duplikasi "sudah dikabari" + penyusun pesan
internal/notify/        ntfy & Telegram — cuma HTTP POST, nol dependensi
internal/api/           GET /api/v1/*
web/                    vanilla, ter-embed, tanpa build step
systemd/                socket + service + timer SMART + timer agent
libexec/                pengumpul SMART milik root
packaging/              .deb, tarball, docker-compose
install.sh              pemasang POSIX sh untuk distro tanpa dpkg
```

Test ada di sebelah kode yang diujinya. Yang diutamakan adalah **parser** —
di situlah data dunia nyata paling sering mengejutkan — lalu seluruh aturan
`internal/health` dengan data mode demo sebagai fixture. Catatan lengkap
tentang distro lain, termasuk yang belum diuji, ada di
[`distro.md`](distro.md).

**Kegagalan per-bagian tidak menggagalkan seluruh respons.** Setiap collector
yang gagal menaruh pesannya di `errors[]`, dan sisanya tetap disajikan. Di
mesin tanpa ZFS atau tanpa Docker, issboard tetap jalan dan berguna — itu
bukan kasus tepi, itu cara mengembangkannya.

Error disimpan **bersama nilai yang di-cache**, bukan dilaporkan sekali saat
pengambilan gagal. Kalau tidak, error cuma muncul di satu permintaan lalu
hilang selama TTL — dan karena halaman polling tiap 15 detik, host tanpa ZFS
akan hampir selalu terlihat baik-baik saja. Layar yang tampak sehat karena
buta lebih berbahaya daripada layar yang mengaku tidak tahu.

## 6. API

Semua JSON. **v1 hanya `GET`**, tapi router sengaja **tidak dikunci** ke GET
saja: endpoint yang bermutasi akan menyusul, dan bentuknya sudah disiapkan
supaya tidak perlu dibongkar.

| Endpoint | Isi |
|---|---|
| `GET /api/v1/health` | liveness, tanpa mengumpulkan apa pun |
| `GET /api/v1/status` | snapshot penuh: host, pools, datasets, containers, smart, `verdict`, `findings[]` |
| `GET /api/v1/history` | riwayat untuk sparkline — MEMBACA berkas milik agent, tidak mengumpulkan apa pun |

**Vonis dihitung di server, bukan di JavaScript.** Aturannya ada di
`internal/health` supaya pengirim notifikasi (yang tidak punya browser)
memakai aturan yang persis sama. Dua salinan aturan di dua bahasa akan
berbeda pelan-pelan, dan yang gagal duluan justru jalur alert — satu-satunya
yang bekerja saat halaman dashboard tidak dibuka.

## 7. Tampilan

Temanya **Nothing OS**, di-port dari proyek `lookna`: off-white atau hitam
pekat, garis 1px, radius squircle, label mono uppercase. Tidak ada gradient
warna, glow, glass/blur — dan tidak ada animasi yang jalan terus-menerus,
karena dashboard yang berkedip melelahkan dan membuang daya HP.

**Dirancang untuk layar HP lebih dulu.** Host ini paling sering dilihat lewat
remote dari HP, jadi satu kolom adalah keadaan normal, bukan kasus tepi yang
ditambal di ujung berkas CSS.

### 7.1 Vonis di rail, dan pembalikan warna yang cuma satu arah

Di layar ≥1180px, vonis dan daftar temuan pindah ke **rail** di kanan yang
menempel saat scroll. Dua alasan, dan yang kedua yang membuatnya bukan sekadar
mengisi ruang kosong:

1. Isi halaman terkunci di kolom tengah; di monitor lebar sisanya menganggur.
2. **Vonisnya dulu menggulung hilang.** Satu-satunya jawaban atas "ada yang
   perlu diurus?" lenyap dari layar begitu orang mulai membaca kartu — padahal
   itu pertanyaan yang membuat halaman ini dibuka.

Kartu vonisnya **gelap di kedua tema**, dan itu disengaja. Saat tema terang ia
kebalikan halaman, jadi menonjol tanpa menyilaukan. Saat tema gelap,
membaliknya jadi putih menghasilkan bidang terang besar di layar yang gelap —
diuji di HP dan hasilnya menyakitkan mata, padahal halaman ini paling sering
dibuka malam hari dari HP. **Silau hanya berjalan satu arah, jadi
pembalikannya juga cuma satu arah.**

Daftar temuan di rail juga menutup lubang lama: `findings[]` sudah dihitung
server sejak awal, tapi tidak pernah ditampilkan di mana pun. Angka besar di
kartu vonis tidak bisa dijawab tanpa menyisir seluruh halaman sendiri.

### 7.2 "Sehat itu pastel, bermasalah itu pekat"

Pastel dipakai sebagai **identitas data** — menandai *pool yang mana*, bukan
*sehat atau tidak*. Status memakai warna pekat, dan hanya saat ada masalah.

Akibatnya layar yang sehat sepenuhnya tenang, dan satu warna pekat yang muncul
langsung menarik mata. Itu memang seluruh tugas dashboard ini.

Konsekuensi yang mengikat: **palet pastel tidak boleh memuat kuning, oranye,
atau merah.** Warna itu sudah punya arti. Butter pastel sempat dipakai, dan bar
memori 67% yang sehat langsung terbaca seperti peringatan.

### 7.3 Angka tidak pernah memakai font dot-matrix

Font dot-matrix adalah tanda tangan Nothing OS, dan menggoda untuk dipakai di
angka besar. Tapi diuji: pada font itu, **`52` terbaca `92`, `35` terbaca `39`,
`58` terbaca `98`** — digit 3/5/6/8/9 memakai kisi piksel yang nyaris sama.

Salah membaca suhu disk 52°C sebagai 92°C persis jenis kesalahan yang dashboard
ini ada untuk mencegahnya. Jadi aturannya: **font dot untuk kata, mono untuk
angka.**

### 7.4 Mode demo

`-demo` menyajikan data palsu dan tidak menyentuh sistem sama sekali. Dua
alasan, dan yang kedua yang membuatnya dikerjakan bersamaan dengan UI:

1. Tampilan kondisi sakit mustahil digarap kalau harus menunggu disk benar-benar
   memburuk. Tanpa ini, aturan di 7.2 tidak bisa dilihat hasilnya.
2. Halaman ini menampilkan hostname, alamat IP, nama pool, dan nama app. Mode
   demo membuat screenshot dan rekaman layar aman — persis hal yang `.gitignore`
   repo ini susah payah jaga.

## 8. Keamanan

**Aturan tetap: batasi akses, bukan kemampuan.** Dashboard yang dilumpuhkan
sampai tidak berguna akan diganti orang dengan shell — dan shell itu jauh
lebih berbahaya daripada dashboard yang dirancang benar.

Penerapannya di v1:

- **Bawaannya loopback saja**, dan alamatnya boleh diperluas hanya ke jaringan
  yang dirinya sendiri sudah mengautentikasi perangkat. Rinciannya di 8.1.
- **Tidak ada autentikasi bawaan** di v1 — disengaja, karena read-only di balik
  loopback. **Begitu ada satu endpoint yang bermutasi, autentikasi wajib lebih
  dulu**, bukan menyusul.
- Jalan sebagai **user sistem sendiri**, bukan root, dengan pengerasan systemd
  (`ProtectSystem=strict`, `NoNewPrivileges`, `SystemCallFilter`, dan seterusnya).
- ⚠️ Keanggotaan grup `docker` **setara root** di kebanyakan sistem. Grup itu
  diberikan hanya untuk membaca socket; pembatas sebenarnya di v1 adalah
  **tidak adanya jalur mutasi sama sekali.**

### 8.1 Sampai di mana dashboard ini boleh dijangkau

Versi pertama aturan ini berbunyi *"jangan pernah ditaruh langsung di LAN atau
VPN mesh"* — loopback, titik. Itu ditulis saat model ancamannya berbeda, dan
ditinjau ulang setelah keadaannya berubah. **Aturan yang alasannya sudah
kedaluwarsa tapi tidak pernah ditinjau berakhir dua cara: dilanggar diam-diam,
atau dipatuhi tanpa ada yang ingat kenapa.** Keduanya lebih buruk daripada
aturan yang diperbarui dengan sadar.

Yang tidak berubah adalah dasarnya: **issboard v1 tidak punya autentikasi
sendiri**, dan halamannya memuat hostname, nama pool, daftar container, serta
port yang ter-*publish* — peta pengintaian yang rapi bagi siapa pun yang sudah
berada di jaringan yang sama. Jadi pertanyaannya bukan "boleh dijangkau dari
mana", melainkan **"siapa yang sudah diautentikasi oleh jaringan itu sebelum
sampai ke sini"**.

| Jaringan | Boleh? | Alasan |
|---|---|---|
| Loopback | ✅ selalu | Tidak ada yang bisa menjangkaunya tanpa akses ke mesin |
| **Tailnet / VPN mesh** | ✅ dengan syarat | Perangkat diverifikasi kunci WireGuard **sebelum** paket sampai; lalu lintasnya terenkripsi |
| LAN | ❌ | Berada di LAN bukan bukti identitas apa pun |
| `0.0.0.0` | ❌ | Berarti dua-duanya sekaligus, dan biasanya tidak disengaja |
| Publik | ❌ | Tidak dengan v1 yang tanpa autentikasi |

Syarat untuk tailnet, dan ketiganya mengikat:

1. **Bind ke alamat tailnet yang spesifik**, bukan `0.0.0.0`. Mengikat ke
   semua alamat lalu mengandalkan firewall berarti satu aturan firewall yang
   keliru sudah cukup untuk membocorkannya ke LAN.
2. **ACL tailnet adalah kontrol akses yang sebenarnya.** Kalau semua perangkat
   di tailnet boleh menjangkaunya, maka batasnya adalah perangkat paling lemah
   di sana. Itu keputusan yang harus diambil sadar, bukan bawaan yang
   kebetulan.
3. **Begitu ada endpoint yang bermutasi, autentikasi wajib lebih dulu** —
   aturan ini tidak ikut longgar. Jaringan yang mengautentikasi *perangkat*
   tidak sama dengan aplikasi yang mengautentikasi *orang*.

Alternatif yang tetap sah dan tidak menyentuh alamat issboard sama sekali:
`tailscale serve`, atau reverse proxy yang punya autentikasi sendiri. Keduanya
membiarkan issboard di loopback dan menaruh pintu berautentikasi di depannya —
secara struktur ini yang paling rapi, karena pembatasnya tidak lagi bergantung
pada issboard yang mengikat alamat dengan benar.

⚠️ **Beberapa alamat berarti beberapa file descriptor.** systemd menyerahkan
satu fd per `ListenStream`, dan program yang hanya menerima `LISTEN_FDS=1`
akan jatuh ke jalur cadangan lalu mencoba bind sendiri ke alamat yang justru
sedang dipegang systemd. Hasilnya proses mati seketika, dinyalakan lagi tiap
koneksi, sampai socket-nya sendiri ikut gagal kena *start limit* — dashboard
mati total, dan penyebabnya tidak tersirat di pesan systemd mana pun. issboard
memakai **semua** fd yang diserahkan; ini pernah salah, dan dikunci test.

**Catatan penerapan:** di bawah systemd, alamatnya dipegang `issboard.socket`,
**bukan** `listen:` di berkas config — `listen:` hanya dipakai saat berjalan
tanpa socket activation. Ubah lewat drop-in di
`/etc/systemd/system/issboard.socket.d/`, jangan menyunting unit bawaan paket,
karena suntingan itu akan hilang saat upgrade. `ListenStream` bersifat
menumpuk: menambah satu baris berarti menambah alamat, bukan menggantinya.
Alamat tailnet juga butuh `FreeBind=true`, karena `tailscale0` bisa naik
setelah socket dibuat dan bind ke alamat yang belum ada akan gagal saat boot.

### Kalau nanti ada CRUD

Dua hal yang harus dipegang sejak sekarang, karena mahal kalau ditambal
belakangan:

1. **Hak akses per kebutuhan, bukan `NOPASSWD: ALL`.** Godaannya besar saat
   menambahkan aksi pertama yang butuh root. Tuliskan perintah spesifiknya.
2. **Validasi nama dataset/pool dengan daftar-putih, bukan regex.** Regex
   meloloskan hal seperti `pool/app@../..`. Ambil daftar nyata dari sistem,
   cocokkan persis, tolak sisanya.

## 9. Notifikasi & riwayat: `issboard-agent`

Program **kedua**, dijalankan `issboard-agent.timer` tiap menit. Ia memakai
ulang `internal/collector` dan `internal/health`, menulis riwayat, mengirim
paling banyak satu pesan, lalu keluar.

Godaan yang ditolak di sini adalah menjadikan issboard daemon supaya bisa
mengirim alert. Angkanya yang membuat pilihannya jelas:

| | RAM idle | CPU idle | Riwayat | Bisa alert |
|---|---|---|---|---|
| issboard jadi daemon | ~15–25 MB terus-menerus | poll tiap 15 dtk selamanya | ✅ di memori | ✅ |
| issboard sekarang saja | **0 MB** | **0** | ❌ | ❌ |
| **issboard + agent bertimer** | **0 MB** | 1 spawn/menit | ✅ dari berkas | ✅ |

Riwayat di berkas juga **selamat dari reboot** — sesuatu yang daemon in-memory
justru tidak punya.

### 9.1 Aturannya tidak boleh disalin

Agent tidak menulis ulang satu pun aturan vonis. Semuanya di `internal/health`,
dipakai bersama dashboard. Dua salinan aturan di dua tempat akan berbeda
pelan-pelan, dan yang gagal duluan justru jalur alert — satu-satunya yang
bekerja saat halaman tidak dibuka.

### 9.2 Yang menjaga alertnya tetap sepi

Alert yang berisik berakhir diabaikan, lalu dimatikan — hasilnya sama saja
dengan tidak punya alert. Karena itu:

- **Satu pesan per siklus**, bukan satu per temuan.
- **De-duplikasi** lewat `key` temuan yang stabil, disimpan di
  `alert-state.json`. Yang menembusnya cuma tiga hal: temuan baru, tingkat yang
  naik, dan pengingat setelah `alert_repeat`.
- **Siklus pertama dibingkai "kondisi saat ini"**, bukan "baru saja terjadi":
  di host yang sudah lama berjalan, temuannya bisa berumur berbulan-bulan.
- **Pesan dipotong** di ~3,5 KB dengan penghitung sisanya. ntfy dan Telegram
  sama-sama menolak badan pesan yang terlalu panjang, dan alert yang gagal
  terkirim karena isinya kebanyakan adalah kegagalan diam.
- **Aturannya sendiri harus sepi.** Versi pertama menandai setiap port yang
  ter-publish ke `0.0.0.0`. Di server sungguhan hasilnya 19 dari 25 temuan —
  padahal mem-publish port justru cara aplikasi web dijangkau; itu tujuannya,
  bukan kecelakaan. Sekarang yang ditandai hanya layanan yang biasanya
  mengandalkan jaringan sebagai pembatas alih-alih autentikasinya sendiri
  (basis data, cache, broker, API Docker). Daftar yang sama itu turun jadi 5,
  dan kelimanya bisa ditindaklanjuti.

  Pelajarannya lebih umum daripada soal port: **aturan yang menyala untuk
  keadaan normal melatih orang mengabaikan seluruh daftarnya**, dan sesudah
  itu temuan yang sungguhan ikut tidak terbaca. Aturan baru harus diuji
  terhadap mesin yang sehat, bukan cuma terhadap mesin yang sakit.

### 9.3 Dua aturan yang lahir dari cara ini gagal

Keduanya jenis kegagalan yang sama: sistemnya diam atau berbohong justru saat
sedang bermasalah.

1. **Kiriman yang gagal tidak pernah ditandai terkirim.** Wifi putus sesaat
   tidak boleh menghapus satu alert selamanya. Ini berlaku juga untuk kabar
   "sudah pulih": ingatannya ditahan sampai kabar itu benar-benar sampai.
2. **Siklus yang pengumpulannya error tidak pernah melaporkan "pulih".** Kalau
   `zpool` gagal dipanggil, seluruh temuan pool ikut lenyap dari daftar. Tanpa
   penjaga ini, agent akan mengabarkan "pool sudah pulih" persis saat ia
   kehilangan kemampuan melihat pool sama sekali.

### 9.4 Riwayat

Ring buffer **dua lapis** dalam satu berkas, ditulis atomik seperti cache SMART:
60 titik jarak 1 menit (1 jam terakhir) dan 48 titik jarak 30 menit (24 jam
terakhir), di bawah 20 KB. Lapis kasar berisi **rata-rata**, bukan cuplikan
sesaat, supaya lonjakan tidak hilang di grafik 24 jam.

Yang disimpan hanya angka yang berguna sebagai tren. Disk yang sedang tidur
**tidak dicatat sama sekali**, bukan dicatat 0°C — 0 berarti "tidak terbaca",
dan menyimpannya sebagai angka akan menggambar jurang palsu.

Pemakaian CPU butuh dua cuplikan `/proc/stat`, dan proses yang hidup beberapa
milidetik tidak punya cuplikan sebelumnya. Cuplikan itu ikut disimpan di berkas
riwayat — angkanya jadi rata-rata satu menit penuh, bukan hasil tidur 300 ms.

### 9.5 Sparkline membaca, tidak pernah mengumpulkan

Grafik di tiap kartu dibaca dari `history.json` lewat `GET /api/v1/history`.
Endpoint itu **hanya membuka berkas** — tidak memanggil `zpool`, tidak
menyentuh socket container, tidak menulis apa pun. Sama seperti cache SMART,
dan alasannya juga sama: yang mahal dikerjakan di proses lain yang bertimer.

Digambar tangan sebagai satu `<path>` SVG di `app.js`. Pustaka grafik akan
melanggar dua janji sekaligus — nol dependensi, dan `default-src 'self'` tanpa
aset dari luar — demi belasan baris kode yang bisa ditulis langsung.

Tiga aturan menjaga grafiknya tidak mengarang:

1. **Bolong digambar putus.** Riwayat yang hilang karena agent mati atau mesin
   baru menyala tidak boleh disambung lurus; garis lurus palsu menyembunyikan
   justru periode yang tidak terpantau.
2. **Disk tidur tidak punya garis.** Agent tidak mencatat suhu disk standby,
   dan 0 °C akan terbaca sebagai dingin.
3. **Sumbu Y punya rentang minimum.** Autoscale murni membuat pergerakan 0,4%
   tergambar seperti tebing. Dashboard yang bikin panik karena skala, bukan
   karena data, adalah kebalikan dari gunanya.

Grafik kosong punya dua arti yang jauh berbeda — mesin baru dipasang, atau
timer agent mati — jadi endpoint-nya mengembalikan `note` dan halaman
menampilkannya. Agent tidak bisa mengabari bahwa dirinya sendiri berhenti
jalan; dashboard adalah satu-satunya tempat itu bisa ketahuan.

### 9.6 Kredensial

Token **tidak boleh** masuk `/etc/issboard.yaml`: berkas itu dibaca dashboard
dan biasanya boleh dibaca siapa saja. Jalurnya `EnvironmentFile` systemd dari
berkas mode 0600 milik root. Variabel lingkungan menimpa berkas config, jadi
tidak ada alasan menuliskannya dua kali.

## 10. Status & yang belum ada

v1 **sudah terpasang dan berjalan di host sungguhan** sejak 21 Agustus 2026,
dengan tampilan Nothing OS, mode demo, notifikasi lewat `issboard-agent` yang
terpisah, sparkline riwayat, test otomatis, dan paket `.deb`.

Pemasangan pertama itu sendiri menemukan enam cacat yang tidak satu pun bisa
muncul di mesin pengembangan — dicatat lengkap di
[`plan/05-pemasangan.md`](plan/05-pemasangan.md), beserta pelajaran yang
berlaku di luar proyek ini. Yang paling ringkas: **aturan baru harus diuji
terhadap mesin yang sehat, bukan cuma terhadap mesin yang sakit**, dan
**ketiadaan data bukan data buruk**.

Yang **belum diuji** dicatat apa adanya di [`distro.md`](distro.md) dan di
tiap berkas rencana — notifikasi yang benar-benar terkirim, `alert_repeat` 24
jam, SELinux enforcing, Podman, dan arsitektur selain amd64.

Rencana yang sedang berjalan — beserta urutannya — ada di
[`plan/`](plan/00-index.md). Ringkasnya yang belum:

- Perbandingan konfigurasi snapshot (mis. `sanoid.conf`) dengan dataset nyata
- Panel versi app + deteksi drift antara config dan container yang jalan
- Fase arsip & unduhan backup
- Autentikasi (lihat bagian 8)
