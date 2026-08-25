package collector

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// confNyata berbentuk persis seperti berkas kebijakan di mesin sungguhan:
// template yang dipakai ulang, satu bagian tanpa rekursi, dua dengan rekursi,
// dan retensi yang BERBEDA antar template — beda itulah yang membuat aturan
// "snapshot terlambat" tidak boleh memakai satu ambang tetap.
const confNyata = `
[kolam/arsip]
    use_template = arsip
[kolam/prod]
    use_template = prod
    recursive = zfs
[kolam/mandiri]
    use_template = harian
    recursive = zfs

[template_arsip]
    hourly = 0
    daily = 14
    monthly = 12
    autosnap = yes
    autoprune = yes

[template_prod]
    hourly = 48
    daily = 30
    monthly = 6
    autosnap = yes
    autoprune = yes

[template_harian]
    hourly = 0
    daily = 30
    monthly = 6
    autosnap = yes
    autoprune = yes
`

func TestForCakupanRekursif(t *testing.T) {
	p := parseSnapPolicy(confNyata)

	for _, c := range []struct {
		dataset string
		bagian  string // "" = tidak tercakup
	}{
		{"kolam/prod", "kolam/prod"},
		{"kolam/prod/db", "kolam/prod"},      // ikut lewat rekursi
		{"kolam/prod/db/lagi", "kolam/prod"}, // sedalam apa pun
		{"kolam/arsip", "kolam/arsip"},
		{"kolam/arsip/anak", ""}, // TANPA rekursi: anaknya tidak ikut
		{"kolam/mandiri/immich", "kolam/mandiri"},
		{"kolam", ""},          // wadah, tidak disebut di berkas
		{"kolam/media", ""},    // dataset baru: tidak ikut sendiri
		{"kolam/produksi", ""}, // awalan mirip, BUKAN keturunan
	} {
		got := p.For(c.dataset)
		switch {
		case c.bagian == "" && got != nil:
			t.Errorf("%s: mau tidak tercakup, dapat bagian %q", c.dataset, got.Section)
		case c.bagian != "" && got == nil:
			t.Errorf("%s: mau bagian %q, dapat tidak tercakup", c.dataset, c.bagian)
		case got != nil && got.Section != c.bagian:
			t.Errorf("%s: mau bagian %q, dapat %q", c.dataset, c.bagian, got.Section)
		}
	}
}

func TestForTemplateDiselesaikan(t *testing.T) {
	p := parseSnapPolicy(confNyata)

	prod := p.For("kolam/prod/db")
	if prod == nil || !prod.Autosnap || prod.Hourly != 48 || prod.Daily != 30 || prod.Monthly != 6 {
		t.Fatalf("template prod tidak terselesaikan: %+v", prod)
	}
	// Beda inilah yang penting: arsip TIDAK punya snapshot per jam, jadi
	// menilainya dengan ambang per jam akan menandai dataset yang sehat.
	arsip := p.For("kolam/arsip")
	if arsip == nil || arsip.Hourly != 0 || arsip.Daily != 14 {
		t.Fatalf("template arsip tidak terselesaikan: %+v", arsip)
	}
}

func TestBagianMenimpaTemplate(t *testing.T) {
	p := parseSnapPolicy(`
[kolam/khusus]
    use_template = prod
    hourly = 0
    autosnap = no
[template_prod]
    hourly = 48
    daily = 30
    autosnap = yes
`)
	// Baris yang ditulis khusus untuk satu dataset harus benar-benar berlaku
	// untuk dataset itu; kalau template menang, pengecualian yang ditulis orang
	// diabaikan diam-diam.
	got := p.For("kolam/khusus")
	if got == nil || got.Hourly != 0 || got.Autosnap || got.Daily != 30 {
		t.Fatalf("bagian tidak menimpa template: %+v", got)
	}
}

func TestBagianPalingSpesifikMenang(t *testing.T) {
	p := parseSnapPolicy(`
[kolam/prod]
    recursive = yes
    daily = 30
    autosnap = yes
[kolam/prod/scratch]
    daily = 0
    autosnap = no
`)
	got := p.For("kolam/prod/scratch")
	if got == nil || got.Section != "kolam/prod/scratch" || got.Autosnap {
		t.Fatalf("pengecualian yang lebih spesifik kalah: %+v", got)
	}
}

