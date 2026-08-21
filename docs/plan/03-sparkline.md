# Fase 3 — Sparkline

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

- Endpoint `GET /api/v1/history` — membaca file, tidak mengumpulkan apa pun.
- SVG digambar tangan di `app.js`. **Tidak ada pustaka grafik**: satu `<path>`
  dari deret angka sudah cukup, dan itu menjaga janji nol dependensi.
- Warna garis pakai `--p-*-line` sesuai pastel yang dipegang pool/disk itu.
- Saat halaman terbuka, poll 15 detik **menyambung titik live ke ekor grafik**
  di memori browser — terasa hidup tanpa daemon di server.
- Riwayat yang bolong (agent mati / mesin baru menyala) digambar **putus**,
  bukan disambung lurus. Garis lurus palsu menyembunyikan justru hal yang
  ingin diketahui: ada periode yang tidak terpantau.

## Metrik yang dapat sparkline

Load, memori terpakai, ukuran ARC, kapasitas tiap pool, suhu tiap disk.
Bukan jumlah container — itu angka yang melompat, bukan tren.
