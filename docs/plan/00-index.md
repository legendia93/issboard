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
| 2 | [`02-agent.md`](02-agent.md) — riwayat + notifikasi (ntfy, Telegram) | 🚧 berikutnya |
| 3 | [`03-sparkline.md`](03-sparkline.md) — grafik mengisi ruang fase 1 | ⬜ belum |
| 4 | [`04-distribusi.md`](04-distribusi.md) — test, `.deb`, Docker, multi-distro | ⬜ belum |

## Di luar fase (belum dijadwalkan)

Dari `design.md` bagian 9, masih menunggu:

- Perbandingan konfigurasi snapshot (mis. `sanoid.conf`) dengan dataset nyata
- Panel versi app + deteksi drift antara config dan container yang jalan
- Fase arsip & unduhan backup
- Autentikasi — **wajib ada sebelum endpoint bermutasi pertama**, bukan sesudah
