# Fase 8 — Panel ZFS di dashboard

**Status: 🟡 bagian 1 dan 2 selesai di kode (29 September 2026), belum dipasang
di host sungguhan. Kelas C selain hapus container dan hapus satu snapshot
sengaja belum dikerjakan.** Ditulis 25 Agustus 2026 dari arahan pemilik server: side
panel di frontend supaya ZFS bisa diatur dari dashboard — buat zpool, destroy,
buat dataset, snapshot, sanoid, status scrub, quick test.

## Yang dikerjakan 29 September 2026

Arahan pemilik server hari itu mempersempit sekaligus memperluas daftar di
atas. Tiga hal yang diminta:

1. **Batas cache ARC bisa diatur.**
2. **Container bisa dihapus, di-stop, di-start, di-restart** — mirip Portainer.
3. **Scrub dan quick test disk, plus info jadwal (cron dan lainnya)** — mirip
   Cockpit.

Semuanya ada di panel **kelola** (tombol di header, atau `#kelola` di URL).

### Bentuknya

| Aksi | Lewat | Kelas |
|---|---|---|
| start / stop / restart container | socket Docker (grup `docker` yang sudah ada) | A–B |
| **hapus** container | socket Docker, `force=0` `v=0` | **C** |
| scrub mulai / jeda / hentikan | `issboard-helper` → `zpool scrub` | A |
| SMART tes singkat / panjang / batalkan | `issboard-helper` → `smartctl -t` / `-X` | A |
| segarkan cache SMART | `issboard-helper` → `systemctl start issboard-smart.service` | A |
| batas ARC (+ simpan permanen) | `issboard-helper` → `/sys/module/zfs/parameters/zfs_arc_max` | B |
| jadwal: timer systemd, cron, smartd | dibaca tanpa root | baca |

**Kenapa helper, bukan sudoers.** Aturan 2 di atas meminta sudoers
per-perintah. Ternyata sudo sama sekali tidak bisa dipakai: `issboard.service`
berjalan dengan `NoNewPrivileges=yes`, yang mematikan setuid — dan melepasnya
berarti melemahkan seluruh unit demi beberapa perintah. Jadi aksi root
dikerjakan `issboard-helper`: binary kecil milik root yang dinyalakan systemd
**per koneksi** (`issboard-helper.socket`, `Accept=yes`), mengerjakan satu
permintaan, lalu keluar. Tetap nol proses saat tidak dipakai. Socket-nya
`0660 root:issboard` — izin itulah pembatasnya, dan kemampuan helper adalah
daftar tertutup di `internal/ops`, tanpa aksi "jalankan perintah ini".

**Daftar-putih dua kali.** issboard mencocokkan target dengan daftar yang
ditampilkannya (pool dari `zpool list` + saringan `pools:`, disk dari cache
SMART, container dari daftar Docker yang **segar**). Helper **tidak
mempercayai** issboard dan mencocokkan lagi dengan `zpool list` dan
`smartctl --scan`-nya sendiri. Argumen `-d tipe` untuk smartctl diambil dari
hasil scan, bukan dari permintaan. Test mengunci bahwa target seperti
`kolam;reboot`, `kolam/data`, atau `-s` tidak pernah sampai ke exec.

**Hapus container adalah kelas C**, dan diperlakukan begitu: hanya untuk
container yang **sudah berhenti** (yang jalan ditolak, bukan di-force), volume
**tidak** ikut dihapus, dan konfirmasinya meminta **mengetik nama container**.
Pertimbangan "tombol untuk hal yang jarang, mudah salah, tak bisa dibatalkan"
di bawah tetap berlaku — yang membuatnya layak di sini adalah `docker compose
up -d` bisa membuatnya lagi selama compose-nya ada.

**Batas ARC** punya dua jebakan yang sekarang terlihat di UI, bukan ditebak:

- ZFS **diam-diam mengabaikan** `zfs_arc_max` yang ≤ `zfs_arc_min` atau
  < 64 MiB. Penulisan ke `/sys` tetap "berhasil". Helper menolak di luar
  rentang, lalu membaca ulang `c_max` untuk membuktikan nilainya dipakai.
- Simpan permanen menulis **hanya** `/etc/modprobe.d/issboard-zfs-arc.conf`.
  Kalau `zfs_arc_max` sudah diatur di berkas lain, permintaannya **ditolak**
  sambil menyebut berkas itu: modprobe membaca menurut abjad dan yang terakhir
  menang, jadi menulis berkas yang kalah lalu melapor "tersimpan" adalah
  kebohongan. Root di ZFS juga butuh `update-initramfs -u` — ditulis di
  konfirmasinya.

