package collector

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Pool struct {
	Name          string `json:"name"`
	Health        string `json:"health"`
	SizeBytes     int64  `json:"size_bytes"`
	AllocBytes    int64  `json:"alloc_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
	Fragmentation int    `json:"fragmentation_pct"`
	// ScanLine adalah baris "scan:" apa adanya dari `zpool status`. Kosong
	// berarti pool BELUM PERNAH di-scrub — itu sinyal, bukan ketiadaan data.
	ScanLine string `json:"scan_line"`
	// Mirrored false berarti stripe: satu disk mati, seluruh pool hilang.
	Mirrored bool     `json:"mirrored"`
	Devices  []string `json:"devices"`
	ReadErr  int64    `json:"read_errors"`
	WriteErr int64    `json:"write_errors"`
	CksumErr int64    `json:"cksum_errors"`
}

type Dataset struct {
	Name       string `json:"name"`
	UsedBytes  int64  `json:"used_bytes"`
	AvailBytes int64  `json:"avail_bytes"`
	// UsedByDataset adalah data yang benar-benar duduk DI dataset ini, tanpa
	// keturunan dan tanpa snapshot. Dipakai untuk membedakan dataset induk
	// yang cuma wadah — `used`-nya ratusan GB karena anak-anaknya — dari
	// dataset yang sungguh menyimpan sesuatu. Aturan "tidak tercakup" berdiri
	// di atas beda itu; memakai `used` akan menandai tiap dataset induk.
	UsedByDataset int64  `json:"used_by_dataset"`
	Mountpoint    string `json:"mountpoint"`
	SnapshotCount int    `json:"snapshot_count"`
	// LastSnapshot nil berarti dataset ini BELUM PERNAH punya snapshot.
	//
	// 🔴 Sengaja pointer, bukan time.Time. `omitempty` TIDAK berlaku untuk
	// struct: time.Time yang nol tetap terkirim sebagai "0001-01-01T00:00:00Z",
	// dan penerima mana pun yang cuma memeriksa "ada isinya atau tidak" akan
	// membacanya sebagai tanggal sungguhan — lalu melaporkan umur snapshot
	// dalam ratusan ribu hari. Ketiadaan harus terlihat SEBAGAI ketiadaan,
	// bukan menyamar jadi nilai; ini kesalahan yang sama dengan membaca suhu 0
	// sebagai dingin (docs/plan/05-pemasangan.md, cacat #2).
	LastSnapshot *time.Time `json:"last_snapshot,omitempty"`
	// SnapPolicy nil berarti tidak ada bagian di berkas kebijakan yang
	// mencakupnya — belum tentu salah, lihat internal/health.
	SnapPolicy *SnapPolicy `json:"snap_policy,omitempty"`
	// SnapExempt: dikecualikan dengan sadar lewat config.
	SnapExempt bool `json:"snap_exempt,omitempty"`
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func CollectPools(ctx context.Context, only []string) ([]Pool, error) {
	out, err := run(ctx, "zpool", "list", "-Hp", "-o", "name,size,alloc,free,frag,health")
	if err != nil {
		return nil, err
	}
	pools := parsePoolList(out, only)
	for i := range pools {
		st, err := run(ctx, "zpool", "status", pools[i].Name)
		if err != nil {
			return pools, err
		}
		parsePoolStatus(st, &pools[i])
	}
	return pools, nil
}

// parsePoolList membaca keluaran `zpool list -Hp`. Dipisahkan dari
// pemanggilan perintahnya supaya bisa diuji: di parser inilah data dunia
// nyata paling sering mengejutkan.
func parsePoolList(out string, only []string) []Pool {
	want := map[string]bool{}
	for _, p := range only {
		want[p] = true
	}

	var pools []Pool
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			continue
		}
		if len(want) > 0 && !want[f[0]] {
			continue
		}
		pools = append(pools, Pool{
			Name:          f[0],
			SizeBytes:     parseInt(f[1]),
			AllocBytes:    parseInt(f[2]),
			FreeBytes:     parseInt(f[3]),
			Fragmentation: int(parseInt(strings.TrimSuffix(f[4], "%"))),
			Health:        f[5],
		})
	}
	return pools
}

// parsePoolStatus mengurai `zpool status` untuk hal yang tidak disediakan
// `zpool list`: riwayat scrub, bentuk vdev, dan penghitung error.
//
// Bentuk keluarannya berbeda antar versi ZFS, jadi yang dibaca hanya hal yang
// stabil — dan itulah sebabnya bagian ini yang paling butuh test.
func parsePoolStatus(out string, p *Pool) {
	inConfig := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		// Nama pool dibaca dari keluarannya sendiri kalau belum diketahui.
		// Tanpa ini, parser bergantung pada pemanggil yang sudah mengisi
		// Name lebih dulu — dan kalau lupa, baris nama pool ikut terhitung
		// sebagai disk, sehingga pool mirror terbaca sebagai stripe. Itu
		// kebalikan persis dari temuan yang paling penting di dashboard ini.
		case strings.HasPrefix(line, "pool:"):
			if p.Name == "" {
				p.Name = strings.TrimSpace(strings.TrimPrefix(line, "pool:"))
			}
		case strings.HasPrefix(line, "scan:"):
			p.ScanLine = strings.TrimSpace(strings.TrimPrefix(line, "scan:"))
		case strings.HasPrefix(line, "config:"):
			inConfig = true
		case strings.HasPrefix(line, "errors:"):
			inConfig = false
		case inConfig:
			f := strings.Fields(line)
			if len(f) < 5 || f[0] == "NAME" || f[0] == p.Name {
				continue
			}
			if strings.HasPrefix(f[0], "mirror") || strings.HasPrefix(f[0], "raidz") {
				p.Mirrored = true
				continue
			}
			if strings.HasPrefix(f[0], "logs") || strings.HasPrefix(f[0], "cache") ||
				strings.HasPrefix(f[0], "spares") {
				continue
			}
			p.Devices = append(p.Devices, f[0])
			p.ReadErr += parseInt(f[2])
			p.WriteErr += parseInt(f[3])
			p.CksumErr += parseInt(f[4])
		}
	}
}

// CollectDatasets mengembalikan daftar dataset beserta kebijakan snapshot yang
// berlaku untuk masing-masing. Set kebijakannya ikut dikembalikan karena
// pemanggil perlu tahu bedanya "tidak tercakup" dan "tidak ada berkas
// kebijakan sama sekali" — dua hal yang sama-sama menghasilkan SnapPolicy nil.
func CollectDatasets(ctx context.Context, o Options) ([]Dataset, SnapPolicySet, error) {
	pol, polErr := LoadSnapPolicy(o.SnapPolicyFile)

	out, err := run(ctx, "zfs", "list", "-Hp", "-o", "name,used,avail,mountpoint,usedbydataset")
	if err != nil {
		return nil, pol, err
	}
	snaps, snapErr := snapshotSummary(ctx)

	ds := parseDatasets(out, snaps)
	for i := range ds {
		ds[i].SnapExempt = snapExempt(ds[i].Name, o.SnapExempt)
		if pol.Present {
			ds[i].SnapPolicy = pol.For(ds[i].Name)
		}
	}
	// Kegagalan membaca snapshot atau berkas kebijakan tidak boleh menghapus
	// daftar datasetnya: yang hilang cuma satu kolom, dan itu jauh lebih baik
	// daripada kartu dataset yang kosong sama sekali. Errornya tetap dilaporkan
	// supaya halaman mengaku tidak tahu, bukan terlihat sehat karena buta.
	return ds, pol, firstErr(snapErr, polErr)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func parseDatasets(out string, snaps map[string]snapInfo) []Dataset {
	var ds []Dataset
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		d := Dataset{
			Name:       f[0],
			UsedBytes:  parseInt(f[1]),
			AvailBytes: parseInt(f[2]),
			Mountpoint: f[3],
		}
		if len(f) >= 5 {
			d.UsedByDataset = parseInt(f[4])
		}
		if s, ok := snaps[f[0]]; ok {
			d.SnapshotCount = s.count
			if !s.last.IsZero() {
				at := s.last
				d.LastSnapshot = &at
			}
		}
		ds = append(ds, d)
	}
	return ds
}

type snapInfo struct {
	count int
	last  time.Time
}

// snapshotSummary memakai satu panggilan untuk seluruh sistem: memanggil
// `zfs list -t snapshot` per dataset akan jadi puluhan proses per refresh.
// Di host dengan 653 snapshot satu panggilan ini 73 ms, jadi memindahkannya
// ke agent tidak diperlukan.
func snapshotSummary(ctx context.Context) (map[string]snapInfo, error) {
	out, err := run(ctx, "zfs", "list", "-Hp", "-t", "snapshot", "-o", "name,creation")
	if err != nil {
		return map[string]snapInfo{}, err
	}
	return parseSnapshotSummary(out), nil
}

// parseSnapshotSummary mengambil yang TERBARU per dataset, bukan yang terakhir
// tercetak: urutan keluaran `zfs list` tidak dijamin, dan snapshot yang salah
// pilih berarti umur perlindungan dilaporkan lebih muda daripada kenyataan —
// arah kebohongan yang paling berbahaya untuk aturan ini.
func parseSnapshotSummary(out string) map[string]snapInfo {
	m := map[string]snapInfo{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, created, _ := strings.Cut(line, "\t")
		ds, _, ok := strings.Cut(name, "@")
		if !ok {
			continue
		}
		s := m[ds]
		s.count++
		if sec := parseInt(created); sec > 0 {
			if at := time.Unix(sec, 0); at.After(s.last) {
				s.last = at
			}
		}
		m[ds] = s
	}
	return m
}

func parseInt(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
