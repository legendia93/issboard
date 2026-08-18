package collector

import (
	"context"
	"os"
	"strconv"
	"strings"
)

type Host struct {
	Hostname      string  `json:"hostname"`
	Uptime        int64   `json:"uptime_seconds"`
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	MemTotalBytes int64   `json:"mem_total_bytes"`
	MemAvailBytes int64   `json:"mem_available_bytes"`
	SwapTotal     int64   `json:"swap_total_bytes"`
	SwapFree      int64   `json:"swap_free_bytes"`
	ARCSizeBytes  int64   `json:"arc_size_bytes"`
	ARCMaxBytes   int64   `json:"arc_max_bytes"`
	ARCHitRatio   float64 `json:"arc_hit_ratio"`
}

// CollectHost membaca /proc dan /proc/spl/kstat langsung. Tidak memanggil
// proses apa pun: bagian ini harus tetap bekerja saat sistem sedang sibuk.
func CollectHost(_ context.Context) (Host, error) {
	var h Host
	h.Hostname, _ = os.Hostname()

	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			sec, _ := strconv.ParseFloat(f[0], 64)
			h.Uptime = int64(sec)
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			h.Load1, _ = strconv.ParseFloat(f[0], 64)
			h.Load5, _ = strconv.ParseFloat(f[1], 64)
			h.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			kb := parseInt(strings.TrimSuffix(strings.TrimSpace(val), " kB"))
			switch key {
			case "MemTotal":
				h.MemTotalBytes = kb * 1024
			case "MemAvailable":
				h.MemAvailBytes = kb * 1024
			case "SwapTotal":
				h.SwapTotal = kb * 1024
			case "SwapFree":
				h.SwapFree = kb * 1024
			}
		}
	}
	readARC(&h)
	return h, nil
}

func readARC(h *Host) {
	b, err := os.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return
	}
	var hits, misses int64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		v := parseInt(f[2])
		switch f[0] {
		case "size":
			h.ARCSizeBytes = v
		case "c_max":
			h.ARCMaxBytes = v
		case "hits":
			hits = v
		case "misses":
			misses = v
		}
	}
	if hits+misses > 0 {
		h.ARCHitRatio = float64(hits) / float64(hits+misses)
	}
}
