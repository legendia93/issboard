#!/bin/sh
# Tarball generik untuk distro yang tidak memakai dpkg.
#
# Isinya binary statis + unit systemd + install.sh, jadi pemasangannya:
#   tar xzf issboard_<versi>_<arch>.tar.gz
#   cd issboard_<versi>_<arch> && sudo ./install.sh
set -eu

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || true)}"
[ -n "$VERSION" ] || VERSION="0.0.0+$(date -u +%Y%m%d)"
# Nama berkas yang diawali hash commit telanjang menyulitkan membandingkan
# dua unduhan. Disamakan dengan aturan versi paket .deb.
case "$VERSION" in [0-9]*) ;; *) VERSION="0.0.0+git$VERSION" ;; esac

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
