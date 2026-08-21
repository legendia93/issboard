# Fase 4 — Test, paket, dan distro lain

**Status: ✅ selesai** (21 Agustus 2026). Yang belum diverifikasi dikumpulkan
di [`../distro.md`](../distro.md) dan ditandai jelas di sana.

Fase yang mengubah "jalan di mesinku" jadi "bisa dipasang orang lain".

## Test

- [x] `zpool list -Hp` & `zpool status` — mirror vs stripe vs raidz, baris
      `scan:` dalam berbagai bentuk, penghitung error, `logs`/`cache`/`spares`
      yang tidak boleh terhitung sebagai disk data.
- [x] Pembacaan cache SMART, termasuk **deteksi basi** dan batas 12 jam.
- [x] JSON Docker — network kosong, port `0.0.0.0` dan `::`, port loopback
      yang TIDAK boleh terdaftar, urutan yang harus stabil antar refresh.
- [x] `internal/health` — tiap aturan vonis, dengan data demo sebagai fixture.
- [x] `internal/history` — ring buffer, rata-rata lapis kasar, disk tidur yang
      tidak dicatat, janji berkas < 20 KB.
- [x] `internal/alert` — seluruh mesin de-duplikasi, termasuk jalur yang paling
      mudah salah: kiriman gagal harus dicoba lagi, dan siklus yang
      pengumpulannya error tidak pernah melaporkan "pulih".
- [x] `internal/notify` — header ntfy, prioritas, token, pembersihan judul.
- [x] `internal/config` — nilai berisi `:`, baris ngawur, timpaan lingkungan.
- [x] `internal/api` — header CSP, catatan riwayat basi, dan bukti endpoint
      riwayat tidak menulis apa pun.

Cakupan: alert 89%, api 95%, health 89%, history 93%, config 81%, notify 78%.
`internal/collector` rendah (30%) dan memang begitu adanya — sisanya membaca
`/proc` dan memanggil biner luar, yang tidak berguna dites dengan tiruan.

## Yang ditemukan saat menulis test

Dua bug nyata, keduanya ketahuan justru karena kodenya dipisah agar bisa diuji:

1. **`Health` container tidak pernah diisi.** Docker tidak memberi field
   healthcheck terpisah di `/containers/json` — satu-satunya tempatnya adalah
   embel-embel di `Status`, seperti `Up 2 hours (unhealthy)`. Akibatnya aturan
   "healthcheck gagal" cuma pernah menyala di mode demo, tidak pernah di data
   sungguhan. Itu bentuk kebutaan yang paling menyesatkan: aturannya ada,
   test-nya hijau di demo, tapi di mesin nyata diam.
2. **`parsePoolStatus` bergantung pada pemanggil.** Ia melewati baris nama pool
   dengan mencocokkan `p.Name`, jadi kalau pemanggil lupa mengisinya lebih
   dulu, baris nama pool ikut terhitung sebagai disk — dan **pool mirror
   terbaca sebagai stripe**, kebalikan persis dari temuan terpenting dashboard
   ini. Sekarang parser membaca sendiri nama pool dari baris `pool:`.

## Paket

| Jalur | Status yang dijanjikan | Berkas |
|---|---|---|
| `.deb` | **direkomendasikan**, jalur utama | `packaging/build-deb.sh` |
| `install.sh` | didukung | `install.sh` (POSIX sh, ada `--uninstall`) |
| tarball generik | didukung | `packaging/build-tarball.sh` |
| Docker | didukung, dengan peringatan jujur | `Dockerfile`, `packaging/docker-compose.yml` |
| Docker **+ ZFS** | **tidak** didukung penuh — lihat di bawah | — |

Paketnya sengaja tidak memakai debhelper, fpm, atau GoReleaser: cukup
`dpkg-deb` yang sudah ada di tiap sistem Debian. Isinya memang sederhana —
dua binary statis, satu skrip, beberapa unit — dan rantai build yang lebih
panjang daripada isinya adalah beban yang tidak dibayar kembali.

Diverifikasi: `.deb` terbangun dan isinya benar (jalur unit ditulis ulang dari
`/usr/local` ke `/usr`, binary statis, `agent.env` mode 0600, config ditandai
*conffile*); image Docker terbangun dan container-nya benar-benar melayani
`/api/v1/status`.