**Quick test = SMART short self-test**, dijawab dari pertanyaan di bawah. Ia
**membangunkan disk**, dan konfirmasinya menyebut itu — termasuk kalau disknya
sedang tidur saat itu. Hasilnya baru terlihat setelah tes selesai **dan** cache
SMART diperbarui; tombol "segarkan cache" ada untuk itu, dan tetap memakai
`-n standby`.

**Jadwal.** Cockpit menampilkan timer systemd, tapi tidak cron dan tidak
smartd — padahal Debian menjadwalkan scrub bawaannya lewat
`/etc/cron.d/zfsutils-linux`. Panel ini menjejerkan ketiganya, dan mencatat
kalau scrub dijadwalkan **dua kali** (cron *dan* `zfs-scrub-*.timer`) atau
**tidak sama sekali**. `e2scrub_all` milik ext4 sengaja tidak dihitung sebagai
scrub ZFS.

## Bagian 2, dikerjakan hari yang sama: dataset, snapshot, sanoid

| Aksi | Kelas | Catatan |
|---|---|---|
| buat snapshot (opsional `-r`) | A | nama otomatis `issboard_<waktu>[_tag]` |
| hapus **satu** snapshot | **C** | tanpa `-r`/`-R` selamanya; konfirmasi ketik nama |
| buat dataset anak | B | `zfs create -u`, lalu mount lewat `systemd-run` |
| ubah properti | B | daftar tertutup: compression, atime, relatime, recordsize, readonly, quota, refquota, reservation, refreservation |
| jalankan sanoid sekarang | A | `systemctl start sanoid.service` — unit yang sama dengan timernya |
| potongan `sanoid.conf` untuk dataset tak tercakup | baca | **tidak menulis** berkas milik sanoid |

**Nama baru tidak bisa di-daftar-putih.** Tag snapshot dan nama anak dataset
belum ada di sistem, jadi aturan 3 tidak bisa diterapkan pada keduanya. Yang
dipakai sebagai gantinya: himpunan karakter sempit (`[A-Za-z0-9_.:-]`, diawali
huruf/angka, tanpa `/` dan `..`), dan selalu jadi satu argumen utuh di belakang
bagian yang **sudah** lolos daftar-putih — induk dataset, atau dataset yang
di-snapshot. Test mengunci bahwa `-r`, `-o`, `../etc`, `a/b`, dan nilai properti
seperti `lz4 -o x=y` tidak pernah sampai ke exec.

**Awalan `issboard_`** membedakan snapshot manual dari buatan sanoid
(`autosnap_`). sanoid tidak memangkas yang bukan miliknya, jadi snapshot manual
**tidak pernah kedaluwarsa sendiri** — konfirmasinya menyebut itu, dan daftar
snapshot memberi label *sanoid* / *manual* per baris.

**Mount namespace.** Helper berjalan dengan `ProtectSystem=strict`, yang
membuatkannya mount namespace sendiri. `zfs create` biasa di dalamnya akan
me-mount dataset baru **hanya di namespace helper** — di host ia tampak tidak
ter-mount, lalu hilang begitu helper keluar. Ditemukan saat merancang, belum
dibuktikan di mesin nyata. Jalan keluarnya `zfs create -u` lalu
`systemd-run --wait zfs mount`, yang berjalan di namespace host. Kalau mount-nya
gagal, laporannya memisahkan dua fakta: dataset **sudah** dibuat, mount-nya
belum.

**Properti yang sengaja tidak ada**: `mountpoint`, `canmount`, `encryption`,
`sharenfs`/`sharesmb`. Salah isi di sana memindahkan atau menyembunyikan data,
bukan sekadar mengubah perilakunya.

## Sesudah dipasang pertama kali (29 September 2026)

Tiga hal yang tidak terlihat di mesin pengembangan:

1. **Laci samping terlalu sempit untuk pekerjaan admin.** Diganti halaman
   sendiri, `admin.html` (di tab yang sama — tab baru tiap bolak-balik ternyata
   cuma menumpuk): layar masuk penuh, navigasi per
   bagian lewat hash (`admin.html#dataset` bisa di-bookmark), tabel yang
   memakai lebar layar dan jatuh jadi kartu di HP, riwayat aksi sesi ini, dan
   kabar hasil aksi yang terlihat di bagian mana pun. Dashboard kembali jadi
   halaman baca murni — tidak ada satu pun tombol yang mengubah host di sana.
   Helper bersama keduanya pindah ke `common.js`.
