package alert

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/legendia93/issboard/internal/health"
)

var t0 = time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)

func opsi(now time.Time) Options {
	return Options{Now: now, MinLevel: health.Warn, Repeat: 24 * time.Hour, Settled: true}
}

func temuan(key string, lvl health.Level) health.Finding {
	return health.Finding{Level: lvl, Key: key, Title: "Judul " + key, Detail: "rincian"}
}

// Satu siklus penuh: temuan baru -> terkirim -> siklus berikutnya SEPI.
// Inilah alasan seluruh paket ini ada.
func TestDedupSiklusKedua(t *testing.T) {
	st := State{Active: map[string]Entry{}}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}

	d := st.Plan(fs, opsi(t0))
	if len(d.New) != 1 || !d.FirstRun {
		t.Fatalf("siklus pertama salah: %+v", d)
	}
	st.MarkSent(d, t0)
	st.Observe(fs, opsi(t0))
	st.UpdatedAt = t0 // seolah sudah tersimpan ke disk

	d2 := st.Plan(fs, opsi(t0.Add(time.Minute)))
	if d2.Any() {
		t.Errorf("siklus kedua harus sepi, dapat: %+v", d2)
	}
}

// Naik tingkat selalu dikabari ulang, seberapa pun barunya pesan terakhir.
func TestNaikTingkatMenembusDedup(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	warn := []health.Finding{temuan("pool.full.kolam", health.Warn)}
	d := st.Plan(warn, opsi(t0))
	st.MarkSent(d, t0)
	st.Observe(warn, opsi(t0))

	crit := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	d2 := st.Plan(crit, opsi(t0.Add(time.Minute)))
	if len(d2.Worse) != 1 {
		t.Fatalf("kenaikan warn->crit harus menembus dedup: %+v", d2)
	}
	if !d2.Urgent() {
		t.Error("temuan yang memburuk jadi kritis harus mendesak")
	}

	// Turun lagi ke warn TIDAK dikabari: keadaannya membaik, dan pesan untuk
	// setiap gerakan kecil adalah cara tercepat membuat orang mematikan alert.
	st.MarkSent(d2, t0.Add(time.Minute))
	st.Observe(crit, opsi(t0.Add(time.Minute)))
	d3 := st.Plan(warn, opsi(t0.Add(2*time.Minute)))
	if d3.Any() {
		t.Errorf("turun tingkat tidak perlu dikabari: %+v", d3)
	}
}

func TestPengingatSetelahJedaDiam(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("smart.realloc./dev/sdc", health.Warn)}
	d := st.Plan(fs, opsi(t0))
	st.MarkSent(d, t0)
	st.Observe(fs, opsi(t0))

	if got := st.Plan(fs, opsi(t0.Add(23*time.Hour))); got.Any() {
		t.Errorf("belum 24 jam, belum boleh mengingatkan: %+v", got)
	}
	got := st.Plan(fs, opsi(t0.Add(25*time.Hour)))
	if len(got.Reminder) != 1 {
		t.Errorf("setelah 24 jam harus mengingatkan: %+v", got)
	}
	// Pengingat tidak pernah mendesak, walau temuannya kritis.
	if got.Urgent() {
		t.Error("pengingat tidak boleh membangunkan orang")
	}
}

// 🔴 Kiriman yang GAGAL tidak boleh menghapus jejaknya: siklus berikutnya
// harus mencoba lagi. Alert yang hilang karena jaringan sedang putus adalah
// kegagalan paling mahal dari sistem seperti ini.
func TestKirimGagalDicobaLagi(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}

	if d := st.Plan(fs, opsi(t0)); len(d.New) != 1 {
		t.Fatalf("siklus pertama harus punya temuan baru: %+v", d)
	}
	// MarkSent SENGAJA tidak dipanggil: anggap pengirimannya gagal.
	st.Observe(fs, opsi(t0))

	d2 := st.Plan(fs, opsi(t0.Add(time.Minute)))
	if len(d2.New) != 1 {
		t.Errorf("temuan yang gagal terkirim harus dicoba lagi: %+v", d2)
	}
	// Umurnya tetap dihitung sejak pertama terlihat, bukan sejak terkirim.
	if !d2.New[0].Since.Equal(t0) {
		t.Errorf("umur temuan salah: %v", d2.New[0].Since)
	}
}

func TestKabarPulih(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	d := st.Plan(fs, opsi(t0))
	st.MarkSent(d, t0)
	st.Observe(fs, opsi(t0))

	d2 := st.Plan(nil, opsi(t0.Add(time.Minute)))
	if len(d2.Recovered) != 1 {
		t.Fatalf("temuan yang hilang harus dikabarkan pulih: %+v", d2)
	}
	if d2.Urgent() {
		t.Error("kabar pulih tidak boleh mendesak")
	}
	st.MarkSent(d2, t0.Add(time.Minute))
	st.Observe(nil, opsi(t0.Add(time.Minute)))
	if len(st.Active) != 0 {
		t.Errorf("ingatan harus bersih setelah kabar pulih terkirim: %+v", st.Active)
	}
}

