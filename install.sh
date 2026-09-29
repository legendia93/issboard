#!/bin/sh
# Pemasang issboard untuk sistem ber-systemd.
#
# Jalur yang DIANJURKAN adalah paket .deb (lihat packaging/build-deb.sh).
# Skrip ini untuk distro yang tidak memakai dpkg, atau saat memasang langsung
# dari hasil build sendiri.
#
#   sudo ./install.sh              pasang atau perbarui
#   sudo ./install.sh --uninstall  copot (config dan data TIDAK dihapus)
#
# Sengaja POSIX sh: mesin yang perlu dashboard kesehatan belum tentu punya bash.
set -eu

PREFIX="${PREFIX:-/usr/local}"
UNITDIR="${UNITDIR:-/etc/systemd/system}"
CONFIG="${CONFIG:-/etc/issboard.yaml}"
USER_NAME=issboard

say()  { printf '%s\n' "$*"; }
warn() { printf 'PERINGATAN: %s\n' "$*" >&2; }
die()  { printf 'GAGAL: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "jalankan sebagai root (sudo ./install.sh)"
command -v systemctl >/dev/null 2>&1 || die \
  "systemd tidak ditemukan. Di sistem non-systemd, jalankan issboard langsung
   dengan 'listen:' di config — idle_timeout otomatis nonaktif tanpa socket
   activation, jadi prosesnya tidak akan keluar sendiri tanpa ada yang
   menyalakannya lagi."

cd "$(dirname "$0")"

if [ "${1:-}" = "--uninstall" ]; then
  for u in issboard.socket issboard.service issboard-agent.timer \
           issboard-agent.service issboard-smart.timer issboard-smart.service \
           issboard-helper.socket issboard-helper@.service; do
    systemctl disable --now "$u" >/dev/null 2>&1 || true
    rm -f "$UNITDIR/$u"
  done
  rm -f "$PREFIX/bin/issboard" "$PREFIX/bin/issboard-agent" \
        "$PREFIX/libexec/issboard-smart-collect" "$PREFIX/libexec/issboard-helper"
  systemctl daemon-reload
  say "issboard dicopot."
  say "TIDAK dihapus (sengaja): $CONFIG, /etc/issboard/ (termasuk kredensial),"
  say "/etc/modprobe.d/issboard-zfs-arc.conf kalau ada, /var/lib/issboard/,"
  say "/var/cache/issboard/, dan user sistem '$USER_NAME'."
  exit 0
fi

# --- binary -----------------------------------------------------------------
if [ ! -x ./issboard ] || [ ! -x ./issboard-agent ] || [ ! -x ./issboard-helper ]; then
  command -v go >/dev/null 2>&1 || die \
    "binary belum ada dan Go tidak terpasang. Build dulu di mesin lain:
       CGO_ENABLED=0 go build -o issboard .
       CGO_ENABLED=0 go build -o issboard-agent ./cmd/issboard-agent
       CGO_ENABLED=0 go build -o issboard-helper ./cmd/issboard-helper"
  say "==> membangun binary"
  CGO_ENABLED=0 go build -o issboard .
  CGO_ENABLED=0 go build -o issboard-agent ./cmd/issboard-agent
  CGO_ENABLED=0 go build -o issboard-helper ./cmd/issboard-helper
fi

say "==> memasang binary ke $PREFIX"
install -d -m 0755 "$PREFIX/bin" "$PREFIX/libexec"
install -m 0755 issboard                     "$PREFIX/bin/issboard"
install -m 0755 issboard-agent               "$PREFIX/bin/issboard-agent"
install -m 0755 libexec/issboard-smart-collect "$PREFIX/libexec/issboard-smart-collect"
install -m 0755 issboard-helper              "$PREFIX/libexec/issboard-helper"

# --- user sistem ------------------------------------------------------------
if ! id "$USER_NAME" >/dev/null 2>&1; then
  say "==> membuat user sistem $USER_NAME"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME" 2>/dev/null ||
    adduser --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME" 2>/dev/null ||
    warn "gagal membuat user $USER_NAME — buat manual sebelum menyalakan unit"
fi

# issboard-helper.socket memakai SocketGroup=issboard; tanpa grup ini unit
# socket-nya gagal dinyalakan. useradd --system tidak selalu membuatnya.
getent group "$USER_NAME" >/dev/null 2>&1 || groupadd --system "$USER_NAME" 2>/dev/null || true
usermod -aG "$USER_NAME" "$USER_NAME" 2>/dev/null || true

# ⚠️ Grup docker setara root di kebanyakan sistem, dan sejak fase 8 dipakai
# juga untuk aksi container. Pembatasnya autentikasi issboard (fase 7).
if getent group docker >/dev/null 2>&1; then
  usermod -aG docker "$USER_NAME" 2>/dev/null || true
elif getent group podman >/dev/null 2>&1; then
  usermod -aG podman "$USER_NAME" 2>/dev/null || true
fi

# --- config & folder data ---------------------------------------------------
# Config yang sudah ada TIDAK PERNAH ditimpa: di situlah topik ntfy dan daftar
# pool milik mesin ini tinggal.
if [ -f "$CONFIG" ]; then
  say "==> $CONFIG sudah ada, dibiarkan apa adanya"
else
  install -m 0644 issboard.example.yaml "$CONFIG"
  say "==> config contoh dipasang di $CONFIG"
fi

install -d -m 0755 /etc/issboard
if [ ! -f /etc/issboard/agent.env ]; then
  # 0600 milik root: di sinilah token notifikasi tinggal, dan berkas config
  # utama boleh dibaca siapa saja.
  install -m 0600 -o root -g root issboard-agent.env.example /etc/issboard/agent.env
  say "==> contoh kredensial dipasang di /etc/issboard/agent.env (mode 0600)"
fi

install -d -m 0755 -o "$USER_NAME" -g "$USER_NAME" /var/cache/issboard
install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" /var/lib/issboard

# --- unit systemd -----------------------------------------------------------
say "==> memasang unit ke $UNITDIR"
install -d -m 0755 "$UNITDIR"
for u in systemd/*.socket systemd/*.service systemd/*.timer; do
  install -m 0644 "$u" "$UNITDIR/$(basename "$u")"
done
systemctl daemon-reload

# 🔴 Yang di-enable adalah SOCKET-nya, bukan service-nya. Meng-enable
# issboard.service membuat daemon yang jalan terus — persis yang dihindari.
systemctl enable --now issboard.socket
systemctl enable --now issboard-smart.timer
systemctl enable --now issboard-agent.timer
systemctl enable --now issboard-helper.socket

# --- catatan jujur ----------------------------------------------------------
say ""
say "Selesai. Buka http://127.0.0.1:9955 (loopback saja — dari luar lewat"
say "SSH tunnel atau reverse proxy yang punya autentikasi sendiri)."
say ""
command -v zpool     >/dev/null 2>&1 || warn "zpool tidak ada — bagian ZFS akan kosong dan melaporkan error. Itu bukan kerusakan issboard."
command -v smartctl  >/dev/null 2>&1 && say "Cache SMART pertama terisi beberapa menit lagi (issboard-smart.timer)." \
                                     || warn "smartctl tidak ada — pasang smartmontools kalau ingin panel SMART."
[ -S /var/run/docker.sock ] || [ -S /run/podman/podman.sock ] || \
  warn "socket container tidak terlihat — sesuaikan docker_socket: di $CONFIG"
say ""
say "Notifikasi belum menyala sampai kanalnya diisi:"
say "  sudoedit /etc/issboard/agent.env      # topik ntfy / token Telegram"
say "  systemctl restart issboard-agent.timer"
say "Coba tanpa mengirim apa pun:  issboard-agent -dry-run"
if [ ! -f /etc/issboard/auth ]; then
  say ""
  say "Aksi dari dashboard (container, scrub, SMART, ARC) mati sampai kata"
  say "sandi operator diatur:  sudo $PREFIX/bin/issboard -set-password"
fi
