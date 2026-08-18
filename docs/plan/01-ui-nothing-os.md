# Fase 1 — UI Nothing OS + mode demo

**Tujuan:** mengubah halaman HTML polos jadi dashboard yang enak dilihat,
terutama **di HP** (host diakses lewat remote dari HP), tanpa build step,
tanpa dependensi, tanpa aset pihak ketiga.

## Prinsip

**Sehat itu pastel, bermasalah itu pekat.**

Basisnya tetap Nothing OS: off-white / hitam pekat, garis 1px, radius squircle,
label mono uppercase. Lalu:

- **Pastel = identitas data, bukan status.** Tiap pool/disk dapat satu pastel,
  dipakai untuk bar kapasitas & (nanti) garis sparkline.
- **Warna pekat hanya untuk masalah.** Amber = perhatian, merah Nothing = kritis.
- Akibatnya **layar yang sehat sepenuhnya tenang**; satu warna pekat muncul dan
  mata langsung ke sana. Itu memang tugas dashboard ini.

**Dilarang** (diwarisi dari `nothing.css` lookna, dan tetap berlaku di sini):
gradient warna, glow, glass/blur, emerald. Tambahan: **tidak ada animasi yang
jalan terus-menerus** — melelahkan dan membuang daya HP.

## Yang dikerjakan

- [x] Port token `nothing.css` dari `lookna` ke `web/nothing.css`, ditambah
      lapisan pastel (`--p-*` fill + `--p-*-line` stroke) dan token status.
- [x] Embed font **Ndot** (Pixelify Sans, OFL) ke binary — ±79 KB. Dipakai untuk
      angka besar & vonis hero. Font lain memakai stack sistem, karena CSP
      `default-src 'self'` melarang aset eksternal dan itu memang disengaja.
- [x] `web/index.html` jadi **bento grid**; satu kolom penuh di HP.
- [x] **Kartu hero hitam** dengan vonis besar: `SEHAT` / `n PERLU PERHATIAN`.
- [x] `internal/health/` — aturan vonis **di Go, bukan di JavaScript**, supaya
      fase 2 (notifikasi) memakai ulang aturan yang persis sama. Lihat
      [`02-agent.md`](02-agent.md).
- [x] **Mode demo** (`-demo`): data palsu yang generik, termasuk kondisi sakit
      (pool stripe, disk reallocated, container `Up` tanpa network, port
      ter-publish ke `0.0.0.0`, cache SMART basi).
- [x] Sisakan ruang sparkline berukuran tetap di kartu metrik, supaya fase 3
      mengisi tanpa menggeser layout.

## Kenapa mode demo dikerjakan sekarang, bukan nanti

Bukan demi video. Tanpa data palsu, tampilan **disk sekarat** atau **pool
penuh** baru bisa dilihat kalau hal itu benar-benar terjadi di mesin nyata —
mustahil menggarap aturan "sehat pastel, bermasalah pekat" seperti itu.

Efek sampingnya kebetulan penting: dashboard ini menampilkan hostname, IP, nama
pool, dan nama app. Mode demo membuat **screenshot dan rekaman layar aman**,
persis hal yang `.gitignore` repo ini susah payah jaga.

## Putaran revisi setelah dilihat di HP

- **Metrik host digabung jadi SATU kartu**, bukan lima kartu terpisah. Metrik
  host saling menjelaskan — CPU tinggi bersama swap terisi bercerita lain
  daripada CPU tinggi sendirian — dan lima kartu membuat halaman dua kali
  lebih panjang di HP.
- **Chip di section container & dataset meluber keluar kartu di HP.** Chip
  duduk di kolom `auto` baris pertama, dan teks seperti `0.0.0.0:5432->5432/tcp`
  yang tidak boleh dipenggal memaksa kolom itu melebar. Sekarang chip turun ke
  barisnya sendiri dan boleh berganti baris di dalam dirinya.
- **Vonis hero tidak lagi kapital semua.** "6 KRITIS / 8 PERHATIAN" membuat
  teksnya berebut perhatian dengan angkanya. Sekarang angka besar + kata kecil
  huruf biasa: "6 — perlu ditangani sekarang".
- **Tiap metrik bisa diketuk untuk penjelasan**: apa yang diukur, dibaca dari
  mana, dan angka seperti apa yang bermasalah. Sengaja bukan atribut `title`:
  tooltip hover tidak ada di layar sentuh, dan halaman ini paling sering dibuka
  dari HP. Nama seperti "ARC" tidak menjelaskan dirinya sendiri.
- **Metrik sistem ditambahkan** meniru dashboard `lookna`: CPU%, swap, distro,
  kernel, arsitektur, model & jumlah core CPU, suhu CPU.

## Yang ditemukan saat mengerjakan

Dua hal yang tidak terduga, keduanya sudah diperbaiki dan dicatat di
`design.md` bagian 7:

1. **Font dot-matrix tidak boleh dipakai untuk angka.** `52` terbaca `92`,
   `35` terbaca `39`. Ditemukan karena mode demo menampilkan disk 52°C.
   Aturannya sekarang: font dot untuk kata, mono untuk angka.
2. **Pastel butter/peach bertabrakan dengan makna status.** Bar memori 67%
   yang sehat terbaca seperti peringatan. Palet identitas sekarang hanya
   mint / sky / lavender / teal — tanpa kuning, oranye, dan merah.

Selain itu, satu bug lama ikut ketahuan: `errors[]` hanya terisi di permintaan
yang benar-benar mengumpulkan, lalu hilang selama TTL. Di halaman yang polling
tiap 15 detik, host tanpa ZFS jadi hampir selalu terlihat sehat. Error kini
disimpan bersama nilai yang di-cache.

## Selesai kalau

- `go build` bersih, tetap **nol dependensi** di luar pustaka standar.
- `./issboard -demo` menampilkan seluruh kondisi sakit dengan warna pekat, dan
  sisanya pastel & tenang.
- Terbaca nyaman di layar HP.
- Tidak ada satu pun permintaan jaringan ke luar (CSP tetap `default-src 'self'`).
