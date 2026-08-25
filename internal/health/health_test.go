package health

import (
	"fmt"
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
		"ctr.exposed.basis-data.*:5432->5432/tcp",
		"snap.uncovered.pool-cepat/media",  // 84 GiB tanpa kebijakan apa pun
		"snap.stale.pool-cepat/basis-data", // per jam, terakhir 5 jam lalu
		"snap.never.pool-uji/coba",         // tercakup, autosnap menyala, nol snapshot
	}

	// Dan yang justru TIDAK boleh muncul: dataset wadah yang kecil, dataset
	// yang dikecualikan dengan sadar, serta dataset yang snapshot-nya segar.
	// Aturan yang cuma diuji lawan keadaan sakit tidak pernah ketahuan
	// menyala untuk keadaan sehat.
	haram := []string{
		"snap.uncovered.pool-cepat",
		"snap.uncovered.pool-arsip/rekaman",
		"snap.stale.pool-cepat/app",
	}
	for _, k := range haram {
		if f := punyaKunci(fs, k); f != nil {
			t.Errorf("temuan %q tidak boleh ada: %s", k, f.Detail)
		}
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

// 🔴 Mem-publish port ke semua alamat adalah cara aplikasi web dijangkau —
// itu tujuannya, bukan kecelakaan. Aturan yang menyala untuk keadaan normal
// melatih orang mengabaikan seluruh daftar temuan.
func TestPortBiasaBukanTemuan(t *testing.T) {
	s := collector.Snapshot{Containers: []collector.Container{{
		Name: "web", State: "running", Status: "Up 6 days", Networks: []string{"bridge"},
		PublishedPorts: []string{
			"*:3000->3000/tcp", "*:8080->80/tcp", "*:1935->1935/tcp", "*:9443->9443/tcp",
		},
	}}}
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("port aplikasi biasa tidak boleh jadi temuan: %+v", fs)
	}
}

// Yang tersisa di daftar sensitif adalah layanan yang biasanya mengandalkan
// jaringan sebagai pembatas, bukan autentikasinya sendiri.
func TestPortSensitifJadiTemuan(t *testing.T) {
	for _, c := range []struct {
		port int
		mau  Level
	}{
		{5432, Warn}, {3306, Warn}, {6379, Warn}, {27017, Warn},
		{2375, Crit}, {23, Crit}, {10250, Crit},
	} {
		p := fmt.Sprintf("*:%d->%d/tcp", c.port, c.port)
		s := collector.Snapshot{Containers: []collector.Container{{
			Name: "x", State: "running", Status: "Up", Networks: []string{"bridge"},
			PublishedPorts: []string{p},
		}}}
		fs := Evaluate(s)
		if len(fs) != 1 {
			t.Errorf("port %d: mau 1 temuan, dapat %+v", c.port, fs)
			continue
		}
		if fs[0].Level != c.mau {
			t.Errorf("port %d: mau %s, dapat %s", c.port, c.mau, fs[0].Level)
		}
	}
}

