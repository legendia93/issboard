# Fase 7 — Autentikasi

**Status: ✅ selesai di kode (29 September 2026), belum dipasang di host
sungguhan.** Ditulis 25 Agustus 2026, saat fase 6 baru selesai dan panel ZFS
([`08-panel-zfs.md`](08-panel-zfs.md)) sudah diputuskan akan dikerjakan.

## Kenapa ini yang duluan, bukan panelnya

Aturannya sudah ditulis di [`../design.md`](../design.md) §8 jauh sebelum ada
yang membutuhkannya:

> Begitu ada **satu** endpoint yang bermutasi, autentikasi **wajib lebih dulu**,
> bukan menyusul.

Dan §8 juga menyimpan kalimat yang akan **kedaluwarsa di menit pertama** panel
ZFS hidup:

> Keanggotaan grup `docker` setara root di kebanyakan sistem. Grup itu
> diberikan hanya untuk membaca socket; pembatas sebenarnya di v1 adalah
> **tidak adanya jalur mutasi sama sekali.**

Jadi selama ini yang menjaga issboard bukan kekuatan pembatasnya, melainkan
ketiadaan jalur. Menambah panel lebih dulu berarti mencabut satu-satunya
pembatas yang benar-benar bekerja, lalu berjanji akan menggantinya nanti.

Tailnet **tidak** menutup lubang ini. Ia mengautentikasi **perangkat** lewat
kunci WireGuard; panel yang bisa `zfs destroy` butuh sesuatu yang
mengautentikasi **orang** — dan §8.1 sudah menyatakan itu eksplisit. HP yang
tertinggal di meja sudah ada di tailnet.

## Keputusan yang harus diambil lebih dulu

**Autentikasi sendiri, atau pintu berautentikasi di depannya?** Ini menentukan
seluruh sisa fase, dan §8.1 sudah condong ke yang kedua:

> Keduanya membiarkan issboard di loopback dan menaruh pintu berautentikasi di
> depannya — secara struktur ini yang paling rapi, karena pembatasnya tidak
> lagi bergantung pada issboard yang mengikat alamat dengan benar.

| | Sendiri (di dalam issboard) | Proxy (Cloudflare Access / `tailscale serve`) |
|---|---|---|
| Kode yang harus benar | sesi, hash, CSRF, rate limit — semuanya milik kita | nyaris nol |
| Kalau salah | seluruh mesin | pintunya yang jatuh, bukan kodenya kita |
| Bekerja tanpa jaringan luar | ✅ | ❌ Access butuh Cloudflare |
| Identitas **orang** | dari berkas config | dari IdP yang sudah ada |
| Audit "siapa" | kita yang catat | header identitas dari proxy |

Yang perlu diingat: memilih proxy **bukan** berarti issboard boleh polos.
Endpoint bermutasi yang percaya begitu saja pada header identitas akan terbuka
lebar bagi siapa pun yang bisa menjangkau loopback — termasuk container lain di
host yang sama. Minimal tetap perlu: issboard **hanya** mendengarkan loopback,
dan menolak permintaan bermutasi yang datang tanpa header yang diharapkan.

## Konsekuensi yang khas proyek ini

🔴 **Sesi tidak boleh disimpan di memori.** issboard memakai socket activation
dan keluar sendiri setelah idle (`idle_timeout`, bawaan 5 menit). Session store
di memori berarti **setiap orang ter-logout tiap kali halaman ditinggal lima
menit** — dan yang lebih buruk, itu akan terlihat seperti bug acak, bukan
seperti akibat desain.

Jadi sesinya harus **stateless dan bertanda tangan** (HMAC di cookie), atau
disimpan di berkas seperti `alert-state.json`. Cookie bertanda tangan lebih
cocok: tidak ada state, tidak ada berkas baru, dan sifat "RAM idle 0 MB" —
alasan utama seluruh arsitektur ini — tetap utuh.

Go 1.26 sudah punya `crypto/pbkdf2` dan `crypto/hmac` di pustaka standar, jadi
janji **nol dependensi di luar pustaka standar** (§3.1) tidak perlu dilanggar
untuk ini.

## Keputusan: sendiri, di dalam issboard (29 September 2026)

Proxy kalah bukan karena lebih buruk secara struktur — tabel di atas masih
benar — tapi karena keadaan nyata mesinnya:

1. **Belum ada proxy berautentikasi di depan issboard.** Aksesnya tailnet
   langsung. Cloudflare Access belum terpasang, dan `tailscale serve` hanya
   memberi header identitas yang tetap mengautentikasi *akun tailnet*, bukan
   orang di depan HP yang tertinggal.
2. **Harus tetap bekerja saat internet mati** — justru saat dashboard paling
   dibutuhkan. Access butuh Cloudflare.
3. Kode yang "harus benar" ternyata kecil: ±300 baris di `internal/auth`, nol
   dependensi, semuanya dikunci test.

Jalur proxy tetap terbuka nanti: issboard yang punya autentikasi sendiri tidak
menjadi lebih lemah dengan ditaruh di belakang proxy.

## Yang dikerjakan

- [x] Putuskan lebih dulu: sendiri atau proxy — **sendiri**, lihat di atas.
- [x] Kata sandi di-hash **PBKDF2-SHA256, 600 ribu iterasi**, di
      `/etc/issboard/auth` (0640 `root:issboard`) — bukan di config yang boleh
      dibaca siapa saja. Ditulis `sudo issboard -set-password`.
- [x] Cookie sesi ber-HMAC, **stateless**, berlaku 12 jam. `HttpOnly` +
      `SameSite=Strict`. `Secure` **hanya kalau sambungannya HTTPS**: tailnet
      melayani HTTP polos, dan browser membuang cookie `Secure` di HTTP — login
      akan "berhasil" lalu langsung lupa.
- [x] Mengganti kata sandi selalu mengganti rahasia HMAC → **semua sesi lama
      ter-logout**, termasuk HP yang tertinggal dalam keadaan masuk.
- [x] **CSRF**: token diturunkan dari sesi (HMAC nonce), dikirim di header
      `X-CSRF-Token`, plus penolakan `Origin`/`Sec-Fetch-Site` asing.
- [x] **Rate limit** login: 5 gagal per alamat per 15 menit, lalu 429 —
      termasuk untuk kata sandi yang benar. Di memori, dan itu cukup: penyerang
      yang terus menebak justru menahan proses tetap hidup.
- [x] **Log audit** ke journald (stderr): login berhasil/gagal/ditahan, dan tiap
      aksi dicatat **sebelum** dijalankan dan sesudahnya, dengan user dan IP.
- [x] Bagian read-only **tetap terbuka** — keputusan sadar: tidak ada yang
      berubah untuk yang cuma melihat, dan agent tidak butuh sesi.
- [x] Test: tiap endpoint bermutasi tanpa sesi → **401** (mode demo dan mode
      tanpa berkas kredensial), tanpa CSRF → 403, origin asing → 403, GET ke
      jalur aksi tidak menjalankan apa pun.

## Belum terbukti

- Belum pernah dipasang di host sungguhan. Yang paling mungkin mengejutkan:
  izin `/etc/issboard/auth` saat grup `issboard` dibuat dengan cara lain dari
  `adduser --group`.

## Yang TIDAK dikerjakan di fase ini

- Multi-user, peran, dan izin per-aksi. Satu operator sudah cukup untuk mesin
  ini; menambah model peran sebelum ada orang keduanya adalah kerumitan yang
  belum dibayar siapa pun.
- OAuth/OIDC di dalam issboard. Kalau butuh identitas dari IdP, itu justru
  alasan memilih jalur proxy.
