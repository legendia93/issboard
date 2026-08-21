#!/bin/sh
# Membangun paket .deb — jalur pemasangan yang dianjurkan.
#
# Tidak butuh dpkg-dev, debhelper, atau fpm: cukup dpkg-deb yang sudah ada di
# tiap sistem Debian. Paketnya sederhana karena isinya memang sederhana —
# dua binary statis, satu skrip, dan beberapa unit systemd.
#
#   ./packaging/build-deb.sh            → dist/issboard_<versi>_<arch>.deb
#   VERSION=1.2.3 ARCH=arm64 ./packaging/build-deb.sh
set -eu

cd "$(dirname "$0")/.."

command -v dpkg-deb >/dev/null 2>&1 || {
  printf 'GAGAL: dpkg-deb tidak ada. Di distro non-Debian pakai install.sh.\n' >&2
  exit 1
}

# Versi dari git kalau ada; kalau tidak, tanggal — supaya paket hasil build
# dari tarball tetap punya versi yang naik.
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || true)}"
[ -n "$VERSION" ] || VERSION="0.0.0+$(date -u +%Y%m%d)"
# dpkg menolak versi yang tidak diawali angka (mis. hash commit telanjang).
case "$VERSION" in [0-9]*) ;; *) VERSION="0.0.0+git$VERSION" ;; esac

ARCH="${ARCH:-$(dpkg --print-architecture)}"
case "$ARCH" in
  amd64) GOARCH=amd64 ;;
  arm64) GOARCH=arm64 ;;
  armhf) GOARCH=arm ;;
  i386)  GOARCH=386 ;;
  *)     GOARCH="$ARCH" ;;
esac

ROOT="dist/issboard_${VERSION}_${ARCH}"
rm -rf "$ROOT"
mkdir -p "$ROOT/DEBIAN" \
         "$ROOT/usr/bin" \
         "$ROOT/usr/libexec" \
         "$ROOT/usr/lib/systemd/system" \
         "$ROOT/usr/share/doc/issboard" \
         "$ROOT/etc/issboard"

printf '==> membangun binary (%s, statis)\n' "$GOARCH"
# CGO_ENABLED=0: binary benar-benar statis, jadi tidak ada dependensi runtime
# yang bisa ikut rusak saat sistemnya sendiri sedang bermasalah — alasan yang
# sama dengan memilih Go sejak awal.
CGO_ENABLED=0 GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$ROOT/usr/bin/issboard" .
CGO_ENABLED=0 GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$ROOT/usr/bin/issboard-agent" ./cmd/issboard-agent
# go build mengikuti umask, dan umask 002 menghasilkan 775 — bisa ditulis grup.
# Isi paket tidak boleh bergantung pada umask mesin yang mem-build-nya.
chmod 0755 "$ROOT/usr/bin/issboard" "$ROOT/usr/bin/issboard-agent"

install -m 0755 libexec/issboard-smart-collect "$ROOT/usr/libexec/issboard-smart-collect"
install -m 0644 issboard.example.yaml          "$ROOT/etc/issboard.yaml"
install -m 0600 issboard-agent.env.example     "$ROOT/etc/issboard/agent.env"
install -m 0644 README.md                      "$ROOT/usr/share/doc/issboard/README.md"
install -m 0644 LICENSE                        "$ROOT/usr/share/doc/issboard/copyright"