func TestPublicPort(t *testing.T) {
	for _, c := range []struct {
		in  string
		mau int
	}{
		{"*:5432->5432/tcp", 5432},
		{"10.0.0.5:8080->80/tcp", 8080},
		{"*:8890->8890/udp", 8890},
		{"bukan-port", 0},
		{"*:abc->80/tcp", 0},
	} {
		if got := publicPort(c.in); got != c.mau {
			t.Errorf("%q: mau %d, dapat %d", c.in, c.mau, got)
		}
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
			PublishedPorts: []string{"*:8080->80/tcp"},
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

// 🔴 Disk yang tidur tidak melaporkan APA PUN — termasuk `passed`, yang
// kosongnya berarti false. Di server sungguhan ini memunculkan alarm KRITIS
// palsu "SMART gagal" pada menit pertama.
func TestDiskTidurTidakPernahJadiTemuan(t *testing.T) {
	s := collector.Snapshot{Smart: collector.SmartReport{
		WrittenAt: time.Now(),
		Disks: []collector.SmartDisk{{
			Device: "/dev/sda", Standby: true,
			Passed: false, Temperature: 0, Reallocated: 0, PendingSect: 0,
		}},
	}}
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("disk tidur tidak boleh menghasilkan temuan apa pun: %+v", fs)
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

// --- Kebijakan snapshot ------------------------------------------------

const gib = int64(1) << 30

func waktu(t time.Time) *time.Time { return &t }

func hourly() *collector.SnapPolicy {
	return &collector.SnapPolicy{Section: "kolam/prod", Autosnap: true, Hourly: 48, Daily: 30}
}
func harian() *collector.SnapPolicy {
	return &collector.SnapPolicy{Section: "kolam/self", Autosnap: true, Daily: 30, Monthly: 6}
}

func snapSnapshot(ds ...collector.Dataset) collector.Snapshot {
	return collector.Snapshot{
		SnapPolicy: collector.SnapPolicySet{Source: "/etc/sanoid/sanoid.conf", Present: true},
		Datasets:   ds,
	}
}

// 🔴 Aturan baru harus diuji terhadap mesin yang SEHAT, bukan cuma terhadap
// mesin yang sakit — pelajaran termahal dari pemasangan pertama.
//
// Ambang tetap "6 jam" akan menandai SETIAP dataset di host yang kebijakannya
// memang harian. Di mesin nyata itu enam temuan palsu sekaligus, semuanya
// untuk dataset yang bekerja persis seperti seharusnya.
func TestSnapshotSehatSunyi(t *testing.T) {
	s := snapSnapshot(
		// Per jam, baru 45 menit lalu.
		collector.Dataset{Name: "kolam/prod/db", UsedByDataset: 4 * gib,
			LastSnapshot: waktu(time.Now().Add(-45 * time.Minute)), SnapPolicy: hourly()},
		// HARIAN, 9,7 jam lalu — persis keadaan yang akan ditandai salah oleh
		// ambang tetap, dan sama sekali tidak bermasalah.
		collector.Dataset{Name: "kolam/self/immich", UsedByDataset: 63 * gib,
			LastSnapshot: waktu(time.Now().Add(-9*time.Hour - 42*time.Minute)), SnapPolicy: harian()},
		// Wadah: tidak tercakup, isinya sendiri nyaris nol.
		collector.Dataset{Name: "kolam", UsedByDataset: 112 * 1024},
		collector.Dataset{Name: "kolam/prod", UsedByDataset: 122 * 1024},
		// Tidak tercakup tapi dikecualikan dengan sadar.
		collector.Dataset{Name: "kolam/rekaman", UsedByDataset: 900 * gib, SnapExempt: true},
		// Tercakup dengan autosnap dimatikan: itu keputusan yang sudah diambil
		// orang, tertulis di berkasnya. Menanyakannya lagi cuma berisik.
		collector.Dataset{Name: "kolam/scratch", UsedByDataset: 50 * gib,
			SnapPolicy: &collector.SnapPolicy{Section: "kolam/scratch", Autosnap: false}},
	)
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("kebijakan snapshot yang sehat harus sunyi, dapat: %+v", fs)
	}
}

// 🔴 Penjaga kedua: mesin tanpa berkas kebijakan sama sekali. Ketiadaan data
// bukan data buruk. Tanpa ini, tiap host yang tidak memakai snapshot terkelola
// menyalakan satu temuan per dataset di menit pertama.
func TestTanpaBerkasKebijakanSeluruhAturanDiam(t *testing.T) {
	s := collector.Snapshot{
		SnapPolicy: collector.SnapPolicySet{Present: false},
		Datasets: []collector.Dataset{
			{Name: "kolam/media", UsedByDataset: 84 * gib},
			{Name: "kolam/data", UsedByDataset: 500 * gib},
		},
	}
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("tanpa berkas kebijakan harus diam, dapat: %+v", fs)
	}
}

func TestDatasetTidakTercakup(t *testing.T) {
	s := snapSnapshot(collector.Dataset{Name: "kolam/media", UsedByDataset: 84 * gib,
		SnapshotCount: 2, LastSnapshot: waktu(time.Now().Add(-11 * 24 * time.Hour))})

	f := punyaKunci(Evaluate(s), "snap.uncovered.kolam/media")
	if f == nil {
		t.Fatal("dataset berisi 84 GiB tanpa kebijakan apa pun harus jadi temuan")
	}
	if f.Level != Warn {
		t.Errorf("mau warn, dapat %s", f.Level)
	}
	// Ukurannya harus ikut tertulis: "tidak tercakup" tanpa angka tidak
	// memberi tahu apakah yang hilang itu 84 GiB atau 84 KiB.
	if !strings.Contains(f.Detail, "84.0 GiB") {
		t.Errorf("detail tidak menyebut ukurannya: %q", f.Detail)
	}
}

// Dataset kecil yang tidak tercakup tidak boleh jadi temuan: separuh daftar
// akan berisi wadah kosong, dan daftar semacam itu berhenti dibaca.
func TestDatasetKecilTidakTercakupBukanTemuan(t *testing.T) {
	s := snapSnapshot(collector.Dataset{Name: "kolam/kosong", UsedByDataset: 900 * (1 << 20)})
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("dataset di bawah ambang harus diam, dapat: %+v", fs)
	}
}

