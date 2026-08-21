# Fase 2 — `issboard-agent`: riwayat + notifikasi

**Status: ✅ selesai** (21 Agustus 2026). Yang belum diverifikasi dicatat di
bagian [Yang belum diuji](#yang-belum-diuji) di bawah — jangan dianggap teruji
cuma karena kodenya ada.

## 🔴 Aturan arsitektur yang tidak boleh dilanggar

**Jangan membangun ini dengan mengubah issboard jadi daemon.**

issboard memakai socket activation: kalau halamannya tidak dibuka, prosesnya
tidak hidup. Jadi issboard **secara desain tidak bisa** jadi sumber alert — itu
konsekuensi yang diterima sadar (lihat `design.md` §3.2), bukan kekurangan yang
perlu ditambal. Pengirim notifikasi adalah **unit systemd terpisah** yang
kecil dan jalan berkala. Boleh berbagi kode `internal/`, tapi **bukan proses
yang sama** dan bukan `Restart=always` di atas `issboard.service`.

## Kenapa timer, bukan daemon — angkanya

| | RAM idle | CPU idle | Riwayat | Bisa alert |
|---|---|---|---|---|
| issboard jadi daemon | ~15–25 MB terus-menerus | poll tiap 15 dtk selamanya | ✅ di memori | ✅ |
| issboard sekarang saja | **0 MB** | **0** | ❌ | ❌ |
| **issboard + agent bertimer** | **0 MB** | 1 spawn/menit (~5 ms) | ✅ dari file | ✅ |

Riwayat di file juga **selamat dari reboot** — sesuatu yang daemon in-memory
justru tidak punya.

## Bentuk

```
issboard-agent  (systemd timer, tiap 60 detik)
  ├─ pakai ulang internal/collector
  ├─ tulis  /var/lib/issboard/history.json   ← ring buffer, atomik
  ├─ evaluasi internal/health  → daftar temuan
  ├─ bandingkan dengan state dedup, kirim yang baru
  └─ keluar. RSS kembali 0.

issboard (tidak berubah sama sekali)
  └─ BACA history.json — persis pola cache SMART
```

## Ring buffer

Dua lapis supaya file tetap kecil (< 20 KB) tapi jangkauannya panjang:

| Lapis | Jumlah titik | Jarak | Cakupan |
|---|---|---|---|
| halus | 60 | 1 menit | 1 jam terakhir |
| kasar | 48 | 30 menit | 24 jam terakhir |

Ditulis atomik (tulis ke `.tmp` lalu `rename`), sama seperti cache SMART.

## Notifikasi

- Kanal: **ntfy** dan **Telegram**, keduanya opsi (boleh aktif dua-duanya,
  boleh tidak sama sekali). Keduanya cuma HTTP POST — nol dependensi terjaga.
- **De-duplikasi wajib.** Disk dengan reallocated sector akan memicu temuan yang
  sama tiap siklus selamanya; tanpa state "sudah dikabari", alertnya jadi
  berisik dan berisik = diabaikan. Simpan state di
  `/var/lib/issboard/alert-state.json`, kirim ulang hanya kalau tingkatnya naik
  atau setelah jeda diam yang panjang.
- Token/kredensial **tidak boleh masuk repo**. Lewat file config di `/etc` yang
  permisinya ketat, atau `EnvironmentFile` systemd.

## Yang dikerjakan

- [x] `cmd/issboard-agent/` — binary kedua, `Type=oneshot`, hidup beberapa
      milidetik lalu keluar. issboard sendiri **tidak diubah sama sekali**.
- [x] `internal/history/` — ring buffer dua lapis, ditulis atomik, memakai
      ulang `internal/collector`. Cuplikan `/proc/stat` ikut disimpan, jadi
      CPU% yang tercatat adalah rata-rata satu menit penuh.
- [x] `internal/notify/` — ntfy & Telegram, keduanya cuma HTTP POST. Nol
      dependensi tetap utuh.
- [x] `internal/alert/` — ingatan "sudah dikabari" + penyusun pesan.
- [x] `systemd/issboard-agent.{service,timer}`, `StateDirectory=issboard`,
      `EnvironmentFile=-/etc/issboard/agent.env`.
- [x] `issboard-agent.env.example` + kunci baru di `issboard.example.yaml`.
- [x] `-demo` dan `-dry-run` untuk mencoba tanpa mengirim/menulis apa pun.

## Yang ditemukan saat mengerjakan

Empat hal yang tidak ada di rencana awal, semuanya ketahuan karena dicoba,
bukan karena dipikirkan:

1. **Kabar "pulih" bisa hilang selamanya.** Kiriman yang gagal memang tidak
   ditandai terkirim — tapi pencatat kehadiran tetap menghapus temuan yang
   sudah lenyap, jadi kabar pulihnya tidak pernah dicoba lagi. Sekarang entri
   ditahan sampai kabar itu benar-benar sampai, dengan batas 7 hari supaya
   berkasnya tidak tumbuh selamanya kalau memang tidak ada kanal.
2. **"Pulih" palsu saat kita yang buta.** `zpool` yang gagal dipanggil
   membuat seluruh temuan pool lenyap dari daftar, dan itu terbaca persis
   seperti pool yang sudah beres. Siklus yang `errors[]`-nya terisi sekarang
   tidak pernah melaporkan pulih.
3. **Pesan siklus pertama menggandakan dirinya sendiri.** Tiap baris berakhir
   dengan "(sejak kurang dari sejam lalu)" — benar, tapi nol gunanya. Umur
   hanya disebut kalau temuannya sudah berumur minimal sejam.
4. **Pesan bisa terlalu panjang untuk dikirim.** 14 temuan mode demo sudah
   ~2,5 KB, dan ntfy maupun Telegram menolak badan pesan yang kepanjangan.
   Dipotong di ~3,5 KB dengan penghitung "N temuan lagi tidak dimuat".

Satu hal kecil lagi: temuan yang sudah pulih tetap membawa tingkat lamanya,
jadi pesannya sempat berbunyi `[KRITIS] Pool hampir penuh` **di bawah judul
PULIH** — persis kebalikan dari kabar yang sedang disampaikan.

## Selesai kalau

- [x] `go build ./...` & `go vet ./...` bersih, tetap nol dependensi.
- [x] Dua siklus berturut-turut dengan temuan yang sama → siklus kedua **sepi**.
- [x] Temuan hilang saat semua collector sehat → kirim **satu** kabar pulih.
- [x] Temuan hilang karena collector error → **tidak** ada kabar pulih, dan
      ingatannya tetap utuh.
- [x] Kanal mati saat siklus pulih → keluar dengan kode 1, ingatan ditahan,
      kabar pulihnya menyusul saat kanalnya hidup lagi.
- [x] RAM idle tetap 0 MB: tidak ada proses yang menyala terus.

## Yang belum diuji

Jujur soal ini, karena mudah sekali dikira sudah beres:

- **Unit systemd-nya belum pernah dijalankan systemd sungguhan.** Pengerasan
  (`ProtectSystem=strict`, `SystemCallFilter`, `StateDirectory`) baru ditulis,
  belum dibuktikan tidak memblokir `zpool` atau socket container.
- **Telegram belum pernah diuji ke API aslinya.** Yang teruji jalur ntfy,
  lewat server tiruan lokal — header, token, prioritas, dan tag benar.
- **Belum jalan semalaman.** Pengingat `alert_repeat` 24 jam belum pernah
  benar-benar terpicu di mesin nyata, baru diuji lewat jalur kodenya.
  (Lapis kasar 30 menit sudah dibuktikan terpisah: 91 titik berjarak 1 menit
  menghasilkan 4 titik kasar berjarak tepat 1800 detik, isinya rata-rata
  jendela — dan 5000 siklus berhenti di 16,8 KB, masih di bawah janji 20 KB.)

## Temuan yang wajib dikirim

Aturannya sudah ditulis di `internal/health` pada fase 1 — agent memakai ulang,
tidak menulis ulang. Yang tidak boleh luput:

| Pemicu | Kenapa |
|---|---|
| SMART memburuk (reallocated/pending naik, `PASSED` gagal) | disk di pool **stripe** tidak punya redundansi sama sekali |
| Scrub menemukan error | hasil scrub tidak ada yang membaca kalau tidak dikirim |
| Container mati, atau **`Up` tapi tanpa network** | jebakan yang jadi akar insiden monitoring buta berjam-jam |
| Cache SMART basi | timer pengumpulnya mati adalah temuan tersendiri |
| Pool tidak `ONLINE`, atau error read/write/cksum > 0 | |
| Pool belum pernah di-scrub / scrub sudah lama | mirror tanpa scrub cuma redundansi di atas kertas |

Jangan kirim alert untuk hal yang berubah tiap menit. Semua di atas adalah
kondisi yang **bertahan** — cocok untuk pengecekan berkala, bukan streaming.
