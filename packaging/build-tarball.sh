#!/bin/sh
# Tarball generik untuk distro yang tidak memakai dpkg.
#
# Isinya binary statis + unit systemd + install.sh, jadi pemasangannya:
#   tar xzf issboard_<versi>_<arch>.tar.gz
#   cd issboard_<versi>_<arch> && sudo ./install.sh
set -eu

cd "$(dirname "$0")/.."

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  # Versi paket menentukan apakah apt menganggap build berikutnya sebagai
  # UPGRADE. Hash commit tidak boleh dipakai mentah: ia tidak berurutan, dan
  # hash yang kebetulan diawali angka (mis. 9ca9266) lolos begitu saja lalu
  # membuat build berikutnya terlihat lebih tua. Jumlah commit berurutan naik,
  # jadi itu yang jadi tulang punggungnya; hash-nya ikut sebagai keterangan.
  if VERSION=$(git describe --tags --dirty 2>/dev/null); then
    VERSION=$(printf '%s' "$VERSION" | sed 's/^v//')
  elif n=$(git rev-list --count HEAD 2>/dev/null); then
    h=$(git rev-parse --short HEAD)
    git diff --quiet 2>/dev/null || h="$h+dirty"
    VERSION="0.0.0+$n.g$h"
  else
    VERSION="0.0.0+$(date -u +%Y%m%d)"
  fi
fi
GOARCH="${GOARCH:-$(go env GOARCH)}"

NAME="issboard_${VERSION}_${GOARCH}"
ROOT="dist/$NAME"
rm -rf "$ROOT"
mkdir -p "$ROOT/systemd" "$ROOT/libexec"

printf '==> membangun binary (%s, statis)\n' "$GOARCH"
CGO_ENABLED=0 GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$ROOT/issboard" .
CGO_ENABLED=0 GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$ROOT/issboard-agent" ./cmd/issboard-agent
chmod 0755 "$ROOT/issboard" "$ROOT/issboard-agent"

cp systemd/*.socket systemd/*.service systemd/*.timer "$ROOT/systemd/"
cp libexec/issboard-smart-collect "$ROOT/libexec/"
cp install.sh issboard.example.yaml issboard-agent.env.example README.md LICENSE "$ROOT/"
chmod 0755 "$ROOT/install.sh" "$ROOT/libexec/issboard-smart-collect"

# Tar dibuat tanpa kepemilikan mesin yang mem-build: isi arsip tidak boleh
# bergantung pada siapa yang kebetulan menjalankannya.
tar -czf "$ROOT.tar.gz" --owner=0 --group=0 -C dist "$NAME"
rm -rf "$ROOT"
printf '==> %s\n' "$ROOT.tar.gz"
tar -tzf "$ROOT.tar.gz" | sed 's/^/    /'
