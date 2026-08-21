package collector

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
	Name          string `json:"name"`
	UsedBytes     int64  `json:"used_bytes"`
	AvailBytes    int64  `json:"avail_bytes"`
	Mountpoint    string `json:"mountpoint"`
	SnapshotCount int    `json:"snapshot_count"`
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

func CollectDatasets(ctx context.Context) ([]Dataset, error) {
	out, err := run(ctx, "zfs", "list", "-Hp", "-o", "name,used,avail,mountpoint")
	if err != nil {
		return nil, err
	}
	counts, _ := snapshotCounts(ctx)
	return parseDatasets(out, counts), nil
}

func parseDatasets(out string, counts map[string]int) []Dataset {
	var ds []Dataset
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		ds = append(ds, Dataset{
			Name:          f[0],
			UsedBytes:     parseInt(f[1]),
			AvailBytes:    parseInt(f[2]),
			Mountpoint:    f[3],
			SnapshotCount: counts[f[0]],
		})
	}
	return ds
}

// snapshotCounts memakai satu panggilan untuk seluruh sistem: memanggil
// `zfs list -t snapshot` per dataset akan jadi puluhan proses per refresh.
func snapshotCounts(ctx context.Context) (map[string]int, error) {
	out, err := run(ctx, "zfs", "list", "-Hp", "-t", "snapshot", "-o", "name")
	if err != nil {
		return map[string]int{}, err
	}
	return parseSnapshotCounts(out), nil
}

func parseSnapshotCounts(out string) map[string]int {
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if ds, _, ok := strings.Cut(line, "@"); ok {
			counts[ds]++
		}
	}
	return counts
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
