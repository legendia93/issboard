package collector

import (
	"os"
	"strconv"
	"strings"
)

// Kebijakan snapshot: membandingkan apa yang DIKLAIM berkas konfigurasi
// dengan dataset yang benar-benar ada di sistem.
//
// Ini menjawab dua pertanyaan yang tidak dijawab `zfs list` maupun UI mana
// pun, dan dua-duanya diam kalau salah:
//
//  1. Dataset mana yang TIDAK tercakup kebijakan apa pun. Dataset baru tidak
//     otomatis masuk; yang membuatnya berbahaya adalah tidak ada satu pun
//     pesan saat itu terjadi. Ia terlihat persis sama dengan dataset yang
//     terlindungi, sampai hari orang membutuhkan snapshot-nya.
//  2. Dataset yang tercakup tapi snapshot terakhirnya sudah basi. Timer bisa
//     saja `active` sementara tidak menghasilkan apa-apa sama sekali.
//
// Formatnya format sanoid — INI file dengan bagian per-dataset dan template
// yang bisa dipakai ulang. Parsernya ditulis sendiri karena alasan yang sama
// dengan config: satu binary statis tanpa dependensi (docs/design.md §3.1).
//
// ⚠️ Yang dibaca hanya berkasnya, bukan sanoid-nya. issboard tidak pernah
// memanggil sanoid, tidak pernah membuat snapshot, dan tidak peduli apakah
// sanoid benar-benar terpasang. Yang dibandingkan adalah "apa yang tertulis"
// lawan "apa yang ada di ZFS".

// SnapPolicy adalah aturan yang berlaku untuk satu dataset, hasil pencocokan
// dengan berkas kebijakan — template sudah ikut diselesaikan.
type SnapPolicy struct {
	// Section adalah bagian di berkas yang mencakup dataset ini. Untuk dataset
	// yang tercakup lewat pewarisan, isinya nama LELUHURNYA — dan itu memang
	// yang ingin dilihat orang saat bertanya "kenapa dataset ini ikut?".
	Section  string `json:"section"`
	Template string `json:"template,omitempty"`
	Autosnap bool   `json:"autosnap"`
	Hourly   int    `json:"hourly"`
	Daily    int    `json:"daily"`
	Monthly  int    `json:"monthly"`
	Yearly   int    `json:"yearly"`
}

// SnapPolicySet adalah isi berkas kebijakan.
//
// 🔴 Present adalah penjaga yang paling penting di berkas ini. Berkas yang
// tidak ada berarti mesin ini TIDAK MEMAKAI kebijakan snapshot terkelola —
// bukan berarti seluruh datasetnya tidak terlindungi. Menyalakan temuan
// "tidak tercakup" untuk mesin semacam itu akan menyalakan satu temuan per
// dataset di menit pertama, dan sesudah itu tidak ada yang membaca daftarnya
// lagi. Ketiadaan data bukan data buruk — pelajaran yang sudah dibayar dua
// kali di proyek ini (docs/plan/05-pemasangan.md).
type SnapPolicySet struct {
	Source  string `json:"source,omitempty"`
	Present bool   `json:"present"`
	// Templates: nama template yang tersedia, untuk potongan config yang
	// ditawarkan panel kelola bagi dataset yang belum tercakup.
	Templates []string `json:"templates,omitempty"`

	sections  []snapSection
	templates map[string]map[string]string
}

type snapSection struct {
	name string
	vals map[string]string
}

// LoadSnapPolicy membaca berkas kebijakan. Berkas yang tidak ada BUKAN error:
// hasilnya set kosong dengan Present=false, dan seluruh aturan yang
// bergantung padanya ikut diam.
func LoadSnapPolicy(path string) (SnapPolicySet, error) {
	if path == "" {
		return SnapPolicySet{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SnapPolicySet{Source: path}, nil
		}
		return SnapPolicySet{Source: path}, err
	}
	p := parseSnapPolicy(string(b))
	p.Source = path
	return p, nil
}

