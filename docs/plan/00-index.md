# Rencana issboard — indeks

Daftar pekerjaan yang belum selesai, supaya tidak hilang antar sesi.
Alasan **kenapa** bentuknya begini ada di [`../design.md`](../design.md);
di sini hanya **apa** yang dikerjakan dan **urutannya**.

> Repo ini publik. Jangan pernah menulis hostname, alamat IP, nama pool, atau
> nama app milik mesin nyata di folder ini. Konteks nyata ada di
> `docs/konteks-lokal/` yang diabaikan `.gitignore`.

## Keputusan yang mendasari urutan ini

Ditetapkan 18 Agustus 2026:

1. **Nama tetap `issboard`.** Tidak jadi diganti meski repo akan publik.
2. **issboard TIDAK jadi daemon.** Riwayat untuk sparkline ditulis oleh
   **agent bertimer** ke file, lalu issboard **membacanya** — pola yang sama
   dengan cache SMART. Alasannya efisiensi: RAM idle tetap **0 MB**.
   Perbandingannya ada di [`02-agent.md`](02-agent.md).
3. **Tema Nothing OS**, di-port dari proyek `lookna`, ditambah aksen pastel.
   Aturannya: **sehat itu pastel, bermasalah itu pekat.**
4. **UI dikerjakan lebih dulu**, karena tidak bergantung apa pun dan justru
   memaksa bentuk API matang sebelum agent dibangun di atasnya.
5. **Podman didukung** lewat path socket yang bisa dikonfigurasi, ditandai
   **belum diuji** — belum ada mesin untuk memverifikasi.
6. Video/konten **bukan pemicu jadwal**. Dikerjakan saat app-nya sudah jadi.

## Fase

| Fase | Berkas | Status |
|---|---|---|
| 1 | [`01-ui-nothing-os.md`](01-ui-nothing-os.md) — tema, bento, mode demo | ✅ selesai |
| 2 | [`02-agent.md`](02-agent.md) — riwayat + notifikasi (ntfy, Telegram) | ✅ selesai |
| 3 | [`03-sparkline.md`](03-sparkline.md) — grafik mengisi ruang fase 1 | ✅ selesai |
| 4 | [`04-distribusi.md`](04-distribusi.md) — test, `.deb`, Docker, multi-distro | ✅ selesai |
| 5 | [`05-pemasangan.md`](05-pemasangan.md) — pasang di mesin sungguhan | ✅ selesai |
| 6 | [`06-kebijakan-snapshot.md`](06-kebijakan-snapshot.md) — cakupan & umur snapshot | ✅ selesai |

Keempat fase yang direncanakan sudah selesai, dan issboard **sudah berjalan di
host sungguhan** sejak 21 Agustus 2026.

Fase 6 mengerjakan item pertama dari daftar "di luar fase" di bawah, dan jadi
fase pertama yang aturannya diuji lawan data host sungguhan **selagi** ditulis,
bukan sesudahnya. Hasilnya di [`06-kebijakan-snapshot.md`](06-kebijakan-snapshot.md):
21 dataset diperiksa, **nol temuan palsu**, satu temuan sungguhan — 84 GiB yang
tidak tercakup aturan snapshot apa pun dan tidak pernah disebut alat lain mana
pun di mesin itu. Terpasang sore itu juga, dan hasil di mesinnya sendiri sama
persis dengan prediksinya.

Dengan itu, ketiga sisa fase 5 yang menunggu waktu sudah lunas: notifikasi
Telegram **benar-benar terkirim**, `alert_repeat` 24 jam **terpicu empat kali**,
dan riwayat 24 jam **terisi penuh tanpa lubang**. Yang tersisa di bawah
menunggu keputusan, bukan waktu.

Fase 5 tidak ada di rencana awal. Ia ditambahkan setelah kenyataan
membuktikannya perlu: pemasangan pertama menemukan **enam cacat** yang tidak
satu pun bisa muncul di mesin pengembangan — termasuk satu yang mematikan
dashboard sepenuhnya, dan satu alarm kritis palsu di menit pertama. Rinciannya
di [`05-pemasangan.md`](05-pemasangan.md); yang tersisa di bawah belum pernah
dijadwalkan, dan sebagiannya menunggu keputusan, bukan menunggu waktu.

## Di luar fase (belum dijadwalkan)

Dari `design.md` bagian 10, masih menunggu:

- ~~Perbandingan konfigurasi snapshot dengan dataset nyata~~ — selesai di fase 6
- Panel versi app + deteksi drift antara config dan container yang jalan
- Fase arsip & unduhan backup
- Autentikasi — **wajib ada sebelum endpoint bermutasi pertama**, bukan sesudah
- Replikasi: snapshot yang ada tapi tidak pernah pergi ke luar mesin tetap
  satu disk dari hilang (muncul dari fase 6)
