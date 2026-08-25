# Fase 7 — Autentikasi

**Status: ⬜ belum dimulai.** Ditulis 25 Agustus 2026, saat fase 6 baru selesai
dan panel ZFS ([`08-panel-zfs.md`](08-panel-zfs.md)) sudah diputuskan akan
dikerjakan.

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

## Yang dikerjakan

- [ ] Putuskan lebih dulu: sendiri atau proxy. Sisanya bergantung pada ini.
- [ ] Kalau sendiri: kata sandi di-hash (bukan plaintext di config), cookie
      sesi ber-HMAC, `HttpOnly` + `Secure` + `SameSite=Strict`.
- [ ] **CSRF.** `SameSite=Strict` menutup sebagian besar, tapi endpoint
      bermutasi tetap wajib menolak permintaan yang tidak membawa token.
- [ ] **Rate limit** pada jalur login. Tanpa ini, kata sandi apa pun cuma soal
      waktu.
- [ ] **Log audit**: siapa, apa, kapan, berhasil atau tidak — untuk **setiap**
      permintaan bermutasi. Ditulis ke journald, bukan berkas baru.
- [ ] Bagian read-only tetap boleh terbuka atau ikut tertutup — putuskan sadar,
      jangan sampai kebetulan.
- [ ] Test yang mengunci hal yang paling mudah lolos: endpoint bermutasi tanpa
      sesi harus **401**, bukan 200 yang diam-diam bekerja.

## Yang TIDAK dikerjakan di fase ini

- Multi-user, peran, dan izin per-aksi. Satu operator sudah cukup untuk mesin
  ini; menambah model peran sebelum ada orang keduanya adalah kerumitan yang
  belum dibayar siapa pun.
- OAuth/OIDC di dalam issboard. Kalau butuh identitas dari IdP, itu justru
  alasan memilih jalur proxy.