// 🔴 Kabar pulih yang gagal terkirim juga harus dicoba lagi — kalau entrinya
// langsung dihapus, orang tetap mengira masalahnya masih ada selamanya.
func TestKabarPulihGagalDitahan(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	d := st.Plan(fs, opsi(t0))
	st.MarkSent(d, t0)
	st.Observe(fs, opsi(t0))

	// Temuan hilang, tapi pengiriman gagal: MarkSent tidak dipanggil.
	d2 := st.Plan(nil, opsi(t0.Add(time.Minute)))
	if len(d2.Recovered) != 1 {
		t.Fatal("kabar pulih tidak muncul")
	}
	st.Observe(nil, opsi(t0.Add(time.Minute)))
	if len(st.Active) != 1 {
		t.Fatalf("entri harus DITAHAN sampai kabar pulihnya sampai: %+v", st.Active)
	}

	d3 := st.Plan(nil, opsi(t0.Add(2*time.Minute)))
	if len(d3.Recovered) != 1 {
		t.Errorf("kabar pulih harus dicoba lagi: %+v", d3)
	}
	st.MarkSent(d3, t0.Add(2*time.Minute))
	st.Observe(nil, opsi(t0.Add(2*time.Minute)))
	if len(st.Active) != 0 {
		t.Errorf("setelah terkirim, ingatan harus bersih: %+v", st.Active)
	}
}

// Entri yang menggantung tidak boleh menumpuk selamanya kalau memang tidak
// ada kanal yang bisa menerima kabar pulihnya.
func TestEntriMenggantungAdaBatasnya(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	st.MarkSent(st.Plan(fs, opsi(t0)), t0)
	st.Observe(fs, opsi(t0))

	st.Observe(nil, opsi(t0.Add(8*24*time.Hour)))
	if len(st.Active) != 0 {
		t.Errorf("entri berumur 8 hari harus dilepas: %+v", st.Active)
	}
}

// 🔴 Pengumpulan yang gagal TIDAK boleh terbaca sebagai kesembuhan. `zpool`
// yang mati membuat semua temuan pool lenyap dari daftar — mengabarkannya
// pulih adalah kebalikan persis dari keadaan sebenarnya.
func TestPengumpulanGagalTidakPernahPulih(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	st.MarkSent(st.Plan(fs, opsi(t0)), t0)
	st.Observe(fs, opsi(t0))

	o := opsi(t0.Add(time.Minute))
	o.Settled = false // pengumpulan tidak lengkap

	if d := st.Plan(nil, o); len(d.Recovered) != 0 {
		t.Errorf("siklus dengan error tidak boleh melaporkan pulih: %+v", d)
	}
	st.Observe(nil, o)
	if len(st.Active) != 1 {
		t.Errorf("ingatan harus ditahan saat pengumpulan gagal: %+v", st.Active)
	}
}

// Temuan yang belum pernah dikabari tidak perlu diumumkan pulih: orangnya
// belum pernah tahu ada masalahnya.
func TestPulihTanpaPernahDikabari(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	st.Observe(fs, opsi(t0)) // terlihat, tapi tidak pernah terkirim

	d := st.Plan(nil, opsi(t0.Add(time.Minute)))
	if len(d.Recovered) != 0 {
		t.Errorf("tidak perlu mengabari pulih untuk yang belum pernah dikabari: %+v", d)
	}
}

func TestMinLevelMenyaring(t *testing.T) {
	st := State{Active: map[string]Entry{}, UpdatedAt: t0}
	fs := []health.Finding{
		temuan("pool.stripe.kolam", health.Warn),
		temuan("pool.full.kolam", health.Crit),
	}
	o := opsi(t0)
	o.MinLevel = health.Crit

	d := st.Plan(fs, o)
	if len(d.New) != 1 || d.New[0].Level != health.Crit {
		t.Errorf("notify_min_level crit harus menyaring warn: %+v", d.New)
	}
	st.Observe(fs, o)
	if len(st.Active) != 1 {
		t.Errorf("yang disaring tidak boleh ikut diingat: %+v", st.Active)
	}
}

// Salah ketik di config sebaiknya membuat alert terlalu ramai, bukan
// diam-diam membisukan setengah temuan.
func TestParseLevel(t *testing.T) {
	for _, c := range []struct {
		in  string
		mau health.Level
	}{
		{"crit", health.Crit}, {"CRIT", health.Crit}, {"kritis", health.Crit},
		{"warn", health.Warn}, {"", health.Warn}, {"salah-ketik", health.Warn},
	} {
		if got := ParseLevel(c.in); got != c.mau {
			t.Errorf("%q: mau %s, dapat %s", c.in, c.mau, got)
		}
	}
}

func TestSimpanMuatState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "alert-state.json")

	st := State{Active: map[string]Entry{}}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}
	st.MarkSent(st.Plan(fs, opsi(t0)), t0)
	st.Observe(fs, opsi(t0))
	if err := Save(p, st); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Active) != 1 {
		t.Fatalf("ingatan tidak selamat: %+v", got.Active)
	}
	// State yang sudah pernah ditulis bukan lagi siklus pertama — kalau tidak,
	// tiap reboot akan mengirim ulang seluruh kondisi sebagai "ringkasan awal".
	if d := got.Plan(fs, opsi(t0.Add(time.Minute))); d.FirstRun {
		t.Error("state yang sudah tersimpan tidak boleh dianggap siklus pertama")
	}
}