2. **CDN menyajikan CSS lama bersama HTML baru.** Lewat domain di belakang
   Cloudflare, panel tampil tanpa gaya sama sekali; lewat tailnet benar. `.css`
   dan `.js` di-cache di edge menurut ekstensinya, dan aset ter-embed tidak
   punya validator. Sekarang semua aset `Cache-Control: no-cache`, API
   `no-store` (`/session` membawa token CSRF).
3. **Di belakang cloudflared semua orang adalah 127.0.0.1.** Pembatas login
   jadi satu hitungan global — lima tebakan salah dari siapa pun mengunci
   pemiliknya — dan audit mencatat alamat yang sama untuk semua. Alamat asli
   dibaca dari `CF-Connecting-IP`/`X-Forwarded-For`, **hanya** kalau
   koneksinya dari loopback; dari tailnet header itu bisa dipalsukan.

Menyusul hari yang sama:

4. **`no-cache` saja ternyata tidak cukup.** Cloudflare menimpa header dari
   issboard dengan `max-age=14400` ("Browser Cache TTL" bawaannya), jadi browser
   tetap menyimpan CSS 4 jam. Sekarang setiap CSS/JS yang dirujuk HTML diberi
   versi isinya (`style.css?v=3923b935ee`, per berkas), aset bertanda versi
   `immutable`, HTML `no-cache`. Tidak bergantung pada pengaturan CDN mana pun.
5. **Catatan "scrub dua kali" tidak padam setelah diperbaiki.** Pemilik mematikan
   scrub cron Debian dengan cara yang dianjurkan Debian —
   `zfs set org.debian:periodic-scrub=disable` di tiap pool — tapi catatannya
   hanya melihat baris cron *ada*. Sekarang properti itu dibaca, dan baris cron
   yang akan melewati semua pool ditandai *tidak aktif*. Catatan yang tidak bisa
   dipadamkan dengan memperbaiki masalahnya akan diabaikan orang.

Dan satu yang justru bekerja seperti dirancang: di mesin nyata `zfs_arc_max`
sudah diatur di `/etc/modprobe.d/zfs.conf`, dan helper menolak "simpan
permanen" sambil menyebut berkas itu. Halaman kelola sekarang mematikan pilihan
itu di depan dan menjelaskan jalan keluarnya, bukan menunggu ditolak.

## Pengaturan notifikasi & aplikasi (29 September 2026)

Diminta pemilik setelah pemasangan: token dan chat id Telegram harus bisa
diganti tanpa SSH. Bagian **pengaturan** di halaman kelola:

| Pengaturan | Disimpan di | Oleh |
|---|---|---|
| token & chat id Telegram, server/topik/token ntfy | `/etc/issboard/agent.env` (0600 root) | helper, hanya kunci yang dikelola |
| level notifikasi, jeda pengingat, idle timeout, `snapshot_exempt`, saringan pool | `/etc/issboard/settings.conf` (timpaan) | helper |
| kirim tes | — | `systemd-run` sebagai user `issboard` dengan `agent.env` yang sama |

Keputusan yang mengikat:

- **Nilai rahasia tidak pernah kembali ke browser.** Helper hanya melaporkan
  "terisi" + empat karakter terakhir. Kolom rahasia yang kosong = tidak diubah;
  menghapus harus dicentang.
- **Log audit helper tidak mencatat nilainya** — hanya nama kunci. Journal
  dibaca grup `adm`; token yang tercatat di sana sama dengan token yang bocor.
  Ditemukan saat menulis fitur ini: log audit semula mencetak seluruh `props`.
- **Nilai divalidasi dengan himpunan karakter sempit**, bukan "tidak kosong":
  ia ditulis ke berkas `KUNCI=NILAI` yang dibaca systemd, dan baris baru di
  dalam nilai akan menyelundupkan variabel lingkungan apa pun (`LD_PRELOAD`)
  ke proses agent. Dikunci test.
- **`issboard.yaml` tidak disunting.** Ia conffile paket; sekali disunting
  mesin, tiap upgrade yang mengubah contoh bawaannya berhenti dan bertanya.
  Timpaan `settings.conf` dibaca sesudahnya, dan halaman menampilkan asal tiap
  nilai (bawaan / `issboard.yaml` / halaman ini) beserta tombol "kembalikan".