## Docker: janjinya harus jujur

Dashboard kesehatan seharusnya tetap hidup justru saat Docker rusak — itu
alasan `design.md` §3.1 memilih binary di host. Container tetap disediakan
karena itu jalur coba-coba yang paling mudah, tapi README harus menyebut:

1. **Kalau Docker mati, dashboard ikut mati.** Ini trade-off, bukan bug.
2. **Binary `zfs`/`zpool` di dalam image harus cocok versinya dengan modul
   kernel ZFS di host.** Host zfs 2.1 + image zfs 2.2 = error, dan versi ZFS
   penonton tidak bisa dikontrol. Ini sumber issue terbesar kalau dijanjikan.
3. Butuh `/dev/zfs`, akses `/dev/sd*` untuk SMART, dan longgar di SELinux.

## Distro lain (Debian tetap utama)

- **SELinux** (Fedora/RHEL) — `ProtectSystem=strict` + baca socket container +
  tulis cache. Butuh label/policy, bukan sekadar catatan di README.
- **Podman** — path socket `/run/podman/podman.sock`, API kompatibel.
  Kodenya disiapkan, ditandai **belum diuji**; belum ada mesin untuk mengecek.
  Socket rootless (`/run/user/<uid>/podman/podman.sock`) ikut dicatat di
  `docs/distro.md`.
- **Non-systemd** (Alpine/Void) — fallback `net.Listen` sudah ada di `main.go`.
  ✅ **`idle_timeout` kini otomatis nonaktif** kalau tidak ada socket
  activation, dan alasannya dicatat di log saat start. Ini memang bug yang
  menunggu terjadi, dan sudah terbukti di container: tanpa penjaga ini,
  dashboard-nya akan keluar setelah 5 menit dan tidak ada apa pun yang
  menyalakannya lagi. Tidak diserahkan ke pengguna untuk ingat menulis
  `idle_timeout: 0` sendiri.
- Path unit systemd `/lib` vs `/usr/lib`.
- ZFS tidak universal (DKMS di Fedora/RHEL, archzfs di Arch) — sudah aman
  karena kegagalan per-bagian, tapi README jangan berasumsi.

## Yang ketahuan saat dipasang sungguhan

Dua hal yang tidak mungkin muncul di mesin pengembangan, keduanya soal
pemasangan — bukan soal kode dashboard-nya:

1. **Versi paket dari hash commit tidak berurutan.** `git describe` di repo
   tanpa tag mengembalikan hash telanjang, dan hash yang kebetulan diawali
   angka lolos dari penjaga versi. apt memakai versi untuk memutuskan apakah
   paket berikutnya sebuah upgrade — hash membuat build berikutnya bisa
   terlihat lebih tua. Sekarang tulang punggungnya jumlah commit.

2. **Upgrade tidak mengganti proses yang sedang jalan.** Socket activation
   berarti binary di disk boleh berganti sementara proses lama terus
   melayani sampai idle-exit — dan kalau ada yang sedang memantau
   halamannya, idle itu tidak pernah datang. Perbaikan yang baru dipasang
   jadi tidak pernah aktif. `postinst` sekarang menghentikan
   `issboard.service` (BUKAN socket-nya), jadi koneksi berikutnya dilayani
   binary baru — tanpa downtime, karena socket-lah yang memegang port.

## Yang belum diuji

Dikumpulkan lengkap di [`../distro.md`](../distro.md), diringkas di sini:

- **SELinux enforcing** — belum pernah dicoba sama sekali. Tiga hal yang
  kemungkinan butuh policy: membaca socket container, menulis cache SMART
  sebagai root lalu membacanya sebagai user lain, dan menulis
  `/var/lib/issboard` dari unit bertimer.
- **Podman**, rootful maupun rootless.
- **Unit systemd dijalankan systemd sungguhan.** Pengerasan unit sudah lolos
  `systemd-analyze verify`, tapi itu cuma memeriksa bentuk berkasnya — bukan
  apakah `SystemCallFilter` diam-diam memblokir `zpool`.
- **Arsitektur selain amd64.** Skrip paket menerima `ARCH=arm64` dan
  seterusnya, tapi hasilnya belum pernah dijalankan di mesin ARM.
