# Fase 5 — Pemasangan pertama di mesin sungguhan

**Status: ✅ selesai** (21 Agustus 2026), dengan sisa yang ditandai jelas di
bawah. Konteks host nyatanya ada di `docs/konteks-lokal/` yang diabaikan git.

Fase yang tidak ada di rencana awal, dan ternyata paling banyak menemukan
cacat. Sebelum ini issboard hanya pernah jalan di mesin pengembangan — yang
kebetulan **tidak punya ZFS, tidak punya container, dan tidak punya satu pun
disk yang tidur**.

## Yang terbukti bekerja

Bukan diasumsikan; diukur di host sungguhan:

| Yang diuji | Hasil |
|---|---|
| Pengerasan systemd vs `zpool` | tidak diblokir — kekhawatiran terbesar ternyata tidak terjadi |
| `zpool`/`zfs` sebagai non-root | jalan; tidak perlu unit root seperti SMART |
| Socket activation | HTTP 200 dalam 0,17 dtk dari keadaan mati |
| RAM saat melayani | **12 MB**, dan nol saat halaman tidak dibuka |
| Socket container lewat grup | 25 container terbaca |
| Timer SMART + `-n standby` | cache terisi, disk yang tidur tetap tidur |
| Paket `.deb` | pasang & upgrade bersih, config admin tidak tertimpa |

`zfs list -t snapshot` untuk 653 snapshot: **73 ms**. Kecurigaan bahwa
penghitungan snapshot perlu dipindah ke agent tidak terbukti — dibatalkan.

## Enam cacat yang hanya bisa ditemukan di sini

Diurutkan sesuai urutan munculnya. **Tidak satu pun bisa muncul di mesin
pengembangan**, dan itu inti pelajarannya.

1. **19 dari 25 temuan cuma kebisingan.** Aturan "port ter-publish ke
   `0.0.0.0`" menyala untuk keadaan yang justru normal — begitulah aplikasi
   web dijangkau. Ditambah tiap port dihitung **dua kali**, karena
   `-p 3000:3000` menghasilkan entri IPv4 dan IPv6 terpisah. Setelah
   dipersempit ke layanan yang benar-benar berbahaya: **25 → 5 temuan**.

2. **Alarm KRITIS palsu untuk disk yang tidur.** `smartctl -n standby` sengaja
   tidak membangunkan disk, jadi disk yang tertidur mengembalikan berkas
   kosong — termasuk `passed` yang kosongnya berarti `false`. Terbaca sebagai
   "SMART gagal". Ketiadaan data dinilai sebagai data buruk, kesalahan yang
   sama persis dengan membaca suhu 0 sebagai dingin.

3. **Versi paket dari hash commit tidak berurutan.** Hash yang kebetulan
   diawali angka lolos dari penjaga versi. apt memakai versi untuk memutuskan
   upgrade; hash membuat build berikutnya bisa terlihat lebih tua.

4. **Upgrade tidak mengganti proses yang sedang jalan.** Socket activation
   berarti binary di disk boleh berganti sementara proses lama terus melayani
   sampai idle-exit — dan kalau ada yang sedang memantau halamannya, idle itu
   tidak pernah datang. Perbaikan yang baru dipasang tidak pernah aktif, tanpa
   satu pun pesan error.

5. **Alamat listen kedua mematikan dashboard sepenuhnya.** systemd menyerahkan
   satu fd per `ListenStream`; kode hanya menerima `LISTEN_FDS=1` dan untuk
   selain itu jatuh ke jalur cadangan — bind ke alamat yang justru sedang
   dipegang systemd. Proses mati berulang sampai socket-nya ikut kena *start
   limit*. Yang terlihat di systemd cuma `service-start-limit-hit` pada socket
   yang justru sukses bind; **tidak ada satu pun pesan yang menunjuk ke
   penyebabnya**.

6. **Pil "DATA PALSU" muncul di data yang asli.** `[hidden]` bawaan browser
   punya spesifisitas serendah mungkin, jadi `.n-pill { display: inline-flex }`
   mengalahkannya. Di mode demo pil itu memang harus tampil, jadi bug-nya
   tidak pernah terlihat sampai dijalankan dengan data asli.

## Pelajaran yang berlaku di luar proyek ini

- **Aturan baru harus diuji terhadap mesin yang SEHAT**, bukan cuma terhadap
  mesin yang sakit. Aturan yang menyala untuk keadaan normal melatih orang
  mengabaikan seluruh daftarnya, dan sesudah itu temuan yang sungguhan ikut
  tidak terbaca. (#1)
- **Ketiadaan data bukan data buruk.** Muncul dua kali di proyek ini — suhu 0
  dan `passed` kosong — dan dua-duanya menghasilkan kebohongan ke arah yang
  berlawanan dengan kenyataan. (#2)
- **Data demo hanya sebaik imajinasi penulisnya.** Yang mengarangnya tahu
  disknya sehat, jadi ia menulis `standby: true` bersama `passed: true`. Hanya
  mesin sungguhan yang punya disk yang benar-benar tidur. (#2)
- **Upgrade yang memperbaiki crash juga harus membersihkan akibat crash-nya** —
  proses lama yang masih melayani, dan status `failed` yang tertinggal. Kalau
  tidak, perbaikannya terlihat seperti tidak bekerja dan orang mencari di
  tempat yang salah. (#4, #5)

## Yang masih tersisa

- ~~Notifikasi belum pernah benar-benar terkirim~~ — **terkirim lewat Telegram
  sejak 21 Agustus 2026, 14:10.** Lima siklus sebelumnya melaporkan "tidak ada
  kanal notifikasi yang dikonfigurasi" dan **sengaja tidak menandai sudah
  dikabari**; begitu token diisi, kondisi yang sedang berlangsung langsung
  terkirim sekali tanpa menunggu sesuatu memburuk dulu. Keputusan itu ditulis
  sebagai komentar di `cmd/issboard-agent/main.go` jauh sebelum ada kanal yang
  bisa membuktikannya, dan ia bekerja persis seperti yang dijanjikan.
  Jalur ntfy tetap hanya pernah diuji lawan server tiruan lokal.
- ~~`alert_repeat` 24 jam belum pernah terpicu~~ — **terpicu empat kali**, 22–25
  Agustus, "5 masih berlangsung". Jaraknya 24j00m04d, 24j00m54d, 24j00m32d,
  24j00m45d: pengingatnya adalah **lantai, bukan jadwal** — ia menyala pada
  siklus pertama sesudah 24 jam lewat, jadi jitter timer semenit menumpuk
  pelan-pelan dan tidak pernah menyusut. Tidak apa-apa untuk pengingat, dan
  perlu diingat kalau suatu saat ada yang mengharapkannya presisi.
- ~~Belum jalan semalaman~~ — **terisi penuh per 25 Agustus 2026**: lapis halus
  60/60 titik (1 jam) dan lapis kasar 48/48 titik (24 jam), seluruhnya data
  sungguhan. Diperiksa jaraknya, bukan cuma jumlahnya — ring buffer yang penuh
  belum tentu tanpa lubang: jarak antar titik 60–75 dtk di lapis halus dan
  1804–1845 dtk di lapis kasar, **nol lompatan**. Agent bertimer memang jalan
  tiap menit tanpa terlewat, dengan jitter systemd yang wajar.
- **Cloudflare Tunnel ditunda.** Syaratnya sudah jelas — Cloudflare Access
  harus lebih dulu, lihat `design.md` §8.1. Akses saat ini lewat tailnet.
- SELinux, Podman, dan arsitektur selain amd64 tetap belum diuji
  (`docs/distro.md`).