// Nol snapshot pada dataset yang berkasnya sendiri mengklaim di-snapshot
// bukan "ketiadaan data" — itu kontradiksi antara yang tertulis dan yang
// terjadi. Persis bentuk kegagalan yang sudah pernah terjadi: timer `active`,
// hasil nol.
func TestTercakupTapiBelumPernahTerSnapshot(t *testing.T) {
	s := snapSnapshot(collector.Dataset{Name: "kolam/prod/baru", UsedByDataset: 2 * gib,
		SnapshotCount: 0, SnapPolicy: hourly()})

	f := punyaKunci(Evaluate(s), "snap.never.kolam/prod/baru")
	if f == nil || f.Level != Crit {
		t.Fatalf("mau temuan kritis, dapat %+v", f)
	}
}

func TestSnapshotTerlambatDanBerhenti(t *testing.T) {
	for _, c := range []struct {
		nama  string
		umur  time.Duration
		pol   *collector.SnapPolicy
		kunci string
		level Level
	}{
		{"per jam, 45 menit", 45 * time.Minute, hourly(), "", ""},
		{"per jam, 2 jam masih dalam kelonggaran", 2 * time.Hour, hourly(), "", ""},
		{"per jam, 5 jam", 5 * time.Hour, hourly(), "snap.stale.kolam/x", Warn},
		{"per jam, 2 hari", 48 * time.Hour, hourly(), "snap.stale.kolam/x", Crit},
		{"harian, 9 jam", 9 * time.Hour, harian(), "", ""},
		{"harian, 2 hari masih dalam kelonggaran", 48 * time.Hour, harian(), "", ""},
		{"harian, 4 hari", 4 * 24 * time.Hour, harian(), "snap.stale.kolam/x", Warn},
		{"harian, 30 hari", 30 * 24 * time.Hour, harian(), "snap.stale.kolam/x", Crit},
	} {
		s := snapSnapshot(collector.Dataset{Name: "kolam/x", UsedByDataset: 2 * gib,
			SnapshotCount: 5, LastSnapshot: waktu(time.Now().Add(-c.umur)), SnapPolicy: c.pol})
		fs := Evaluate(s)

		if c.kunci == "" {
			if len(fs) != 0 {
				t.Errorf("%s: harus diam, dapat %+v", c.nama, fs)
			}
			continue
		}
		f := punyaKunci(fs, c.kunci)
		if f == nil {
			t.Errorf("%s: temuan %q hilang", c.nama, c.kunci)
			continue
		}
		if f.Level != c.level {
			t.Errorf("%s: mau %s, dapat %s", c.nama, c.level, f.Level)
		}
	}
}

// Autosnap menyala tanpa satu pun angka retensi berarti tidak ada yang bisa
// disimpulkan soal "seharusnya tiap berapa lama". Menebaknya sendiri di situ
// berarti mengarang harapan yang tidak pernah ditulis siapa pun.
func TestAutosnapTanpaRetensiTidakDitebak(t *testing.T) {
	s := snapSnapshot(collector.Dataset{Name: "kolam/x", UsedByDataset: 2 * gib,
		SnapshotCount: 0,
		SnapPolicy:    &collector.SnapPolicy{Section: "kolam", Autosnap: true}})
	if fs := Evaluate(s); len(fs) != 0 {
		t.Errorf("harus diam, dapat %+v", fs)
	}
}

func TestRoundAge(t *testing.T) {
	for _, c := range []struct {
		d   time.Duration
		mau string
	}{
		{30 * time.Minute, "30 menit"},
		{90 * time.Minute, "1 jam"},
		{5 * time.Hour, "5 jam"},
		{47 * time.Hour, "47 jam"},
		{11 * 24 * time.Hour, "11 hari"},
	} {
		if got := roundAge(c.d); got != c.mau {
			t.Errorf("%v: mau %q, dapat %q", c.d, c.mau, got)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for _, c := range []struct {
		n   int64
		mau string
	}{
		{512, "512 B"},
		{112 * 1024, "112.0 KiB"},
		{84 * gib, "84.0 GiB"},
	} {
		if got := humanBytes(c.n); got != c.mau {
			t.Errorf("%d: mau %q, dapat %q", c.n, c.mau, got)
		}
	}
}
