# issboard

Dashboard kesehatan host untuk server rumahan berbasis **ZFS + Docker**:
satu binary Go statis, frontend ikut ter-*embed*, tanpa runtime apa pun di
server. Read-only di v1.

Yang ditampilkan: pool & dataset ZFS (termasuk **apakah pool pernah di-scrub**
dan **apakah benar-benar redundan**), ringkasan SMART tiap disk, daftar
container beserta port yang ter-*publish*, dan beban host + ARC.

> **Status: v1 awal.** Berjalan dan menyajikan data nyata, tapi belum dipakai
> lama di produksi. Belum ada test otomatis.

## Kenapa begini

Tiga keputusan yang membentuk seluruh desainnya:

**Satu binary Go, di host, bukan container.** Dashboard kesehatan harus tetap
hidup justru saat Docker sedang bermasalah. Container yang membaca `zfs`,
`smartctl`, dan socket Docker butuh `privileged` + mount host — meniadakan
isolasinya sambil menambah kerumitan.

**Socket activation, bukan daemon.** Yang di-*enable* adalah
`issboard.socket`; systemd yang memegang port, prosesnya baru hidup saat
halaman dibuka dan keluar sendiri setelah idle. Nol RAM dan nol permukaan
serang saat tidak ada yang melihat. Konsekuensinya diterima sadar: **tidak ada
poll latar belakang, jadi dashboard ini bukan sumber alert.** Kalau butuh
notifikasi, itu unit systemd terpisah — jangan ubah issboard jadi daemon.

**`smartctl` tidak pernah dipanggil di jalur request.** Memanggilnya
**membangunkan HDD yang sedang tidur**. Dengan socket activation, satu kali
buka halaman dari HP = seluruh disk bangun; dibuka sepuluh kali sehari, disk
tidak pernah sempat tidur. Karena itu SMART dikumpulkan unit root terpisah
tiap 6 jam (dengan `-n standby`) ke sebuah file JSON, dan issboard hanya
membaca file itu. Cache yang basi ditandai di UI — timer yang mati adalah
temuan tersendiri.

## Membangun

```bash
go build -o issboard .      # butuh Go 1.26+
```

Menjalankan untuk mengembangkan (tanpa systemd, memakai `listen:` dari config):

```bash
./issboard -config issboard.example.yaml
# lalu buka http://127.0.0.1:9955
```

Di mesin tanpa ZFS/Docker, issboard tetap jalan dan melaporkan bagian yang
gagal di field `errors` — bukan mati.

## Memasang

```bash
sudo install -m 0755 issboard              /usr/local/bin/issboard
sudo install -m 0755 libexec/issboard-smart-collect \
                                           /usr/local/libexec/issboard-smart-collect
sudo install -m 0644 issboard.example.yaml /etc/issboard.yaml
sudo install -m 0644 systemd/*             /etc/systemd/system/

sudo useradd --system --no-create-home --shell /usr/sbin/nologin issboard
sudo usermod -aG docker issboard           # hanya untuk MEMBACA socket

sudo systemctl daemon-reload
sudo systemctl enable --now issboard-smart.timer
sudo systemctl enable --now issboard.socket   # socket, BUKAN service
```

Sunting `/etc/issboard.yaml` seperlunya, lalu buka `http://127.0.0.1:9955`.

> ⚠️ **`issboard.service` sengaja tidak untuk di-`enable`.** Socket yang
> menyalakannya. Meng-`enable` service-nya membuat daemon yang jalan terus —
> persis yang dihindari desain ini.

## Keamanan

- **Bind ke loopback saja.** Akses dari luar lewat SSH tunnel atau reverse
  proxy yang punya autentikasi. Jangan taruh langsung di LAN atau tailnet.
- **issboard tidak punya autentikasi sendiri** di v1. Ini disengaja: read-only
  di belakang loopback. Begitu ada endpoint yang bermutasi, autentikasi wajib
  lebih dulu.
- Jalan sebagai user sistem sendiri, bukan root, dengan pengerasan systemd.
- ⚠️ Keanggotaan grup `docker` **setara root** di kebanyakan sistem. Pembatas
  sebenarnya di v1 adalah tidak adanya jalur mutasi sama sekali.

## API

| Endpoint | Isi |
|---|---|
| `GET /api/v1/health` | liveness, tanpa mengumpulkan apa pun |
| `GET /api/v1/status` | seluruh snapshot: host, pools, datasets, containers, smart |

Kegagalan per-bagian muncul di `errors[]`, bukan menggagalkan seluruh respons.

## Lisensi

MIT — lihat [LICENSE](LICENSE).
