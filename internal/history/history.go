// Package history menyimpan deret waktu ringkas ke satu berkas JSON.
//
// 🔴 Ini BUKAN alasan untuk mengubah issboard jadi daemon. Yang menulis berkas
// ini adalah issboard-agent — unit bertimer yang hidup sebentar lalu keluar —
// dan issboard hanya MEMBACANYA, persis pola cache SMART (docs/design.md §3.3).
// Dengan begitu RAM idle tetap 0 MB, dan riwayatnya justru selamat dari reboot,
// sesuatu yang daemon in-memory tidak punya.
package history

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/legendia93/issboard/internal/collector"
)

// Dua lapis, supaya berkasnya tetap kecil (< 20 KB) tapi jangkauannya panjang.
const (
	FineStep   = 60 * time.Second
	FineMax    = 60 // 1 jam terakhir, resolusi menit
	CoarseStep = 30 * time.Minute
	CoarseMax  = 48 // 24 jam terakhir
)

// Point adalah satu cuplikan. Kuncinya sengaja pendek: berkas ini berisi
// ratusan titik dan dibaca utuh tiap siklus.
//
// Yang disimpan hanya angka yang berguna sebagai TREN. Jumlah container tidak
// masuk: itu angka yang melompat, bukan sesuatu yang naik-turun perlahan.
type Point struct {
	T     int64   `json:"t"`
	Load1 float64 `json:"load1"`
	// CPU bernilai -1 kalau tidak bisa dihitung — dibedakan dari 0% sungguhan.
	CPU      float64 `json:"cpu"`
	MemUsed  int64   `json:"mem_used"`
	SwapUsed int64   `json:"swap_used"`
	ARC      int64   `json:"arc"`
	// Pools menyimpan persen terpakai, bukan byte: itu yang digambar UI, dan
	// map membuat pool yang datang/pergi tidak merusak titik-titik lama.
	Pools map[string]float64 `json:"pools,omitempty"`
	// Disks menyimpan suhu. Disk yang sedang tidur TIDAK dicatat sama sekali —
	// 0°C bukan "dingin", itu "tidak terbaca", dan menyimpannya sebagai 0 akan
	// menggambar jurang palsu di sparkline.
	Disks map[string]float64 `json:"disks,omitempty"`
}

// Series adalah satu lapis ring buffer.
type Series struct {
	StepSeconds int     `json:"step_seconds"`
	Points      []Point `json:"points"`
}

// CPUMark adalah cuplikan mentah /proc/stat dari siklus sebelumnya.
//
// Pemakaian CPU hanya bisa dihitung dari selisih dua cuplikan, dan agent yang
// hidup beberapa milidetik tidak punya cuplikan sebelumnya. Menyimpannya di
// sini membuat angkanya jadi rata-rata satu menit penuh — lebih berarti
// daripada tidur 300 ms lalu mengukur selisih sesaat.
type CPUMark struct {
	At    time.Time `json:"at"`
	Total uint64    `json:"total"`
	Idle  uint64    `json:"idle"`
}

type File struct {
	WrittenAt time.Time `json:"written_at"`
	Fine      Series    `json:"fine"`
	Coarse    Series    `json:"coarse"`
	CPUMark   *CPUMark  `json:"cpu_mark,omitempty"`
}

// Load membaca berkas riwayat. Berkas yang belum ada bukan error: mesin yang
// baru dipasang memang belum punya riwayat, dan agent harus tetap jalan.
func Load(path string) (File, error) {
	f := File{
		Fine:   Series{StepSeconds: int(FineStep.Seconds())},
		Coarse: Series{StepSeconds: int(CoarseStep.Seconds())},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, err
	}
	// Berkas rusak juga bukan alasan berhenti: lebih baik memulai riwayat baru
	// daripada agent mati dan notifikasi ikut berhenti.
	if err := json.Unmarshal(b, &f); err != nil {
		return File{
			Fine:   Series{StepSeconds: int(FineStep.Seconds())},
			Coarse: Series{StepSeconds: int(CoarseStep.Seconds())},
		}, err
	}
	f.Fine.StepSeconds = int(FineStep.Seconds())
	f.Coarse.StepSeconds = int(CoarseStep.Seconds())
	return f, nil
}

