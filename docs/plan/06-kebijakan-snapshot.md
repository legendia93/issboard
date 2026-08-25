# Fase 6 — Kebijakan snapshot vs dataset nyata

**Status: ✅ selesai dan terpasang** (25 Agustus 2026).

Fase pertama sesudah pemasangan, dan yang pertama dikerjakan dengan mesin
sungguhan sudah tersedia untuk diuji **selagi** aturannya ditulis — bukan
sesudahnya. Itu mengubah cara kerjanya, dan hasilnya terlihat di bagian
"Yang diukur" di bawah.

## Kenapa ini yang duluan

Dari empat item yang menunggu di [`00-index.md`](00-index.md), ini yang paling
mahal kalau ditunda: ia melindungi **data**, bukan kenyamanan. Yang lain —
panel versi app, fase arsip — menambah yang bisa dilihat; yang ini menutup
lubang yang tidak akan pernah muncul sendiri di layar mana pun.

Dua pertanyaan yang dijawabnya:

1. **Dataset mana yang tidak tercakup aturan apa pun.** Dataset baru tidak ikut
   sendiri ke `sanoid.conf`. Yang berbahaya bukan kelalaiannya, melainkan
   **tidak ada satu pun pesan saat itu terjadi** — dataset tak terlindungi
   terlihat persis sama dengan yang terlindungi, sampai hari orang
   membutuhkan snapshot-nya.
2. **Dataset yang tercakup tapi snapshot-nya berhenti.** Timer bisa saja
   `active` sementara tidak menghasilkan apa pun.

## Yang dikerjakan

| Bagian | Isi |
|---|---|
| `internal/collector/snappolicy.go` | Parser `sanoid.conf`: bagian, template, `recursive`, `process_children_only`, pencocokan paling spesifik |
| `internal/collector/zfs.go` | `usedbydataset` + waktu snapshot **terbaru** per dataset, dalam satu panggilan |
| `internal/health/health.go` | Tiga aturan: tidak tercakup, belum pernah ter-snapshot, snapshot terlambat/berhenti |
| `internal/config` | `snapshot_policy`, `snapshot_exempt` |
| `web/app.js` | Panel dataset: pil kebijakan, umur snapshot terakhir, warna mengikuti temuan |

issboard hanya **membaca** berkasnya. Ia tidak memanggil sanoid, tidak membuat
snapshot, dan tidak peduli apakah sanoid benar-benar terpasang. Yang
dibandingkan adalah *yang tertulis* lawan *yang ada di ZFS* — dan itu sengaja,
karena kedua sumber itu bisa berbeda tanpa satu pun dari keduanya error.

## Keputusan yang menentukan aturannya berguna atau berisik

### Ambang "terlambat" diturunkan dari retensi, bukan angka tetap

Ini yang paling mudah salah, dan mesin nyata membuktikannya sebelum satu baris
aturan pun dipasang di sana.

Host uji punya enam dataset dengan `hourly = 0` — memang **harian**. Snapshot
terakhirnya 9,7 jam lalu, dan itu **sepenuhnya normal**. Ambang tetap "6 jam"
yang terdengar masuk akal akan menandai keenamnya sekaligus, semuanya untuk
dataset yang bekerja persis seperti seharusnya.

Jadi yang menentukan bukan selera penulis aturan, tapi **apa yang diminta
berkasnya sendiri**: `hourly > 0` → tiap jam, kalau tidak `daily > 0` → tiap
hari, dan seterusnya. Kelonggarannya 2× jarak itu plus satu jam; di atas 8×
statusnya naik dari "terlambat" jadi "berhenti".

### Ukuran memisahkan wadah dari data

`used` sebuah dataset induk memuat seluruh keturunannya, jadi `kolam/prod`
terlihat 1,7 GB padahal isinya sendiri 122 KB. Aturan cakupan memakai
**`usedbydataset`**, dan hanya menyala di atas 1 GiB. Tanpa itu, tiap dataset
wadah — yang memang tidak perlu tercakup, karena kebijakannya menargetkan
anak-anaknya — jadi satu temuan.

### Berkas yang tidak ada mematikan seluruh aturan

Mesin tanpa `sanoid.conf` bukan mesin yang seluruh datasetnya bermasalah. Kalau
ketiadaan berkas diperlakukan sebagai "tidak ada yang tercakup", host semacam
itu akan menyalakan satu temuan per dataset di menit pertama.

### Ada jalan mematikan satu temuan dengan sadar

`snapshot_exempt` untuk scratch, rekaman CCTV, apa pun yang dilindungi cara
lain. Aturan yang pasti punya kekecualian sah tapi tidak menyediakan tempat
menuliskannya akan dimatikan seluruhnya, bukan sebagian.

