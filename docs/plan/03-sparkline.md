# Fase 3 — Sparkline

Mengisi ruang berukuran tetap yang sudah disiapkan di fase 1, memakai
`history.json` yang ditulis agent di fase 2. **Tidak ada fase 3 tanpa fase 2.**

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
