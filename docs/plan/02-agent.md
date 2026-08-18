# Fase 2 — `issboard-agent`: riwayat + notifikasi

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