// parseSnapPolicy dipisahkan dari pembacaan berkas supaya bisa diuji: di
// parser inilah konfigurasi dunia nyata paling sering mengejutkan.
func parseSnapPolicy(src string) SnapPolicySet {
	p := SnapPolicySet{Present: true, templates: map[string]map[string]string{}}

	var cur map[string]string
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			cur = map[string]string{}
			if t, ok := strings.CutPrefix(name, "template_"); ok {
				p.templates[t] = cur
				p.Templates = append(p.Templates, t)
			} else {
				p.sections = append(p.sections, snapSection{name: name, vals: cur})
			}
			continue
		}

		// Baris kunci di luar bagian mana pun tidak punya pemilik; membuangnya
		// lebih jujur daripada menempelkannya ke bagian sebelumnya.
		if cur == nil {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return p
}

// For mencari kebijakan yang berlaku untuk satu dataset, atau nil kalau tidak
// ada yang mencakupnya.
//
// Yang menang adalah bagian yang PALING SPESIFIK. Bagian `pool/prod` yang
// rekursif mencakup `pool/prod/db`, tapi bagian `pool/prod/db` sendiri — kalau
// ada — yang dipakai. Tanpa aturan ini, pengecualian yang ditulis orang di
// bawah aturan umum akan diam-diam diabaikan.
func (p SnapPolicySet) For(dataset string) *SnapPolicy {
	best := -1
	bestLen := -1
	for i, s := range p.sections {
		if !covers(s, dataset) {
			continue
		}
		if len(s.name) > bestLen {
			best, bestLen = i, len(s.name)
		}
	}
	if best < 0 {
		return nil
	}
	pol := p.resolve(p.sections[best])
	return &pol
}

func covers(s snapSection, dataset string) bool {
	if s.name == dataset {
		// process_children_only: bagiannya ada untuk anak-anaknya, dan dataset
		// induknya sendiri memang sengaja tidak di-snapshot.
		return !truthy(s.vals["process_children_only"])
	}
	return strings.HasPrefix(dataset, s.name+"/") && s.vals["recursive"] != "" &&
		!strings.EqualFold(s.vals["recursive"], "no")
}

// resolve menggabungkan template dengan nilai yang ditulis langsung di
// bagiannya. Urutannya: template lebih dulu (sesuai urutan penulisannya),
// lalu nilai bagian itu sendiri menimpa — supaya satu baris yang ditulis
// khusus untuk satu dataset benar-benar berlaku untuk dataset itu.
func (p SnapPolicySet) resolve(s snapSection) SnapPolicy {
	merged := map[string]string{}
	names := strings.Split(s.vals["use_template"], ",")
	for _, t := range names {
		t = strings.TrimSpace(t)
		for k, v := range p.templates[t] {
			merged[k] = v
		}
	}
	for k, v := range s.vals {
		if k != "use_template" {
			merged[k] = v
		}
	}

	return SnapPolicy{
		Section:  s.name,
		Template: strings.TrimSpace(s.vals["use_template"]),
		Autosnap: truthy(merged["autosnap"]),
		Hourly:   atoiSafe(merged["hourly"]),
		Daily:    atoiSafe(merged["daily"]),
		Monthly:  atoiSafe(merged["monthly"]),
		Yearly:   atoiSafe(merged["yearly"]),
	}
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true", "1", "on":
		return true
	}
	return false
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// snapExempt mencocokkan dataset dengan daftar pengecualian dari config.
//
// Pengecualian ini ada karena aturan "tidak tercakup" pasti punya kekecualian
// yang sah: dataset scratch, rekaman CCTV yang memang tidak perlu di-snapshot,
// dataset yang dilindungi cara lain. Tanpa jalan mematikan satu temuan dengan
// sadar, orang mematikan SELURUH daftarnya.
//
// Pola `pool/data/*` mencakup keturunannya; tanpa `/*` yang cocok hanya
// dataset itu sendiri.
func snapExempt(dataset string, patterns []string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if prefix, ok := strings.CutSuffix(pat, "/*"); ok {
			if dataset == prefix || strings.HasPrefix(dataset, prefix+"/") {
				return true
			}
			continue
		}
		if dataset == pat {
			return true
		}
	}
	return false
}