- **Kunci jalur berkas tidak bisa diatur dari browser** (`history_file`,
  `docker_socket`, `auth_file`, …): mengarahkannya ke tempat lain adalah cara
  memindahkan apa yang dibaca atau ditulis proses berhak lebih.
- **Tes kirim lewat `systemd-run`**, bukan dari helper langsung: helper tidak
  punya jaringan (dan anaknya mewarisi itu), dan tes di lingkungan lain dari
  timernya tidak membuktikan apa pun tentang timernya. Tes tidak menandai
  temuan apa pun sebagai sudah dikabari.
- Dashboard memuat ulang config sesudah pengaturan aplikasi disimpan, jadi
  saringan pool dan `snapshot_exempt` berlaku di permintaan berikutnya.

### Belum dikerjakan, dengan sengaja

- `zfs destroy` **dataset**, `zfs rollback`, `zpool destroy` — alasan di
  bagian "Aksi tidak satu kelas" di atas tetap berlaku: jarang, mudah salah,
  tanpa undo, dan lewat SSH hanya beberapa detik.
- `zpool create` — menyentuh disk mentah; butuh UI yang menyebut bentuk vdev
  dengan kata. Belum ada kebutuhan nyata di mesin ini.

Kalau salah satunya tetap diinginkan, syarat minimal di atas (ketik ulang nama,
tampilkan yang akan hilang, audit sebelum jalan) sudah punya semua bahannya:
`mutate`, dialog ketik-nama, dan daftar-putih helper.

### Terbukti di host sungguhan (29 September 2026)

- aksi container (start) lewat socket Docker
- penolakan "simpan permanen" ARC saat `zfs_arc_max` sudah diatur berkas lain,
  tercatat di audit issboard dan helper
- `systemctl list-timers --output=json` dari dalam `issboard.service` yang
  dikeraskan
- catatan scrub ganda — benar menyala, lalu padam setelah
  `org.debian:periodic-scrub=disable` dipasang di tiap pool
- helper membaca `agent.env` tanpa nilai rahasia masuk journal
- **kirim tes** lewat `systemd-run` sebagai `issboard`: pesan uji sampai di grup
- alamat pengunjung asli lewat cloudflared (bukan 127.0.0.1)

### Belum terbukti

- `zfs create -u` + `systemd-run zfs mount` — apakah dataset baru benar-benar
  terlihat ter-mount di host
- scrub dan SMART self-test lewat helper yang dikeraskan
  (`ProtectKernelModules`, `RestrictAddressFamilies`, `MemoryDenyWriteExecute`)
- `zpool scrub -p` di pool yang tidak sedang di-scrub
- menulis kunci baru ke `agent.env` dan `settings.conf` dari halaman kelola

## Ini mengubah desain, bukan menambah fitur

Kalimat itu sudah ditulis sejak revisi plan 13 Agustus 2026, dan di sinilah ia
ditagih:

> Aplikasi read-only boleh asal-asalan soal auth, CSRF, validasi input, dan
> audit; aplikasi yang bisa `zfs rollback` tidak boleh.

Sampai hari ini issboard **tidak punya satu pun jalur** yang menerima input
pengguna lalu meneruskannya ke `exec`. Perlindungan itu bukan hasil kode yang
hati-hati — ia hasil dari tidak adanya jalurnya. Fase ini mencabutnya, dan
harus mengganti dengan pengerasan yang eksplisit.

## Tiga aturan yang sudah mengikat sejak sekarang

Dari [`../design.md`](../design.md) §8, ditulis jauh sebelum panel ini ada:

1. **Autentikasi lebih dulu.** Fase 7. Tidak ada satu pun endpoint bermutasi
   yang boleh mendahuluinya.
2. **Hak akses per kebutuhan, bukan `NOPASSWD: ALL`.** Godaannya paling besar
   tepat saat aksi pertama yang butuh root ditambahkan. Tuliskan perintah
   spesifiknya di sudoers.
3. **Validasi nama dataset/pool dengan daftar-putih, bukan regex.** Regex
   meloloskan `pool/app@../..`. Ambil daftar nyata dari sistem, cocokkan
   persis, tolak sisanya.

Aturan 3 ternyata **sudah setengah jadi**: fase 6 sudah mengambil daftar
dataset nyata beserta kebijakan snapshot yang berlaku untuk masing-masing.
Daftar itu persis bentuk daftar-putih yang diminta di sini — jadi jangan
membangun yang kedua, pakai yang sudah ada.

## Aksi tidak satu kelas — dan itu harus terlihat

Daftar yang diminta memuat hal-hal yang akibatnya berbeda jauh. Memberi
semuanya tombol berbentuk sama adalah cara paling rapi untuk kehilangan data.

