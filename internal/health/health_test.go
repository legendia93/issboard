package health

import (
	"strings"
	"testing"
	"time"

	"github.com/legendia93/issboard/internal/collector"
)

func punyaKunci(fs []Finding, kunci string) *Finding {
	for i := range fs {
		if fs[i].Key == kunci {
			return &fs[i]
		}
	}
	return nil
}

// Data demo memang dibuat memuat setiap kondisi sakit, jadi ia sekaligus
// fixture terbaik untuk aturan vonis: kalau aturannya berubah diam-diam,
// test ini yang jatuh duluan.
func TestEvaluateDataDemo(t *testing.T) {
	fs := Evaluate(collector.DemoSnapshot())

	wajib := []string{
		"pool.stripe.pool-arsip", // stripe tanpa redundansi
		"pool.full.pool-arsip",   // 92% terpakai
		"pool.health.pool-uji",   // DEGRADED
		"pool.errors.pool-uji",   // checksum error
		"pool.scrub.pool-uji",    // belum pernah di-scrub
		"smart.realloc./dev/sdc", // 88 reallocated
		"smart.pending./dev/sdd", // pending sector
		"smart.temp./dev/sdd",    // 52 °C
		"ctr.down.pencadang",     // exited
		"ctr.nonet.pekerja",      // Up tanpa network
		"ctr.unhealthy.cache",    // healthcheck gagal
		"ctr.exposed.basis-data.0.0.0.0:5432->5432/tcp",
	}
	for _, k := range wajib {
		if punyaKunci(fs, k) == nil {
			t.Errorf("temuan %q hilang dari data demo", k)
		}
	}

	// Kritis harus di atas, supaya daftarnya tidak melompat antar refresh.
	lihatWarn := false
	for _, f := range fs {
		if f.Level == Warn {
			lihatWarn = true
		} else if f.Level == Crit && lihatWarn {
			t.Error("temuan kritis muncul setelah temuan perhatian")
		}
	}

	v := Summarize(fs)
	if v.Level != Crit || v.Crit == 0 {
		t.Errorf("vonis data demo harus kritis: %+v", v)
	}
}

// Host yang sehat harus benar-benar sunyi. Aturan yang menyala tanpa sebab
// membuat orang berhenti memercayai vonisnya.
func TestEvaluateHostSehatSunyi(t *testing.T) {
	s := collector.Snapshot{
		Pools: []collector.Pool{{
			Name: "kolam", Health: "ONLINE",
			SizeBytes: 1000, AllocBytes: 400, Mirrored: true,
			Devices:  []string{"a", "b"},
			ScanLine: "scrub repaired 0B in 01:00:00 with 0 errors on " + ctime(time.Now().Add(-3*24*time.Hour)),
		}},
		Containers: []collector.Container{{
			Name: "web", State: "running", Status: "Up 6 days",
			Networks: []string{"bridge"}, Health: "healthy",
			PublishedPorts: []string{"127.0.0.1:8080->80/tcp"},
		}},
		Smart: collector.SmartReport{
			WrittenAt: time.Now(),
			Disks: []collector.SmartDisk{
				{Device: "/dev/sda", Passed: true, Temperature: 34},
				{Device: "/dev/sdb", Passed: true, Standby: true}, // tidur: suhu 0
			},
		},
	}
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("host sehat harus tanpa temuan, dapat: %+v", fs)
	}
	if v := Summarize(nil); v.Level != OK {
		t.Errorf("vonis tanpa temuan harus ok, dapat %+v", v)
	}
}

// 🔴 Disk yang tidur melaporkan suhu 0. Kalau 0 dibaca sebagai angka, disk
// tidur akan terlihat dingin — dan yang lebih buruk, aturan suhu jadi tidak
// pernah bisa dipercaya.
func TestSuhuNolBukanDingin(t *testing.T) {
	s := collector.Snapshot{Smart: collector.SmartReport{
		WrittenAt: time.Now(),
		Disks:     []collector.SmartDisk{{Device: "/dev/sdb", Passed: true, Standby: true}},
	}}
	for _, f := range Evaluate(s) {
		if strings.HasPrefix(f.Key, "smart.temp.") {
			t.Errorf("disk tidur menghasilkan temuan suhu: %+v", f)
		}
	}
}

func TestAmbangPool(t *testing.T) {
	for _, c := range []struct {
		pakai int64
		mau   Level
	}{{70, ""}, {80, Warn}, {89, Warn}, {90, Crit}, {99, Crit}} {
		s := collector.Snapshot{Pools: []collector.Pool{{
			Name: "kolam", Health: "ONLINE", SizeBytes: 100, AllocBytes: c.pakai,
			Mirrored: true, Devices: []string{"a", "b"},
			ScanLine: "scrub repaired 0B in 01:00:00 with 0 errors on " + ctime(time.Now()),
		}}}
		f := punyaKunci(Evaluate(s), "pool.full.kolam")
		switch {
		case c.mau == "" && f != nil:
			t.Errorf("%d%% belum boleh jadi temuan: %+v", c.pakai, f)
		case c.mau != "" && f == nil:
			t.Errorf("%d%% harus jadi temuan %s", c.pakai, c.mau)
		case f != nil && f.Level != c.mau:
			t.Errorf("%d%%: mau %s, dapat %s", c.pakai, c.mau, f.Level)
		}
	}
}

