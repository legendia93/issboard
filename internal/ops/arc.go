package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// ARCParam adalah parameter modul yang dibaca ZFS saat berjalan.
	ARCParam = "/sys/module/zfs/parameters/zfs_arc_max"
	// ModprobeDir: tempat pengaturan modul yang dibaca saat boot.
	ModprobeDir = "/etc/modprobe.d"
	// ARCPersistFile adalah SATU-SATUNYA berkas yang pernah ditulis helper di
	// ModprobeDir. Berkas milik orang atau paket lain tidak pernah disunting —
	// alasan yang sama dengan tidak menyunting sanoid.conf (plan 08).
	ARCPersistFile = "/etc/modprobe.d/issboard-zfs-arc.conf"

	mib = 1 << 20
	gib = 1 << 30
)

// ARCBounds adalah rentang batas ARC yang diterima.
//
// Batas bawah bukan soal selera: ZFS DIAM-DIAM MENGABAIKAN zfs_arc_max yang
// ≤ zfs_arc_min atau < 64 MiB. Penulisan ke /sys tetap "berhasil", c_max
// tidak berubah, dan tidak ada pesan apa pun. Menolaknya di sini lebih jujur
// daripada tombol yang terlihat bekerja padahal tidak.
//
// Batas atas menyisakan 1 GiB untuk sistem dan container. ARC memang akan
// menyusut saat memori ditekan, tapi tidak secepat alokasi yang mendadak,
// dan OOM killer tidak menunggu ARC.
func ARCBounds(memTotal, cMin int64) (lo, hi int64) {
	lo = 256 * mib
	if cMin+mib > lo {
		lo = cMin + mib
	}
	hi = memTotal - gib
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// ValidateARC memeriksa nilai yang diminta. 0 selalu sah: artinya "kembali ke
// bawaan ZFS" (setengah RAM di kebanyakan versi).
func ValidateARC(v, memTotal, cMin int64) error {
	if v == 0 {
		return nil
	}
	if v < 0 {
		return fmt.Errorf("batas ARC tidak boleh negatif")
	}
	lo, hi := ARCBounds(memTotal, cMin)
	if v < lo || v > hi {
		return fmt.Errorf("batas ARC %s di luar rentang %s–%s", human(v), human(lo), human(hi))
	}
	return nil
}

// ARCInfo adalah keadaan ARC dan pengaturannya, cukup untuk menggambar panel
// pengaturan tanpa root. Semua berkasnya boleh dibaca siapa saja.
type ARCInfo struct {
	Present   bool  `json:"present"`
	SizeBytes int64 `json:"size_bytes"`
	// CMax/CMin adalah batas yang SEDANG BERLAKU menurut ZFS sendiri —
	// bukan yang diminta. Keduanya bisa berbeda (lihat ARCBounds).
	CMax int64 `json:"c_max"`
	CMin int64 `json:"c_min"`
	// Param adalah isi zfs_arc_max saat ini. 0 = bawaan.
	Param    int64 `json:"param"`
	MemTotal int64 `json:"mem_total"`
	Lower    int64 `json:"lower"`
	Upper    int64 `json:"upper"`
	// Persisted: nilai yang akan berlaku setelah reboot, per berkas. Lebih
	// dari satu berkas berarti ada yang saling menimpa — itu yang ingin
	// terlihat, bukan disembunyikan.
	Persisted []PersistedARC `json:"persisted"`
}

type PersistedARC struct {
	File  string `json:"file"`
	Value int64  `json:"value"`
	Ours  bool   `json:"ours"`
}

// ReadARCInfo membaca keadaan ARC dari /proc dan /sys. Tidak ada ZFS = Present
// false, bukan error: issboard dipakai juga di mesin tanpa ZFS.
func ReadARCInfo() ARCInfo {
	var a ARCInfo
	b, err := os.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return a
	}
	a.Present = true
	a.SizeBytes, a.CMax, a.CMin = parseArcstats(string(b))
	a.Param = readInt(ARCParam)
	a.MemTotal = memTotal()
	a.Lower, a.Upper = ARCBounds(a.MemTotal, a.CMin)
	a.Persisted = ReadPersistedARC(ModprobeDir)
	return a
}

func parseArcstats(s string) (size, cmax, cmin int64) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		v, _ := strconv.ParseInt(f[2], 10, 64)
		switch f[0] {
		case "size":
			size = v
		case "c_max":
			cmax = v
		case "c_min":
			cmin = v
		}
	}
	return
}

// ReadPersistedARC mencari zfs_arc_max di seluruh berkas modprobe.d.
func ReadPersistedARC(dir string) []PersistedARC {
	files, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
	sort.Strings(files)
	var out []PersistedARC
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if v, ok := parseModprobeARC(string(b)); ok {
			out = append(out, PersistedARC{File: f, Value: v,
				Ours: filepath.Base(f) == filepath.Base(ARCPersistFile)})
		}
	}
	return out
}

// parseModprobeARC mengambil zfs_arc_max dari baris `options zfs ...`.
// Kalau muncul lebih dari sekali, yang terakhir menang — sama dengan modprobe.
func parseModprobeARC(s string) (int64, bool) {
	var val int64
	found := false
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "options" || f[1] != "zfs" {
			continue
		}
		for _, kv := range f[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k != "zfs_arc_max" {
				continue
			}
			if n, err := strconv.ParseInt(v, 0, 64); err == nil {
				val, found = n, true
			}
		}
	}
	return val, found
}

func readInt(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func memTotal() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func human(n int64) string {
	if n >= gib {
		return strconv.FormatFloat(float64(n)/gib, 'f', 1, 64) + " GiB"
	}
	return strconv.FormatInt(n/mib, 10) + " MiB"
}
