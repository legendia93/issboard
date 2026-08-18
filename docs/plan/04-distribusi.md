# Fase 4 — Test, paket, dan distro lain

Fase yang mengubah "jalan di mesinku" jadi "bisa dipasang orang lain".

## Test

Belum ada test sama sekali. Yang paling berharga duluan — **parser**, karena
di situlah data dunia nyata paling sering mengejutkan:

- `zpool list -H -p` & `zpool status` (termasuk membedakan **mirror vs stripe**,
  dan baris `scan:` dalam berbagai bentuk: belum pernah, sedang jalan, selesai)
- pembacaan cache SMART, termasuk **deteksi basi**
- JSON Docker `/containers/json` (network kosong, port `0.0.0.0`)
- `internal/health` — tiap aturan vonis, memakai data demo sebagai fixture

Mode demo dari fase 1 sekalian jadi bahan fixture-nya.

## Paket

| Jalur | Status yang dijanjikan |
|---|---|
| `.deb` + `install.sh` | **direkomendasikan**, jalur utama |
| tarball generik | didukung |
| Docker | didukung, dengan peringatan jujur |
| Docker **+ ZFS** | **tidak** didukung penuh — lihat di bawah |

## Docker: janjinya harus jujur

Dashboard kesehatan seharusnya tetap hidup justru saat Docker rusak — itu
alasan `design.md` §3.1 memilih binary di host. Container tetap disediakan
karena itu jalur coba-coba yang paling mudah, tapi README harus menyebut:

1. **Kalau Docker mati, dashboard ikut mati.** Ini trade-off, bukan bug.
2. **Binary `zfs`/`zpool` di dalam image harus cocok versinya dengan modul
   kernel ZFS di host.** Host zfs 2.1 + image zfs 2.2 = error, dan versi ZFS
   penonton tidak bisa dikontrol. Ini sumber issue terbesar kalau dijanjikan.
3. Butuh `/dev/zfs`, akses `/dev/sd*` untuk SMART, dan longgar di SELinux.

## Distro lain (Debian tetap utama)

- **SELinux** (Fedora/RHEL) — `ProtectSystem=strict` + baca socket container +
  tulis cache. Butuh label/policy, bukan sekadar catatan di README.
- **Podman** — path socket `/run/podman/podman.sock`, API kompatibel.
  Kodenya disiapkan, ditandai **belum diuji**; belum ada mesin untuk mengecek.
- **Non-systemd** (Alpine/Void) — fallback `net.Listen` sudah ada di `main.go`.
  ⚠️ **`idle_timeout` harus otomatis nonaktif** kalau tidak ada socket
  activation, kalau tidak prosesnya keluar dan tak ada yang menyalakan lagi.
  Ini bug yang menunggu terjadi.
- Path unit systemd `/lib` vs `/usr/lib`.
- ZFS tidak universal (DKMS di Fedora/RHEL, archzfs di Arch) — sudah aman
  karena kegagalan per-bagian, tapi README jangan berasumsi.
