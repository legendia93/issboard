package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/legendia93/issboard/internal/collector"
)

func TestAppendLapisHalusTerbatas(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 200 {
		f.Append(Point{T: base + int64(i)*60, Load1: float64(i)})
	}
	if len(f.Fine.Points) != FineMax {
		t.Fatalf("lapis halus: mau %d titik, dapat %d", FineMax, len(f.Fine.Points))
	}
	// Yang tersisa harus yang TERBARU, bukan yang terlama.
	if f.Fine.Points[len(f.Fine.Points)-1].Load1 != 199 {
		t.Errorf("titik terbaru terbuang: %v", f.Fine.Points[len(f.Fine.Points)-1])
	}
}

// Lapis kasar diisi tiap 30 menit, dan isinya RATA-RATA jendela — bukan
// cuplikan sesaat, supaya lonjakan tidak hilang dari grafik 24 jam.
func TestAppendLapisKasar(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 91 { // menit 0..90 -> batas terlewati di 0, 30, 60, 90
		f.Append(Point{
			T: base + int64(i)*60, Load1: float64(i), CPU: float64(i),
			Pools: map[string]float64{"kolam": float64(i)},
			Disks: map[string]float64{"/dev/sda": 40},
		})
	}
	if len(f.Coarse.Points) != 4 {
		t.Fatalf("lapis kasar: mau 4 titik, dapat %d", len(f.Coarse.Points))
	}
	for i := 1; i < len(f.Coarse.Points); i++ {
		if d := f.Coarse.Points[i].T - f.Coarse.Points[i-1].T; d != int64(CoarseStep.Seconds()) {
			t.Errorf("jarak titik kasar %d: mau %v, dapat %d", i, CoarseStep.Seconds(), d)
		}
	}
	// Titik kasar kedua meliputi menit 1..30.
	if got := f.Coarse.Points[1].Load1; got != 15.5 {
		t.Errorf("rata-rata jendela salah: mau 15.5, dapat %v", got)
	}
	if got := f.Coarse.Points[1].Pools["kolam"]; got != 15.5 {
		t.Errorf("rata-rata pool salah: %v", got)
	}
	if got := f.Coarse.Points[1].Disks["/dev/sda"]; got != 40 {
		t.Errorf("nilai konstan berubah saat dirata-rata: %v", got)
	}
}

func TestLapisKasarTerbatas(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 5000 {
		f.Append(Point{T: base + int64(i)*60, Load1: 1})
	}
	if len(f.Coarse.Points) != CoarseMax {
		t.Fatalf("lapis kasar meluber: %d titik", len(f.Coarse.Points))
	}
}

// 🔴 CPU -1 berarti TIDAK TERHITUNG, bukan 0%. Kalau ikut dirata-rata, satu
// titik buta menyeret turun seluruh jendela.
func TestRataRataLewatiCPUTakTerhitung(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 31 {
		cpu := 50.0
		if i%2 == 0 {
			cpu = -1
		}
		f.Append(Point{T: base + int64(i)*60, CPU: cpu})
	}
	if got := f.Coarse.Points[1].CPU; got != 50 {
		t.Errorf("mau 50 (titik -1 dilewati), dapat %v", got)
	}
}

func TestRataRataSemuaCPUTakTerhitung(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 31 {
		f.Append(Point{T: base + int64(i)*60, CPU: -1})
	}
	if got := f.Coarse.Points[1].CPU; got != -1 {
		t.Errorf("jendela tanpa satu pun nilai sah harus tetap -1, dapat %v", got)
	}
}

// Janji di docs: berkasnya tetap di bawah 20 KB berapa lama pun agent jalan.
func TestBerkasTetapKecil(t *testing.T) {
	var f File
	base := int64(1_700_000_000)
	for i := range 5000 {
		f.Append(Point{T: base + int64(i)*60, Load1: 1.5, MemUsed: 8 << 30,
			Pools: map[string]float64{"kolam": 62.5, "arsip": 41.2},
			Disks: map[string]float64{"/dev/sda": 38, "/dev/sdb": 41}})
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 20000 {
		t.Errorf("berkas %d byte, melewati janji < 20 KB", len(b))
	}
}

// Ditulis atomik lalu dibaca utuh — dan riwayat lama harus selamat, karena
// itulah alasan berkas ini ada dan bukan buffer di memori.
func TestSimpanMuatBolakBalik(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")

	var f File
	f.Append(Point{T: 1_700_000_000, Load1: 0.42, CPU: 17.5,
		Pools: map[string]float64{"kolam": 62.5}})
	f.CPUMark = &CPUMark{At: time.Now(), Total: 1000, Idle: 900}
	if err := Save(p, f); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fine.Points) != 1 || got.Fine.Points[0].Load1 != 0.42 {
		t.Errorf("titik tidak selamat: %+v", got.Fine.Points)
	}
	if got.CPUMark == nil || got.CPUMark.Total != 1000 {
		t.Errorf("cuplikan CPU tidak selamat: %+v", got.CPUMark)
	}
	if got.Fine.StepSeconds != int(FineStep.Seconds()) {
		t.Errorf("step_seconds tidak diisi: %d", got.Fine.StepSeconds)
	}
	// Tidak boleh meninggalkan berkas sementara.
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("ada sisa berkas sementara: %d entri", len(ents))
	}
}