# Unit di repo menunjuk /usr/local (jalur untuk install.sh dan build sendiri).
# Paket memasang ke /usr, jadi jalurnya ditulis ulang di sini — satu-satunya
# tempat perbedaan itu boleh ada.
for u in systemd/*.socket systemd/*.service systemd/*.timer; do
  sed 's#/usr/local/bin/#/usr/bin/#g; s#/usr/local/libexec/#/usr/libexec/#g' \
    "$u" > "$ROOT/usr/lib/systemd/system/$(basename "$u")"
  chmod 0644 "$ROOT/usr/lib/systemd/system/$(basename "$u")"
done

# Berkas yang boleh disunting admin dan tidak boleh ditimpa saat upgrade.
cat > "$ROOT/DEBIAN/conffiles" <<'CONF'
/etc/issboard.yaml
/etc/issboard/agent.env
CONF

cat > "$ROOT/DEBIAN/control" <<CONTROL
Package: issboard
Version: $VERSION
Section: admin
Priority: optional
Architecture: $ARCH
Maintainer: issboard <noreply@example.invalid>
Depends: adduser
Recommends: smartmontools
Suggests: zfsutils-linux, docker.io
Installed-Size: $(du -sk "$ROOT" | cut -f1)
Description: Dashboard kesehatan host untuk server ZFS + Docker
 Satu halaman yang menjawab "ada yang perlu diurus atau tidak": pool ZFS
 (termasuk apakah pernah di-scrub dan apakah benar-benar redundan), ringkasan
 SMART tiap disk, container beserta port yang ter-publish, dan beban host.
 .
 Dashboard-nya memakai socket activation, jadi tidak ada daemon yang menyala
 saat tidak ada yang melihat. Notifikasi dan riwayat dikerjakan issboard-agent
 yang terpisah dan bertimer.
 .
 ZFS dan Docker keduanya OPSIONAL: bagian yang tidak tersedia dilaporkan
 sebagai error per-bagian, dan sisanya tetap disajikan.
CONTROL

cat > "$ROOT/DEBIAN/postinst" <<'POSTINST'
#!/bin/sh
set -e
case "$1" in configure)
  if ! id issboard >/dev/null 2>&1; then
    adduser --system --no-create-home --group --shell /usr/sbin/nologin issboard
  fi
  # ⚠️ Grup docker setara root di kebanyakan sistem. Diberikan HANYA untuk
  # membaca socket; pembatas sebenarnya adalah tidak adanya jalur mutasi.
  if getent group docker >/dev/null 2>&1; then
    usermod -aG docker issboard || true
  fi

  install -d -m 0755 -o issboard -g issboard /var/cache/issboard
  install -d -m 0750 -o issboard -g issboard /var/lib/issboard
  # Di sinilah token notifikasi tinggal.
  chmod 0600 /etc/issboard/agent.env 2>/dev/null || true

  if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    # 🔴 SOCKET-nya yang di-enable, bukan service-nya. Meng-enable
    # issboard.service membuat daemon yang jalan terus — persis yang
    # dihindari desain ini.
    systemctl enable --now issboard.socket || true
    systemctl enable --now issboard-smart.timer || true
    systemctl enable --now issboard-agent.timer || true
  fi

  echo "issboard: buka http://127.0.0.1:9955 (loopback saja)."
  echo "issboard: notifikasi menyala setelah /etc/issboard/agent.env diisi."
;; esac
exit 0
POSTINST

cat > "$ROOT/DEBIAN/prerm" <<'PRERM'
#!/bin/sh
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  for u in issboard.socket issboard.service issboard-agent.timer \
           issboard-smart.timer; do
    systemctl disable --now "$u" || true
  done
fi
exit 0
PRERM

cat > "$ROOT/DEBIAN/postrm" <<'POSTRM'
#!/bin/sh
set -e
[ -d /run/systemd/system ] && systemctl daemon-reload || true
# purge tidak menghapus /var/lib/issboard: riwayat dan ingatan alert milik
# admin, bukan milik paket. Hapus manual kalau memang tidak dibutuhkan lagi.
exit 0
POSTRM

chmod 0755 "$ROOT/DEBIAN/postinst" "$ROOT/DEBIAN/prerm" "$ROOT/DEBIAN/postrm"

# Folder juga mengikuti umask saat mkdir. Disamakan supaya paket yang
# dibangun di mesin mana pun isinya identik.
find "$ROOT" -type d -exec chmod 0755 {} +

dpkg-deb --root-owner-group --build "$ROOT" >/dev/null
printf '==> %s\n' "$ROOT.deb"
dpkg-deb --info "$ROOT.deb" | sed -n '1,12p'
