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
		p := Pool{
			Name:          f[0],
			SizeBytes:     parseInt(f[1]),
			AllocBytes:    parseInt(f[2]),
			FreeBytes:     parseInt(f[3]),
			Fragmentation: int(parseInt(strings.TrimSuffix(f[4], "%"))),
			Health:        f[5],
		}
		if err := fillPoolStatus(ctx, &p); err != nil {
			return pools, err
		}
		pools = append(pools, p)
	}
	return pools, nil
}

// fillPoolStatus mengurai `zpool status` untuk hal yang tidak disediakan
// `zpool list`: riwayat scrub, bentuk vdev, dan penghitung error.
func fillPoolStatus(ctx context.Context, p *Pool) error {
	out, err := run(ctx, "zpool", "status", p.Name)
	if err != nil {
		return err
	}
	inConfig := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
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
	return nil
}

func CollectDatasets(ctx context.Context) ([]Dataset, error) {
	out, err := run(ctx, "zfs", "list", "-Hp", "-o", "name,used,avail,mountpoint")
	if err != nil {
		return nil, err
	}
	counts, _ := snapshotCounts(ctx)

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
	return ds, nil
}

// snapshotCounts memakai satu panggilan untuk seluruh sistem: memanggil
// `zfs list -t snapshot` per dataset akan jadi puluhan proses per refresh.
func snapshotCounts(ctx context.Context) (map[string]int, error) {
	out, err := run(ctx, "zfs", "list", "-Hp", "-t", "snapshot", "-o", "name")
	if err != nil {
		return map[string]int{}, err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if ds, _, ok := strings.Cut(line, "@"); ok {
			counts[ds]++
		}
	}
	return counts, nil
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
