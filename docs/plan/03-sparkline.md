# Fase 3 — Sparkline

**Status: ✅ selesai** (21 Agustus 2026).

Mengisi ruang berukuran tetap yang sudah disiapkan di fase 1, memakai
`history.json` yang ditulis agent di fase 2. **Tidak ada fase 3 tanpa fase 2.**

## Bentuk berkas yang sudah ada (dari fase 2)

`history.json` sudah ditulis agent, jadi fase ini tinggal membacanya:

```
{ "written_at": ..., "cpu_mark": {...},
  "fine":   { "step_seconds": 60,   "points": [ ... 60 titik ... ] },
  "coarse": { "step_seconds": 1800, "points": [ ... 48 titik ... ] } }
```

Tiap titik: `t` (epoch detik), `load1`, `cpu` (**-1 = tak terhitung**, bukan
0%), `mem_used`, `swap_used`, `arc`, lalu map `pools` (persen terpakai) dan
`disks` (suhu °C). Kunci map-nya nama pool / nama device.

Dua hal yang sudah dijamin penulisnya, dan jangan diulang di sisi pembaca:
disk yang sedang tidur **tidak muncul sama sekali** di `disks` (bukan 0°C),
dan tiap titik membawa `t` sendiri — jadi lubang riwayat terlihat dari jarak
antar `t`, tidak perlu penanda khusus.

## Yang dikerjakan

- [x] Endpoint `GET /api/v1/history` — membaca berkas, tidak mengumpulkan apa
      pun dan tidak pernah menulis. Persis pola cache SMART.
- [x] SVG digambar tangan di `app.js`. **Tidak ada pustaka grafik**: satu
      `<path>` dari deret angka sudah cukup, dan itu menjaga janji nol
      dependensi sekaligus CSP `default-src 'self'`.
- [x] Warna garis pakai `--p-*-line` sesuai pastel yang dipegang pool/disk itu.
- [x] Saat halaman terbuka, poll 15 detik **menyambung titik live ke ekor
      grafik** di memori browser — terasa hidup tanpa daemon di server.
- [x] Riwayat yang bolong (agent mati / mesin baru menyala) digambar **putus**,
      bukan disambung lurus. Garis lurus palsu menyembunyikan justru hal yang
      ingin diketahui: ada periode yang tidak terpantau.
- [x] Tombol **1 jam / 24 jam** yang menukar lapis halus dan lapis kasar.
      Ditambahkan karena tanpa ini lapis kasar 48 titik yang susah payah
      ditulis agent tidak pernah dibaca siapa pun.
- [x] Riwayat demo (`-demo`), dengan lubang yang disengaja supaya aturan
      "bolong digambar putus" bisa dilihat tanpa menunggu agent benar-benar
      mati.

## Yang ditemukan saat mengerjakan

1. **Autoscale murni membesar-besarkan hal sepele.** Pool yang bergerak dari
   62,0% ke 62,4% dalam sejam tergambar seperti tebing. Tiap metrik sekarang
   punya rentang sumbu Y **minimum** — grafiknya baru benar-benar naik kalau
   perubahannya memang berarti. Dashboard yang bikin panik karena skala,
   bukan karena data, adalah kebalikan dari gunanya.
2. **Data demo yang pertama beraliasing.** Angka demo memakai periode ayunan
   90–300 detik (disalin dari `DemoSnapshot`), sedangkan lapis halus mencuplik
   tiap 60 detik — hasilnya gigi gergaji rapi yang mustahil muncul di mesin
   nyata. Sekarang sinyalnya punya dua skala: komponen lambat ~7,5 jam
   (bentuk yang terlihat di grafik 24 jam) dan komponen menitan (yang terlihat
   di grafik 1 jam). Lapis kasarnya juga **dirata-ratakan** memakai `mean()`
   yang sama dengan ring buffer sungguhan, bukan dicuplik satu titik per 30
   menit.
3. **Riwayat harus diambil SEBELUM gambar pertama.** Kalau tidak, kotak
   sparkline sempat kosong sepersekian detik lalu terisi — kedipan yang di HP
   terbaca seperti halaman rusak.
4. **Grafik kosong punya dua arti.** "Mesin baru dipasang" dan "timer agent
   mati" sama-sama menghasilkan halaman yang tenang. Endpoint riwayat sekarang
   mengembalikan `note`, dan halaman menampilkannya — karena agent yang mati
   tidak bisa mengabari bahwa dirinya sendiri berhenti jalan.

## Metrik yang dapat sparkline

Load, CPU, memori terpakai, swap, ukuran ARC, kapasitas tiap pool, suhu tiap
disk. Bukan jumlah container — itu angka yang melompat, bukan tren.

CPU dan swap ikut, di luar daftar awal, karena ruangnya memang sudah dipesan
di fase 1 dan datanya memang ditulis agent; membiarkan dua kotak bertitik-titik
selamanya justru terlihat seperti fitur yang rusak.

Disk yang sedang **tidur tidak punya garis sama sekali**, dan itu benar: agent
tidak mencatat suhu disk standby, karena 0 °C akan terbaca sebagai dingin.
Kotaknya tetap kosong, bukan diisi garis nol yang mengarang.

## Selesai kalau

- [x] `-demo` menampilkan seluruh grafik tanpa menyentuh sistem.
- [x] Lubang riwayat tergambar putus, bukan disambung.
- [x] Tombol rentang menukar 1 jam ↔ 24 jam dan pilihannya diingat.
- [x] Tetap nol dependensi, tetap `default-src 'self'`, tidak ada animasi
      yang jalan terus-menerus.
- [x] Terbaca di layar HP: grafik mengisi ruang yang sudah ada tanpa
      menggeser satu pun kartu.