// Baris scan: bentuknya bermacam-macam. Yang perlu dibedakan cuma tiga:
// belum pernah, sedang jalan, dan sudah lama.
func TestAturanScrub(t *testing.T) {
	buat := func(scan string) []Finding {
		return Evaluate(collector.Snapshot{Pools: []collector.Pool{{
			Name: "kolam", Health: "ONLINE", SizeBytes: 100, AllocBytes: 10,
			Mirrored: true, Devices: []string{"a", "b"}, ScanLine: scan,
		}}})
	}

	if punyaKunci(buat(""), "pool.scrub.kolam") == nil {
		t.Error("pool yang belum pernah di-scrub harus jadi temuan")
	}
	if punyaKunci(buat("none requested"), "pool.scrub.kolam") == nil {
		t.Error("'none requested' harus jadi temuan")
	}
	if f := punyaKunci(buat("scrub in progress since Fri Aug 21 08:00:00 2026"), "pool.scrub.kolam"); f != nil {
		t.Errorf("scrub yang SEDANG jalan bukan temuan: %+v", f)
	}
	baru := "scrub repaired 0B in 01:00:00 with 0 errors on " + ctime(time.Now().Add(-2*24*time.Hour))
	if f := punyaKunci(buat(baru), "pool.scrub.kolam"); f != nil {
		t.Errorf("scrub 2 hari lalu bukan temuan: %+v", f)
	}
	lama := "scrub repaired 0B in 01:00:00 with 0 errors on " + ctime(time.Now().Add(-60*24*time.Hour))
	if punyaKunci(buat(lama), "pool.scrub.kolam") == nil {
		t.Error("scrub 60 hari lalu harus jadi temuan")
	}
}

func TestScrubFinishedAt(t *testing.T) {
	// Format ctime(3) punya dua bentuk: tanggal satu digit diberi spasi ganda.
	for _, s := range []string{
		"scrub repaired 0B in 01:00:00 with 0 errors on Sun Aug  2 03:14:22 2026",
		"scrub repaired 0B in 01:00:00 with 0 errors on Sun Aug 16 03:14:22 2026",
	} {
		if _, ok := scrubFinishedAt(s); !ok {
			t.Errorf("stempel waktu tidak terbaca: %q", s)
		}
	}
	if _, ok := scrubFinishedAt("none requested"); ok {
		t.Error("'none requested' tidak punya stempel waktu")
	}
}

// Pool disk tunggal bukan stripe: tidak ada redundansi yang hilang, jadi
// tidak ada yang perlu diperingatkan.
func TestDiskTunggalBukanStripe(t *testing.T) {
	s := collector.Snapshot{Pools: []collector.Pool{{
		Name: "kolam", Health: "ONLINE", SizeBytes: 100, AllocBytes: 10,
		Devices: []string{"a"}, ScanLine: "scrub repaired 0B in 01:00:00 with 0 errors on " + ctime(time.Now()),
	}}}
	if f := punyaKunci(Evaluate(s), "pool.stripe.kolam"); f != nil {
		t.Errorf("disk tunggal salah dilaporkan sebagai stripe: %+v", f)
	}
}

// Container yang mati tidak boleh ikut dilaporkan "tanpa network": ia memang
// tidak punya, dan dua temuan untuk satu sebab cuma menambah kebisingan.
func TestContainerMatiTidakDobel(t *testing.T) {
	s := collector.Snapshot{Containers: []collector.Container{
		{Name: "pencadang", State: "exited", Status: "Exited (1)"},
	}}
	fs := Evaluate(s)
	if punyaKunci(fs, "ctr.down.pencadang") == nil {
		t.Error("container mati harus jadi temuan")
	}
	if f := punyaKunci(fs, "ctr.nonet.pencadang"); f != nil {
		t.Errorf("container mati tidak boleh dobel dengan 'tanpa network': %+v", f)
	}
}

// Kunci temuan dipakai fase 2 untuk de-duplikasi alert. Kalau ia berubah
// antar siklus untuk kondisi yang sama, notifikasinya berulang selamanya.
func TestKunciStabil(t *testing.T) {
	a := Evaluate(collector.DemoSnapshot())
	b := Evaluate(collector.DemoSnapshot())
	if len(a) != len(b) {
		t.Fatalf("jumlah temuan tidak stabil: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Key != b[i].Key {
			t.Errorf("kunci/urutan berubah di posisi %d: %q vs %q", i, a[i].Key, b[i].Key)
		}
	}
}

func ctime(t time.Time) string { return t.Format("Mon Jan _2 15:04:05 2006") }
