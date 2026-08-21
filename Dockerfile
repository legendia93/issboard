# issboard di dalam container.
#
# 🔴 BACA INI DULU. Jalur ini didukung, tapi ia MELAWAN alasan utama proyek
# ini ada, dan itu bukan gaya bahasa:
#
#  1. Kalau Docker mati, dashboard-nya ikut mati — persis saat paling
#     dibutuhkan. issboard dibuat sebagai binary di host justru supaya tetap
#     hidup saat container-nya bermasalah (docs/design.md §3.1).
#
#  2. ZFS TIDAK didukung penuh dari dalam container. Biner zfs/zpool di image
#     harus cocok versinya dengan modul kernel ZFS di host; host zfs 2.1 +
#     image zfs 2.2 = error yang membingungkan. Versi ZFS penonton tidak bisa
#     dikontrol, jadi image ini sengaja TIDAK memasang zfsutils sama sekali.
#
#  3. Socket activation tidak ada di sini, jadi prosesnya jalan terus —
#     bukan lagi 0 MB saat menganggur.
#
# Pakai ini untuk mencoba-coba. Untuk dipasang sungguhan: .deb atau install.sh.

FROM golang:1.26-alpine AS build
WORKDIR /src
# Nol dependensi di luar pustaka standar berarti tidak ada langkah unduh
# modul sama sekali — cukup salin dan build.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/issboard . && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/issboard-agent ./cmd/issboard-agent

# smartmontools TIDAK ikut: SMART dikumpulkan unit root di HOST, dan issboard
# hanya membaca berkas cache-nya. Memanggil smartctl dari container berarti
# membangunkan HDD yang sedang tidur — larangan keras di docs/design.md §3.3.
FROM alpine:3.21
RUN adduser -S -H -s /sbin/nologin issboard
COPY --from=build /out/issboard /out/issboard-agent /usr/bin/
COPY issboard.example.yaml /etc/issboard.yaml

# Di container tidak ada systemd, jadi tidak ada socket activation — dan
# issboard otomatis menonaktifkan idle-exit dalam keadaan itu, karena tidak
# ada apa pun yang akan menyalakannya lagi setelah keluar.
ENV ISSBOARD_LISTEN=0.0.0.0:9955
EXPOSE 9955
USER issboard
ENTRYPOINT ["/usr/bin/issboard", "-config", "/etc/issboard.yaml"]