func TestProcessChildrenOnly(t *testing.T) {
	p := parseSnapPolicy(`
[kolam/app]
    recursive = yes
    process_children_only = yes
    daily = 30
    autosnap = yes
`)
	if got := p.For("kolam/app"); got != nil {
		t.Errorf("induk seharusnya sengaja tidak tercakup, dapat %+v", got)
	}
	if got := p.For("kolam/app/satu"); got == nil {
		t.Error("anaknya seharusnya tetap tercakup")
	}
}

func TestParseKomentarDanBarisLiar(t *testing.T) {
	p := parseSnapPolicy(`
# komentar di awal
daily = 999          ; baris tanpa bagian — tidak punya pemilik

[kolam/app]   # komentar di ujung
    daily = 30   ; sisa hari
    autosnap = yes
`)
	got := p.For("kolam/app")
	if got == nil || got.Daily != 30 || !got.Autosnap {
		t.Fatalf("komentar ikut terbaca sebagai nilai: %+v", got)
	}
}

// 🔴 Penjaga yang paling penting di paket ini. Berkas yang tidak ada berarti
// mesin ini tidak memakai snapshot terkelola — BUKAN berarti seluruh
// datasetnya tidak terlindungi. Kalau Present ikut true untuk berkas yang
// hilang, mesin tanpa sanoid akan menyalakan satu temuan per dataset di menit
// pertama, dan sesudah itu daftar temuannya berhenti dibaca orang.
func TestBerkasHilangBukanError(t *testing.T) {
	p, err := LoadSnapPolicy("/tidak/ada/sanoid.conf")
	if err != nil {
		t.Fatalf("berkas hilang tidak boleh jadi error: %v", err)
	}
	if p.Present {
		t.Error("berkas hilang tidak boleh menghasilkan Present=true")
	}
	if p.For("kolam/apa pun") != nil {
		t.Error("set kosong tidak boleh mencakup apa pun")
	}
}

func TestPathKosongMematikanFitur(t *testing.T) {
	p, err := LoadSnapPolicy("")
	if err != nil || p.Present {
		t.Errorf("path kosong: mau set kosong tanpa error, dapat %+v / %v", p, err)
	}
}

func TestSnapExempt(t *testing.T) {
	pat := []string{"kolam/rekaman/*", "kolam/scratch", "  "}
	for _, c := range []struct {
		dataset string
		mau     bool
	}{
		{"kolam/rekaman", true}, // `/*` ikut mencakup induknya
		{"kolam/rekaman/cam1", true},
		{"kolam/scratch", true},
		{"kolam/scratch/anak", false}, // tanpa `/*`: hanya dataset itu sendiri
		{"kolam/rekamanku", false},    // awalan mirip, bukan keturunan
		{"kolam/app", false},
	} {
		if got := snapExempt(c.dataset, pat); got != c.mau {
			t.Errorf("%s: mau %v, dapat %v", c.dataset, c.mau, got)
		}
	}
}

// 🔴 Dikunci test karena pernah salah: `omitempty` TIDAK berlaku untuk struct,
// jadi time.Time yang nol tetap terkirim sebagai "0001-01-01T00:00:00Z".
// Penerima yang cuma memeriksa "ada isinya atau tidak" lalu melaporkan umur
// snapshot dalam ratusan ribu hari — ketiadaan menyamar jadi nilai, kesalahan
// yang sama dengan membaca suhu 0 sebagai dingin.
func TestDatasetTanpaSnapshotTidakMengirimTanggalPalsu(t *testing.T) {
	b, err := json.Marshal(Dataset{Name: "kolam/baru"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "last_snapshot") {
		t.Errorf("dataset tanpa snapshot tidak boleh mengirim last_snapshot sama sekali: %s", b)
	}

	at := time.Unix(1787600000, 0)
	b, _ = json.Marshal(Dataset{Name: "kolam/app", LastSnapshot: &at})
	if !strings.Contains(string(b), "last_snapshot") {
		t.Errorf("dataset dengan snapshot harus mengirim last_snapshot: %s", b)
	}
}