// Berkas yang belum ada bukan error: mesin baru memang belum punya riwayat,
// dan agent harus tetap jalan supaya notifikasinya tidak ikut mati.
func TestMuatBerkasBelumAda(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "belum-ada.json"))
	if err != nil {
		t.Errorf("berkas belum ada tidak boleh jadi error: %v", err)
	}
	if f.Fine.StepSeconds == 0 {
		t.Error("buffer kosong harus tetap siap dipakai")
	}
}

// Berkas rusak dilaporkan, TAPI tetap mengembalikan buffer yang bisa dipakai:
// riwayat yang hilang tidak boleh ikut membungkam alert.
func TestMuatBerkasRusak(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rusak.json")
	os.WriteFile(p, []byte("{bukan json"), 0o644)

	f, err := Load(p)
	if err == nil {
		t.Error("berkas rusak harus dilaporkan")
	}
	if f.Fine.StepSeconds == 0 {
		t.Error("berkas rusak harus tetap menghasilkan buffer yang bisa dipakai")
	}
	f.Append(Point{T: 1})
	if len(f.Fine.Points) != 1 {
		t.Error("buffer hasil berkas rusak tidak bisa dipakai")
	}
}

// 🔴 Disk yang tidur TIDAK dicatat sama sekali. 0 °C bukan "dingin", itu
// "tidak terbaca" — dan menyimpannya sebagai angka akan menggambar jurang
// palsu di sparkline.
func TestPointFromLewatiDiskTidur(t *testing.T) {
	s := collector.Snapshot{
		CollectedAt: time.Unix(1_700_000_000, 0),
		Host: collector.Host{
			MemTotalBytes: 32 << 30, MemAvailBytes: 11 << 30,
			SwapTotal: 8 << 30, SwapFree: 8 << 30,
			ARCSizeBytes: 9 << 30, Load1: 0.42, CPUPercent: 17.44,
		},
		Pools: []collector.Pool{
			{Name: "kolam", SizeBytes: 1000, AllocBytes: 625},
			{Name: "kosong", SizeBytes: 0, AllocBytes: 0}, // pool tanpa ukuran
		},
		Smart: collector.SmartReport{Disks: []collector.SmartDisk{
			{Device: "/dev/sda", Temperature: 34},
			{Device: "/dev/sdb", Standby: true, Temperature: 0},
			{Device: "/dev/sdc", Temperature: 0}, // tidak tidur, tapi tak terbaca
		}},
	}
	p := PointFrom(s)

	if _, ada := p.Disks["/dev/sdb"]; ada {
		t.Error("disk tidur ikut tercatat")
	}
	if _, ada := p.Disks["/dev/sdc"]; ada {
		t.Error("suhu 0 (tak terbaca) ikut tercatat sebagai angka")
	}
	if p.Disks["/dev/sda"] != 34 {
		t.Errorf("suhu disk sehat salah: %v", p.Disks["/dev/sda"])
	}
	if _, ada := p.Pools["kosong"]; ada {
		t.Error("pool tanpa ukuran ikut tercatat")
	}
	if p.Pools["kolam"] != 62.5 {
		t.Errorf("persen pool salah: %v", p.Pools["kolam"])
	}
	if p.MemUsed != 21<<30 {
		t.Errorf("memori terpakai salah: %d", p.MemUsed)
	}
	if p.SwapUsed != 0 {
		t.Errorf("swap kosong harus 0, dapat %d", p.SwapUsed)
	}
	if p.CPU != 17.4 {
		t.Errorf("cpu harus dibulatkan 1 desimal: %v", p.CPU)
	}
}

// Riwayat demo harus memuat lubang yang disengaja — itu satu-satunya cara
// menggarap aturan "bolong digambar putus" tanpa menunggu agent mati.
func TestDemoFilePunyaLubang(t *testing.T) {
	f := DemoFile()
	if len(f.Coarse.Points) != CoarseMax {
		t.Errorf("lapis kasar demo: mau %d titik, dapat %d", CoarseMax, len(f.Coarse.Points))
	}
	lubang := 0
	for i := 1; i < len(f.Fine.Points); i++ {
		if f.Fine.Points[i].T-f.Fine.Points[i-1].T > 60 {
			lubang++
		}
	}
	if lubang != 1 {
		t.Errorf("mau tepat 1 lubang di riwayat demo, dapat %d", lubang)
	}
	// /dev/sde sedang tidur di data demo, jadi tidak boleh punya riwayat.
	for _, p := range f.Fine.Points {
		if _, ada := p.Disks["/dev/sde"]; ada {
			t.Fatal("disk tidur punya riwayat di data demo")
		}
	}
}
