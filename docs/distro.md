# Menjalankan issboard di luar Debian

Debian tetap jalur utama — di situlah issboard dipakai sungguhan dan diuji.
Halaman ini mengumpulkan yang diketahui tentang sisanya, **termasuk yang belum
pernah dicoba**. Yang belum diuji ditandai jelas: dokumentasi yang mengaku
tahu padahal tidak adalah cara tercepat membuang waktu orang.

## Ringkasan

| Jalur | Status |
|---|---|
| Debian/Ubuntu + systemd, paket `.deb` | **dianjurkan**, diuji |
| Distro ber-systemd lain, `install.sh` / tarball | didukung, diuji sebagian |
| Non-systemd (Alpine, Void, dalam container) | jalan, tanpa socket activation |
| Fedora/RHEL dengan SELinux *enforcing* | butuh langkah tambahan, **belum diuji** |
| Podman | kode siap, **belum diuji** |
| Docker + ZFS | **tidak** didukung penuh — lihat di bawah |

## Non-systemd (Alpine, Void, container)

issboard tetap jalan: kalau systemd tidak menyerahkan socket, ia membuka
sendiri alamat di `listen:`.

⚠️ **`idle_timeout` otomatis nonaktif dalam keadaan itu**, dan itu disengaja.
Idle-exit hanya masuk akal kalau ada systemd yang menyalakan prosesnya lagi
lewat socket. Tanpa itu, keluar saat idle berarti dashboard mati diam-diam dan
baru ketahuan saat dibuka. issboard mencatatnya di log saat start, jadi tidak
perlu ada yang ingat menulis `idle_timeout: 0` sendiri.

Yang hilang tanpa systemd: socket activation (prosesnya jalan terus, bukan
0 MB saat menganggur) dan timer. Dua timer itu perlu diganti cron:

```cron
* * * * *      issboard  /usr/local/bin/issboard-agent -config /etc/issboard.yaml
0 */6 * * *    root      /usr/local/libexec/issboard-smart-collect
```

Di container, alamatnya diatur lewat `ISSBOARD_LISTEN` — di dalam namespace
sendiri, `127.0.0.1` membuat prosesnya tak terjangkau dari luar.

## SELinux (Fedora, RHEL, Rocky) — belum diuji

Unit issboard memakai `ProtectSystem=strict` dan `SystemCallFilter`, dan itu
bagian yang mudah. Yang belum dibuktikan adalah SELinux *enforcing*, karena
issboard melakukan tiga hal yang biasanya butuh label sendiri:

1. membaca socket container (`container_var_run_t`),
2. menulis cache SMART sebagai root lalu **membacanya sebagai user lain**,
3. menulis `/var/lib/issboard` dari unit bertimer.

Cara jujur mencari tahu, bukan menebak:

```bash
sudo ausearch -m avc -ts recent | audit2allow -m issboard
```

Jangan menyalakan `setenforce 0` sebagai solusi. Kalau ada yang berhasil
membuat policy-nya, laporannya sangat dihargai.

## Podman — belum diuji

API Podman kompatibel dengan Docker, jadi kodenya tinggal diarahkan:

```yaml
docker_socket: /run/podman/podman.sock
```

Untuk socket rootless, jalurnya `/run/user/<uid>/podman/podman.sock` dan
issboard harus jalan sebagai user itu. **Belum ada mesin untuk memverifikasi
keduanya.**

## Docker + ZFS: kenapa tidak dijanjikan

Container-nya sendiri didukung sebagai jalur coba-coba, tapi ZFS dari dalam
container **tidak**, dan alasannya bukan kemalasan:

**Biner `zfs`/`zpool` di dalam image harus cocok versinya dengan modul kernel
ZFS di host.** Host zfs 2.1 dengan image zfs 2.2 menghasilkan error yang
membingungkan, dan versi ZFS di mesin orang lain tidak bisa dikontrol. Karena
itu image resmi sengaja **tidak memasang zfsutils sama sekali** — bagian ZFS
akan kosong dan melaporkan error, dan itu lebih jujur daripada gagal setengah
jalan dengan pesan yang menyesatkan.

Selain itu, dari dalam container ZFS butuh `/dev/zfs`, akses `/dev/sd*` untuk
SMART, dan kelonggaran SELinux — masing-masing meniadakan sebagian isolasi
yang jadi alasan memakai container.

Dan yang paling penting: **kalau Docker mati, dashboard-nya ikut mati.**
issboard ada justru untuk tetap hidup saat container bermasalah. Itu trade-off
yang sadar, bukan bug.

## Jalur unit systemd

| Distro | Folder |
|---|---|
| Debian lama | `/lib/systemd/system` |
| Debian 12+, Fedora, Arch (merged-usr) | `/usr/lib/systemd/system` |
| Dipasang admin (semua distro) | `/etc/systemd/system` |

Paket `.deb` memasang ke `/usr/lib/systemd/system`; di sistem merged-usr
`/lib` cuma symlink ke situ, jadi keduanya menunjuk tempat yang sama.
`install.sh` memakai `/etc/systemd/system` — memang di situlah unit yang
dipasang admin seharusnya tinggal, dan ia menang atas unit bawaan paket.

Unit di repo menunjuk `/usr/local/...`; `packaging/build-deb.sh` menulis
ulang jalurnya jadi `/usr/...` saat membangun paket. Itu satu-satunya tempat
perbedaan jalur itu boleh ada.

## ZFS tidak universal

- Debian/Ubuntu: `zfsutils-linux`
- Fedora/RHEL: repo OpenZFS + DKMS
- Arch: `archzfs`
- Alpine: `zfs`

issboard **tidak** menjadikan ZFS syarat. Host tanpa ZFS tetap dilayani:
bagian pool dan dataset melaporkan error per-bagian, sisanya jalan normal.
Itu bukan kasus tepi — itu cara proyek ini dikembangkan sehari-hari.
