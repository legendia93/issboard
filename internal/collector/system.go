package collector

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// System adalah identitas mesin: hal yang jarang berubah, tapi yang pertama
// ditanyakan orang saat membuka dashboard di host yang tidak dikenalnya.
//
// Distro dan kernel ikut ditampilkan bukan sekadar hiasan: issboard dipakai
// di luar Debian juga, dan saat sesuatu berperilaku aneh, "distro apa dan
// kernel berapa" adalah pertanyaan pertama.
type System struct {
	Distro    string  `json:"distro"`
	Kernel    string  `json:"kernel"`
	Arch      string  `json:"arch"`
	CPUModel  string  `json:"cpu_model"`
	CPUCores  int     `json:"cpu_cores"`
	CPUTempC  float64 `json:"cpu_temp_c"`
	GoVersion string  `json:"go_version"`
}

// CPUTimes adalah cuplikan mentah /proc/stat. Pemakaian CPU hanya bisa
// dihitung dari SELISIH dua cuplikan — tidak ada angka "CPU sekarang" yang
// bisa dibaca sekali jalan. Karena itu cuplikan sebelumnya disimpan di Cache.
type CPUTimes struct {
	Total uint64
	Idle  uint64
}

// Valid membedakan "belum pernah mencuplik" dari "0% terpakai".
func (c CPUTimes) Valid() bool { return c.Total > 0 }

func CollectSystem() System {
	s := System{
		Arch:      runtime.GOARCH,
		CPUCores:  runtime.NumCPU(),
		GoVersion: runtime.Version(),
		Distro:    osReleaseName(),
		Kernel:    strings.TrimSpace(readFileOr("/proc/sys/kernel/osrelease", "")),
		CPUModel:  cpuModel(),
		CPUTempC:  cpuTemp(),
	}
	if s.Distro == "" {
		s.Distro = "tidak dikenal"
	}
	return s
}

func readFileOr(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return string(b)
}

// osReleaseName membaca PRETTY_NAME dari /etc/os-release — standar
// freedesktop yang dipakai hampir semua distro modern, jadi satu pembacaan
// ini bekerja di Debian, Fedora, Arch, dan Alpine sekaligus.
func osReleaseName() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if key, val, ok := strings.Cut(line, "="); ok && key == "PRETTY_NAME" {
				return strings.Trim(strings.TrimSpace(val), `"'`)
			}
		}
	}
	return ""
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, val, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		// "model name" di x86; "Model" di banyak board ARM.
		if ok && (key == "model name" || key == "Model") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// cpuTemp mencari sensor suhu paket CPU.
//
// Dicari lewat hwmon, bukan jalur tetap: nomor hwmon berpindah antar boot dan
// antar mesin, jadi jalur seperti /sys/class/hwmon/hwmon2 tidak bisa dipercaya.
// Nama drivernya yang stabil — coretemp (Intel), k10temp (AMD), dan seterusnya.
//
// 0 berarti tidak terbaca, bukan nol derajat. Normal di VM dan sebagian board,
// dan UI menampilkannya sebagai "—", bukan angka.
func cpuTemp() float64 {
	names := map[string]bool{
		"coretemp": true, "k10temp": true, "k8temp": true,
		"zenpower": true, "cpu_thermal": true, "soc_thermal": true,
	}

	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range dirs {
		name := strings.TrimSpace(readFileOr(filepath.Join(d, "name"), ""))
		if !names[name] {
			continue
		}
		// Ambil yang tertinggi: coretemp memaparkan satu input per core, dan
		// yang menarik adalah core terpanas, bukan rata-rata yang meredamnya.
		var max float64
		inputs, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		for _, in := range inputs {
			raw := strings.TrimSpace(readFileOr(in, ""))
			if v, err := strconv.ParseFloat(raw, 64); err == nil && v/1000 > max {
				max = v / 1000
			}
		}
		if max > 0 {
			return max
		}
	}

	// Cadangan: thermal_zone, satu-satunya yang ada di banyak board ARM.
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, z := range zones {
		t := strings.TrimSpace(readFileOr(filepath.Join(z, "type"), ""))
		if !strings.Contains(t, "cpu") && !strings.Contains(t, "x86_pkg_temp") {
			continue
		}
		raw := strings.TrimSpace(readFileOr(filepath.Join(z, "temp"), ""))
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			return v / 1000
		}
	}
	return 0
}

// ReadCPUTimes mengambil baris agregat "cpu" dari /proc/stat.
func ReadCPUTimes() CPUTimes {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUTimes{}
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var c CPUTimes
		for i, v := range f[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			c.Total += n
			// Kolom 4 = idle, kolom 5 = iowait. iowait dihitung sebagai
			// menganggur: CPU-nya memang tidak bekerja, ia menunggu disk.
			if i == 3 || i == 4 {
				c.Idle += n
			}
		}
		return c
	}
	return CPUTimes{}
}

// CPUPercent menghitung pemakaian antara dua cuplikan. Mengembalikan -1 kalau
// belum bisa dihitung, supaya UI bisa membedakannya dari 0% yang sungguhan.
func CPUPercent(prev, cur CPUTimes) float64 {
	if !prev.Valid() || !cur.Valid() || cur.Total <= prev.Total {
		return -1
	}
	dTotal := float64(cur.Total - prev.Total)
	dIdle := float64(cur.Idle - prev.Idle)
	used := (dTotal - dIdle) / dTotal * 100
	if used < 0 {
		return 0
	}
	if used > 100 {
		return 100
	}
	return used
}