### Warna alarm hanya untuk hal yang aturannya sendiri anggap temuan

Versi pertama panel memberi pil oranye "tanpa kebijakan" ke **setiap** dataset
yang tidak tercakup — termasuk dataset wadah 114 KB yang aturannya sendiri
sudah putuskan bukan masalah. Layar dan daftar temuan jadi tidak sepakat, dan
yang dipercaya orang adalah yang paling mencolok. Sekarang warnanya dihitung
dari temuan yang sama dengan yang dipakai kartu lain, bukan aturan kedua yang
ditulis ulang di JavaScript.

## Satu cacat yang ditemukan saat pengerjaan

**`omitempty` tidak berlaku untuk struct.** `LastSnapshot` bertipe `time.Time`
yang nol tetap terkirim sebagai `"0001-01-01T00:00:00Z"`, dan halaman yang cuma
memeriksa "ada isinya atau tidak" membacanya sebagai tanggal sungguhan — lalu
melaporkan umur snapshot dalam **ratusan ribu hari**.

Ini keluarga yang sama dengan suhu 0 dan `passed` kosong di
[`05-pemasangan.md`](05-pemasangan.md): **ketiadaan menyamar jadi nilai.** Kali
ini tertangkap sebelum sampai ke mesin nyata, karena mode demo sengaja memuat
tiga dataset tanpa snapshot. Diperbaiki dengan `*time.Time` — tipe yang tidak
bisa berbohong soal ketiadaan — dan dikunci test.

## Yang diukur di host sungguhan

Aturannya dijalankan lawan `sanoid.conf` dan seluruh 21 dataset host nyata
**sebelum** dipasang di sana, dengan datanya disalin apa adanya:

| | |
|---|---|
| Dataset diperiksa | 21 |
| Pencocokan cakupan salah | **0** |
| Temuan palsu | **0** |
| Temuan sungguhan | **1** |

Temuan itu: satu dataset berisi **84 GiB** yang tidak masuk bagian mana pun di
`sanoid.conf` — snapshot terakhirnya 11 hari lalu, dan hanya ada dua. Tidak ada
alat lain di mesin itu yang pernah menyebutkannya.

Enam dataset harian yang akan ditandai salah oleh ambang tetap: **tidak satu
pun** jadi temuan.

### Lalu dipasang, dan hasilnya sama persis

Terpasang di host sore itu juga (25 Agt, 17:26). Prediksi di atas dibuat lawan
salinan data; ini hasil lawan mesinnya sendiri:

| | |
|---|---|
| `snap_policy.present` | `true` — berkasnya terbaca dari dalam `ProtectSystem=strict`, tanpa penyesuaian unit |
| Temuan snapshot | **1**, `snap.uncovered.tank-main/media` |
| Temuan palsu | **0** — enam dataset harian berumur 10,5 jam, tidak satu pun ditandai |
| Dataset wadah (98 KB, 122 KB, 114 KB) | diam, sesuai ambang |
| Dashboard vs agent | sepakat; keduanya memakai `internal/health` yang sama |

Dan pada 17:33 temuan itu **terkirim ke Telegram** sebagai "1 baru" — aturan
yang ditulis siang hari sampai ke HP pemiliknya sore itu juga, lewat jalur yang
tidak menyalakan daemon apa pun.

Yang perlu dicatat: perintah verifikasi yang pertama diberikan **salah**, bukan
kodenya. API mencetak JSON ber-indent, jadi keluarannya `"key": "snap…"` dengan
spasi sesudah titik dua, sementara pola grep-nya ditulis tanpa spasi — ia tidak
akan pernah cocok, di versi mana pun. Sesaat itu terlihat seperti dashboard dan
agent tidak sepakat. **Alat verifikasi yang salah menghasilkan gejala yang
bentuknya persis sama dengan bug**, dan yang membedakannya cuma memeriksa
alatnya lebih dulu sebelum memeriksa yang diukur.

## Yang masih tersisa

- Format kebijakan selain sanoid (`zfs-auto-snapshot`, `zrepl`) belum dibaca.
- `syncoid` / replikasi ke luar mesin belum diperiksa sama sekali: snapshot
  yang ada tapi tidak pernah pergi ke mana-mana tetap satu disk dari hilang.
- **`tank-main/media` menunggu keputusan**, bukan menunggu kode: masuk
  `sanoid.conf` atau `snapshot_exempt`. Temuan yang tidak diputuskan akan jadi
  kebisingan yang dilewati mata — persis hal yang aturan ini ada untuk cegah.
