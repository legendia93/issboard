# Fase 8 — Panel ZFS di dashboard

**Status: ⬜ belum dimulai, dan TERKUNCI oleh
[`07-autentikasi.md`](07-autentikasi.md).** Ditulis 25 Agustus 2026 dari arahan
pemilik server: side panel di frontend supaya ZFS bisa diatur dari dashboard —
buat zpool, destroy, buat dataset, snapshot, sanoid, status scrub, quick test.

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

- [ ] Fase 7 selesai lebih dulu. Ini bukan urutan yang disarankan, ini syarat.
- [ ] Bentuk API bermutasi: `POST /api/v1/...`, bukan GET. GET yang mengubah
      keadaan akan dijalankan oleh prefetch browser dan crawler.
- [ ] Daftar-putih dari sistem, memakai kembali daftar dataset fase 6.
- [ ] sudoers per-perintah, ditulis eksplisit, tanpa wildcard.
- [ ] Log audit tiap mutasi: siapa, apa, target, hasil.
- [ ] Side panel di FE. Tetap Nothing OS, tetap tanpa pustaka luar, tetap CSP
      `default-src 'self'`.
- [ ] Aksi kelas A dulu: snapshot manual, mulai/hentikan scrub.
- [ ] Status scrub yang sedang berjalan (persen + ETA) — `zpool status` sudah
      diurai di `internal/collector/zfs.go`, tinggal ditampilkan.
- [ ] Test: setiap endpoint bermutasi tanpa sesi → 401; nama di luar
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