| Kelas | Aksi | Sifat |
|---|---|---|
| **A — bisa diulang** | `zfs snapshot`, mulai/hentikan scrub, jalankan sanoid kering | Tidak menghilangkan apa pun. Salah klik = tidak ada akibat. |
| **B — mengubah** | buat dataset, ubah properti (quota, compression) | Bisa dibatalkan, tapi tidak otomatis. |
| **C — menghancurkan** | `zfs destroy`, `zpool destroy`, `zfs rollback` | **Tidak ada undo.** `zpool destroy` menghapus seluruh pool. |

**Usulan untuk v1 panel: kelas A saja, lalu B.** Kelas C sengaja ditinggalkan.

Alasannya bukan kehati-hatian umum, tapi hitungan yang spesifik: `zfs destroy`
lewat SSH butuh beberapa detik dan dilakukan beberapa kali setahun — nilai yang
ditambahkan tombol nyaris nol. Sedangkan biayanya tak terbatas dan tak bisa
ditarik. Sesuatu yang jarang dipakai, mudah salah, dan tidak bisa dibatalkan
adalah kandidat terburuk untuk dijadikan tombol.

Kalau kelas C tetap diinginkan nanti, syarat minimalnya: ketik ulang nama
targetnya, tampilkan apa yang akan hilang (ukuran, jumlah snapshot, anak-anak),
dan catat di log audit sebelum dijalankan — bukan sesudah.

`zpool create` juga bukan kelas B biasa. Ia menyentuh **disk mentah**, dan
memilih disk yang salah menghancurkan isinya tanpa peringatan ZFS. Ia juga
tempat lahirnya temuan "stripe, bukan mirror" yang sudah ada di dashboard ini —
`zpool add` di tempat `zpool attach`. Kalau dibuat, UI-nya harus menyebut
**bentuk vdev-nya dengan kata**, bukan cuma daftar disk bercentang.

## Yang dikerjakan

- [x] Fase 7 selesai lebih dulu. Ini bukan urutan yang disarankan, ini syarat.
- [x] Bentuk API bermutasi: `POST /api/v1/...`, bukan GET. GET yang mengubah
      keadaan akan dijalankan oleh prefetch browser dan crawler.
- [x] Daftar-putih dari sistem — dua kali, di issboard dan di helper.
- [x] ~~sudoers per-perintah~~ → helper root per-koneksi, karena
      `NoNewPrivileges` mematikan sudo (lihat di atas).
- [x] Log audit tiap mutasi: siapa, apa, target, hasil — di issboard **dan**
      di helper (`journalctl -u 'issboard-helper@*'`).
- [x] Side panel di FE. Tetap Nothing OS, tetap tanpa pustaka luar, tetap CSP
      `default-src 'self'`.
- [x] Aksi kelas A dulu: snapshot manual, mulai/hentikan scrub.
- [x] Status scrub yang sedang berjalan — baris `scan:` ditampilkan apa adanya,
      dan tombolnya mengikuti keadaan (mulai / jeda / lanjutkan / hentikan).
- [x] Test: setiap endpoint bermutasi tanpa sesi → 401; nama di luar
      daftar-putih → 400; dan **tidak satu pun** input pengguna sampai ke
      `exec` tanpa melewati daftar-putih.

## Yang masih perlu diputuskan

- **"Quick test" itu apa?** Kalau maksudnya SMART short self-test
  (`smartctl -t short`), ia **membangunkan disk yang sedang tidur** — dan
  seluruh jalur SMART di proyek ini dibangun justru untuk menghindari itu
  (§3.3, dan cacat #2 di [`05-pemasangan.md`](05-pemasangan.md)). Sebagai aksi
  yang diminta manusia secara sadar itu sah, tapi harus **eksplisit** bahwa
  disknya akan dibangunkan. Kalau yang dimaksud lain — tes tulis-baca, atau
  `zpool status -v` cepat — perlu dijelaskan dulu.
- **Sanoid: baca saja, atau jalankan?** Menyunting `sanoid.conf` dari dashboard
  berarti issboard menulis berkas milik program lain. Alternatif yang lebih
  aman: tunjukkan potongan config yang **perlu ditambahkan**, biar orangnya
  yang menempel. Itu menyelesaikan `tank-main/media` tanpa satu pun jalur tulis.
- **Panel ini menjangkau host lain atau tidak?** Sampai sekarang issboard
  single-host dan itu keputusan sadar.