// Append menaruh satu titik di lapis halus, lalu memadatkan ke lapis kasar
// kalau jendela 30 menit sudah lewat.
func (f *File) Append(p Point) {
	f.Fine.Points = trim(append(f.Fine.Points, p), FineMax)

	var lastCoarse int64
	if n := len(f.Coarse.Points); n > 0 {
		lastCoarse = f.Coarse.Points[n-1].T
	}
	if lastCoarse != 0 && p.T-lastCoarse < int64(CoarseStep.Seconds()) {
		return
	}

	var window []Point
	for _, q := range f.Fine.Points {
		if q.T > lastCoarse {
			window = append(window, q)
		}
	}
	if len(window) == 0 {
		return
	}
	f.Coarse.Points = trim(append(f.Coarse.Points, mean(window)), CoarseMax)
}

// Save menulis atomik: tulis ke berkas sementara di folder yang sama, lalu
// rename. Pembaca tidak boleh pernah mendapat berkas separuh jadi — sama
// seperti cache SMART.
func Save(path string, f File) error {
	f.WrittenAt = time.Now()
	// Tanpa indent: 108 titik dengan indent membengkak beberapa kali lipat,
	// dan berkas ini tidak dibaca manusia.
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // tidak berbahaya kalau rename sudah berhasil

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 0644: issboard berjalan sebagai user yang sama, tapi berkas ini juga
	// tidak rahasia — isinya angka beban, bukan kredensial.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// PointFrom menyaring snapshot jadi angka-angka yang layak jadi tren.
func PointFrom(s collector.Snapshot) Point {
	p := Point{
		T:     s.CollectedAt.Unix(),
		Load1: round1(s.Host.Load1),
		CPU:   round1(s.Host.CPUPercent),
		ARC:   s.Host.ARCSizeBytes,
	}
	if s.Host.MemTotalBytes > 0 {
		p.MemUsed = s.Host.MemTotalBytes - s.Host.MemAvailBytes
	}
	if s.Host.SwapTotal > 0 {
		p.SwapUsed = s.Host.SwapTotal - s.Host.SwapFree
	}
	for _, pool := range s.Pools {
		if pool.SizeBytes <= 0 {
			continue
		}
		if p.Pools == nil {
			p.Pools = map[string]float64{}
		}
		p.Pools[pool.Name] = round1(float64(pool.AllocBytes) / float64(pool.SizeBytes) * 100)
	}
	for _, d := range s.Smart.Disks {
		// Disk tidur dilewati, bukan dicatat 0. Lihat catatan di Point.Disks.
		if d.Standby || d.Temperature <= 0 {
			continue
		}
		if p.Disks == nil {
			p.Disks = map[string]float64{}
		}
		p.Disks[d.Device] = float64(d.Temperature)
	}
	return p
}

func trim(p []Point, max int) []Point {
	if len(p) > max {
		return p[len(p)-max:]
	}
	return p
}

// mean memadatkan sejendela titik halus jadi satu titik kasar.
//
// Rata-rata, bukan nilai terakhir: satu cuplikan sesaat tiap 30 menit akan
// melewatkan lonjakan yang justru ingin terlihat di grafik 24 jam.
func mean(in []Point) Point {
	out := Point{T: in[len(in)-1].T, CPU: -1}
	var cpuSum float64
	var cpuN int
	for _, p := range in {
		out.Load1 += p.Load1
		out.MemUsed += p.MemUsed
		out.SwapUsed += p.SwapUsed
		out.ARC += p.ARC
		// Titik yang CPU-nya tak terhitung dilewati, bukan dihitung sebagai 0 —
		// kalau tidak, satu titik buta menyeret turun seluruh rata-rata.
		if p.CPU >= 0 {
			cpuSum += p.CPU
			cpuN++
		}
	}
	n := float64(len(in))
	out.Load1 = round1(out.Load1 / n)
	out.MemUsed = int64(float64(out.MemUsed) / n)
	out.SwapUsed = int64(float64(out.SwapUsed) / n)
	out.ARC = int64(float64(out.ARC) / n)
	if cpuN > 0 {
		out.CPU = round1(cpuSum / float64(cpuN))
	}
	out.Pools = meanMap(in, func(p Point) map[string]float64 { return p.Pools })
	out.Disks = meanMap(in, func(p Point) map[string]float64 { return p.Disks })
	return out
}

// meanMap merata-ratakan per kunci, dan HANYA atas titik yang benar-benar
// punya kunci itu. Pool yang baru muncul setengah jendela tidak boleh
// terlihat setengah penuh.
func meanMap(in []Point, pick func(Point) map[string]float64) map[string]float64 {
	sum := map[string]float64{}
	n := map[string]int{}
	for _, p := range in {
		for k, v := range pick(p) {
			sum[k] += v
			n[k]++
		}
	}
	if len(sum) == 0 {
		return nil
	}
	out := make(map[string]float64, len(sum))
	for k, v := range sum {
		out[k] = round1(v / float64(n[k]))
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