func TestMuatStateBelumAda(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "belum-ada.json"))
	if err != nil {
		t.Errorf("state belum ada bukan error: %v", err)
	}
	if !st.Plan(nil, opsi(t0)).FirstRun {
		t.Error("mesin baru harus terbaca sebagai siklus pertama")
	}
}

// Pesan siklus pertama diberi bingkai "kondisi saat ini": di host yang sudah
// lama jalan, temuannya bisa berumur berbulan-bulan.
func TestComposeSiklusPertama(t *testing.T) {
	st := State{Active: map[string]Entry{}}
	fs := []health.Finding{
		temuan("pool.full.kolam", health.Crit),
		temuan("smart.realloc./dev/sdc", health.Warn),
	}
	judul, badan := Compose(st.Plan(fs, opsi(t0)), "kotak", t0)

	if !strings.Contains(judul, "ringkasan awal") {
		t.Errorf("judul siklus pertama salah: %q", judul)
	}
	if !strings.Contains(badan, "KONDISI SAAT INI") {
		t.Errorf("bingkai siklus pertama hilang: %q", badan)
	}
	if !strings.Contains(badan, "[KRITIS]") || !strings.Contains(badan, "[PERHATIAN]") {
		t.Errorf("label tingkat hilang: %q", badan)
	}
	// Umur tidak disebut untuk temuan yang baru terlihat siklus ini.
	if strings.Contains(badan, "sejak") {
		t.Errorf("umur 0 tidak perlu disebut: %q", badan)
	}
}

func TestComposeUmurDisebutKalauLama(t *testing.T) {
	st := State{Active: map[string]Entry{
		"pool.full.kolam": {Level: health.Crit, Title: "Pool hampir penuh",
			FirstSeen: t0.Add(-50 * time.Hour), LastSeen: t0,
			NotifiedAt: t0.Add(-50 * time.Hour), NotifiedLevel: health.Crit},
	}, UpdatedAt: t0}
	fs := []health.Finding{temuan("pool.full.kolam", health.Crit)}

	_, badan := Compose(st.Plan(fs, opsi(t0)), "kotak", t0)
	if !strings.Contains(badan, "hari lalu") {
		t.Errorf("umur temuan lama harus disebut: %q", badan)
	}
}

// Pesan yang ditolak karena kepanjangan adalah kegagalan diam — persis jenis
// yang proyek ini ada untuk mencegahnya.
func TestComposeDipotongKalauPanjang(t *testing.T) {
	st := State{Active: map[string]Entry{}}
	var fs []health.Finding
	for i := range 200 {
		f := temuan(string(rune('a'+i%26))+strings.Repeat("x", 40)+string(rune(i)), health.Warn)
		f.Detail = strings.Repeat("rincian panjang ", 12)
		fs = append(fs, f)
	}
	_, badan := Compose(st.Plan(fs, opsi(t0)), "kotak", t0)
	if len(badan) > maxBody {
		t.Errorf("badan pesan %d byte, melewati batas %d", len(badan), maxBody)
	}
	if !strings.Contains(badan, "tidak dimuat") {
		t.Error("sisa yang tidak dimuat harus disebutkan, bukan dibuang diam-diam")
	}
}

// Temuan yang pulih membawa tingkat LAMANYA. Menuliskannya apa adanya
// menghasilkan "[KRITIS] ..." di bawah judul PULIH — kebalikan dari kabarnya.
func TestComposePulihTidakBerlabelKritis(t *testing.T) {
	st := State{Active: map[string]Entry{
		"pool.full.kolam": {Level: health.Crit, Title: "Pool hampir penuh",
			Detail: "kolam terpakai 92%", FirstSeen: t0.Add(-time.Hour),
			LastSeen: t0, NotifiedAt: t0.Add(-time.Hour), NotifiedLevel: health.Crit},
	}, UpdatedAt: t0}

	judul, badan := Compose(st.Plan(nil, opsi(t0)), "kotak", t0)
	if strings.Contains(badan, "[KRITIS]") {
		t.Errorf("kabar pulih tidak boleh berlabel kritis: %q", badan)
	}
	if !strings.Contains(badan, "[PULIH]") || !strings.Contains(badan, "sebelumnya:") {
		t.Errorf("bagian pulih salah bentuk: %q", badan)
	}
	if !strings.Contains(judul, "pulih") {
		t.Errorf("judul tidak menyebut pulih: %q", judul)
	}
}

func TestComposeHostKosong(t *testing.T) {
	st := State{Active: map[string]Entry{}}
	judul, _ := Compose(st.Plan([]health.Finding{temuan("a", health.Warn)}, opsi(t0)), "", t0)
	if strings.Contains(judul, "  ") || !strings.Contains(judul, "issboard") {
		t.Errorf("judul tanpa hostname salah bentuk: %q", judul)
	}
}
