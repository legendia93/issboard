// Package health mengubah snapshot mentah jadi daftar temuan.
//
// Aturannya sengaja ditaruh di Go, bukan di JavaScript dashboard, karena
// fase 2 (issboard-agent yang mengirim notifikasi) harus memakai aturan yang
// PERSIS SAMA. Dua salinan aturan di dua bahasa akan berbeda pelan-pelan, dan
// yang gagal duluan justru jalur alert — satu-satunya yang dilihat orang saat
// halaman dashboard tidak dibuka.
package health

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/legendia93/issboard/internal/collector"
)

type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Crit Level = "crit"
)

// Finding adalah satu hal yang perlu diketahui manusia.
type Finding struct {
	Level Level `json:"level"`
	// Key stabil lintas siklus — fase 2 memakainya untuk de-duplikasi alert,
	// supaya disk yang sama tidak mengirim notifikasi tiap menit selamanya.
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Subject menautkan temuan ke kartu yang bersangkutan di UI.
	Subject string `json:"subject,omitempty"`
}

// Verdict adalah ringkasan satu kalimat untuk kartu hero.
type Verdict struct {
	Level Level `json:"level"`
	Warn  int   `json:"warn"`
	Crit  int   `json:"crit"`
}

// Ambang yang dipakai aturan di bawah. Dikumpulkan di satu tempat supaya
// bisa dijadikan konfigurasi nanti tanpa berburu angka ajaib di seluruh file.
const (
	poolFullWarnPct = 80
	poolFullCritPct = 90
	scrubStaleAfter = 35 * 24 * time.Hour // sebulan sekali + kelonggaran
	diskTempWarnC   = 50
	diskTempCritC   = 60

	// snapCoverMinBytes: di bawah ini, dataset yang tidak tercakup kebijakan
	// snapshot TIDAK jadi temuan.
	//
	// Bukan angka kenyamanan — angka ini yang menentukan aturannya berguna
	// atau tidak. Dataset induk hanyalah wadah: `pool/prod` yang menampung
	// sepuluh anak berisi 122 KB miliknya sendiri, dan kebijakan snapshot
	// yang benar memang menargetkan anak-anaknya. Menandai tiap wadah berarti
	// separuh daftar temuan berisi hal yang tidak salah — dan daftar semacam
	// itu berhenti dibaca (docs/plan/05-pemasangan.md, cacat #1).
	//
	// Yang tersisa di atas ambang ini adalah dataset yang benar-benar
	// menyimpan sesuatu tanpa satu pun aturan yang melindunginya.
	snapCoverMinBytes = 1 << 30 // 1 GiB

	// snapStaleGrace memberi kelonggaran di atas jarak yang seharusnya:
	// pengumpulnya bertimer, jadi snapshot "tiap jam" wajar terlambat
	// beberapa menit tanpa ada yang rusak.
	snapStaleGrace = time.Hour
	// snapDeadFactor: sudah bukan telat lagi, tapi berhenti.
	snapDeadFactor = 8
)

// Evaluate mengembalikan temuan, terurut: kritis dulu.
func Evaluate(s collector.Snapshot) []Finding {
	var f []Finding
	f = append(f, evalPools(s.Pools)...)
	f = append(f, evalSmart(s.Smart)...)
	f = append(f, evalContainers(s.Containers)...)
	f = append(f, evalSnapshots(s)...)
	return sortByLevel(f)
}

// Summarize memadatkan temuan jadi vonis untuk kartu hero.
func Summarize(fs []Finding) Verdict {
	v := Verdict{Level: OK}
	for _, x := range fs {
		switch x.Level {
		case Crit:
			v.Crit++
		case Warn:
			v.Warn++
		}
	}
	switch {
	case v.Crit > 0:
		v.Level = Crit
	case v.Warn > 0:
		v.Level = Warn
	}
	return v
}

func evalPools(pools []collector.Pool) []Finding {
	var f []Finding
	for _, p := range pools {
		if !strings.EqualFold(p.Health, "ONLINE") {
			f = append(f, Finding{Crit, "pool.health." + p.Name,
				"Pool tidak ONLINE", fmt.Sprintf("%s berstatus %s", p.Name, p.Health), p.Name})
		}

		// Stripe itu bukan kerusakan, tapi wajib terlihat: satu disk mati dan
		// seluruh pool hilang. Paling sering tak sengaja, karena `zpool add`
		// dipakai di tempat `zpool attach`.
		if !p.Mirrored && len(p.Devices) > 1 {
			f = append(f, Finding{Warn, "pool.stripe." + p.Name,
				"Pool ini stripe, bukan mirror",
				fmt.Sprintf("%s menggabungkan %d disk tanpa redundansi — satu disk mati, seluruh pool hilang",
					p.Name, len(p.Devices)), p.Name})
		}

		if n := p.ReadErr + p.WriteErr + p.CksumErr; n > 0 {
			f = append(f, Finding{Crit, "pool.errors." + p.Name,
				"Pool mencatat error I/O",
				fmt.Sprintf("%s: baca %d, tulis %d, checksum %d",
					p.Name, p.ReadErr, p.WriteErr, p.CksumErr), p.Name})
		}

		f = append(f, evalScrub(p)...)

		if p.SizeBytes > 0 {
			pct := int(p.AllocBytes * 100 / p.SizeBytes)
			switch {
			case pct >= poolFullCritPct:
				f = append(f, Finding{Crit, "pool.full." + p.Name, "Pool hampir penuh",
					fmt.Sprintf("%s terpakai %d%%", p.Name, pct), p.Name})
			case pct >= poolFullWarnPct:
				f = append(f, Finding{Warn, "pool.full." + p.Name, "Pool mulai penuh",
					fmt.Sprintf("%s terpakai %d%%", p.Name, pct), p.Name})
			}
		}
	}
	return f
}

// evalScrub membaca baris `scan:` dari `zpool status`. Bentuknya bermacam-macam
// antar versi ZFS, jadi yang dicari cuma dua hal yang bisa diandalkan:
// "belum pernah" dan tanggal selesai terakhir.
func evalScrub(p collector.Pool) []Finding {
	line := strings.ToLower(p.ScanLine)

	if line == "" || strings.Contains(line, "none requested") {
		return []Finding{{Warn, "pool.scrub." + p.Name, "Pool belum pernah di-scrub",
			p.Name + ": redundansi tanpa scrub cuma ada di atas kertas — bit rot baru ketahuan saat resilver, di momen paling rawan", p.Name}}
	}
	if strings.Contains(line, "in progress") {
		return nil // sedang jalan; bukan temuan
	}
	if strings.Contains(line, "canceled") {
		return []Finding{{Warn, "pool.scrub." + p.Name, "Scrub terakhir dibatalkan",
			p.Name + ": " + p.ScanLine, p.Name}}
	}

	// "scrub repaired 0B in ... on Sun Aug 10 03:14:22 2026"
	if at, ok := scrubFinishedAt(p.ScanLine); ok && time.Since(at) > scrubStaleAfter {
		return []Finding{{Warn, "pool.scrub." + p.Name, "Scrub sudah lama tidak jalan",
			fmt.Sprintf("%s: terakhir %d hari lalu — apakah timer scrub-nya masih aktif?",
				p.Name, int(time.Since(at).Hours()/24)), p.Name}}
	}
	return nil
}

// scrubFinishedAt mengambil stempel waktu di ujung baris `scan:`.
// Formatnya dari ctime(3), tanpa zona waktu — jadi dibaca sebagai waktu lokal.
func scrubFinishedAt(line string) (time.Time, bool) {
	i := strings.LastIndex(line, " on ")
	if i < 0 {
		return time.Time{}, false
	}
	stamp := strings.TrimSpace(line[i+4:])
	for _, layout := range []string{"Mon Jan _2 15:04:05 2006", "Mon Jan 2 15:04:05 2006"} {
		if t, err := time.ParseInLocation(layout, stamp, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func evalSmart(r collector.SmartReport) []Finding {
	var f []Finding

	// Timer pengumpul yang mati adalah temuan tersendiri, bukan sekadar
	// ketiadaan data: layar jadi terlihat sehat justru karena buta.
	if r.Stale {
		age := "belum pernah terisi"
		if !r.WrittenAt.IsZero() {
			age = fmt.Sprintf("terakhir %s lalu", time.Since(r.WrittenAt).Round(time.Hour))
		}
		f = append(f, Finding{Warn, "smart.stale", "Cache SMART basi",
			"issboard hanya membaca cache; " + age + " — periksa issboard-smart.timer", ""})
	}

	for _, d := range r.Disks {
		// 🔴 Disk yang sedang tidur tidak melaporkan APA PUN, dan itu memang
		// disengaja: pengumpulnya memakai `smartctl -n standby` supaya disk
		// yang tidur dibiarkan tidur (design.md §3.3). Hasilnya seluruh field
		// bernilai kosong — termasuk `passed`, yang kosongnya berarti false.
		//
		// Membaca itu sebagai "SMART gagal" adalah kesalahan yang sama dengan
		// membaca suhu 0 sebagai dingin: KETIADAAN data diperlakukan sebagai
		// data buruk. Di mesin sungguhan ini langsung memunculkan satu alarm
		// KRITIS palsu pada menit pertama — dan alarm palsu di hari pertama
		// adalah cara tercepat membuat orang berhenti memercayai alatnya.
		//
		// Kalau timer pengumpulnya sendiri yang mati, itu tertangkap terpisah
		// oleh r.Stale di atas.
		if d.Standby {
			continue
		}
		if !d.Passed {
			f = append(f, Finding{Crit, "smart.failed." + d.Device, "SMART gagal",
				d.Device + " (" + d.Model + ") melaporkan tidak PASSED", d.Device})
		}
		if d.PendingSect > 0 {
			f = append(f, Finding{Crit, "smart.pending." + d.Device, "Ada pending sector",
				fmt.Sprintf("%s: %d sektor menunggu dipetakan ulang — sedang memburuk sekarang",
					d.Device, d.PendingSect), d.Device})
		}
		if d.Reallocated > 0 {
			f = append(f, Finding{Warn, "smart.realloc." + d.Device, "Ada reallocated sector",
				fmt.Sprintf("%s: %d sektor — yang penting bukan angkanya, tapi apakah naik",
					d.Device, d.Reallocated), d.Device})
		}
		// Disk yang sedang tidur tidak melaporkan suhu; 0 berarti tak terbaca,
		// bukan dingin.
		if d.Temperature >= diskTempCritC {
			f = append(f, Finding{Crit, "smart.temp." + d.Device, "Disk terlalu panas",
				fmt.Sprintf("%s: %d°C", d.Device, d.Temperature), d.Device})
		} else if d.Temperature >= diskTempWarnC {
			f = append(f, Finding{Warn, "smart.temp." + d.Device, "Suhu disk tinggi",
				fmt.Sprintf("%s: %d°C", d.Device, d.Temperature), d.Device})
		}
	}
	return f
}

// evalSnapshots membandingkan berkas kebijakan snapshot dengan dataset nyata.
//
// Dua pertanyaan yang tidak dijawab alat lain mana pun, dan dua-duanya diam
// kalau jawabannya buruk: "apakah ada data yang tidak dilindungi apa pun?"
// dan "apakah perlindungan yang tertulis itu masih benar-benar berjalan?"
//
// 🔴 Seluruhnya diam kalau berkas kebijakannya tidak ada. Mesin tanpa
// snapshot terkelola bukan mesin yang seluruh datasetnya bermasalah — dan
// aturan yang menyala satu kali per dataset di menit pertama akan membuat
// orang mematikan seluruh daftar temuannya. Ketiadaan data bukan data buruk.
func evalSnapshots(s collector.Snapshot) []Finding {
	if !s.SnapPolicy.Present {
		return nil
	}

	var f []Finding
	for _, d := range s.Datasets {
		if d.SnapExempt {
			continue
		}

		if d.SnapPolicy == nil {
			// Dataset wadah dan dataset kecil sengaja tidak dihitung; lihat
			// snapCoverMinBytes.
			if d.UsedByDataset >= snapCoverMinBytes {
				f = append(f, Finding{Warn, "snap.uncovered." + d.Name,
					"Dataset tidak tercakup kebijakan snapshot",
					fmt.Sprintf("%s berisi %s dan tidak masuk bagian mana pun di %s — dataset baru tidak ikut sendiri, dan tidak ada yang memberi tahu",
						d.Name, humanBytes(d.UsedByDataset), s.SnapPolicy.Source), d.Name})
			}
			continue
		}

		// autosnap = no adalah keputusan yang sudah diambil orang dengan
		// sadar, tertulis di berkasnya. Menanyakannya lagi tiap menit bukan
		// kewaspadaan, itu cuma berisik.
		p := d.SnapPolicy
		if !p.Autosnap {
			continue
		}
		want := snapInterval(p)
		if want == 0 {
			continue // autosnap menyala tapi tidak ada retensi apa pun
		}

		// Tercakup, autosnap menyala, retensi tertulis — dan tetap nol
		// snapshot. Ini BUKAN ketiadaan data yang dinilai sebagai data buruk:
		// berkasnya sendiri yang mengklaim dataset ini di-snapshot, jadi
		// nol adalah kontradiksi, bukan kekosongan.
		if d.LastSnapshot == nil {
			f = append(f, Finding{Crit, "snap.never." + d.Name,
				"Tercakup kebijakan, tapi belum pernah ter-snapshot",
				fmt.Sprintf("%s ada di bagian [%s] dengan autosnap menyala, dan tidak punya satu pun snapshot — yang tertulis tidak sama dengan yang terjadi",
					d.Name, p.Section), d.Name})
			continue
		}

		age := time.Since(*d.LastSnapshot)
		switch {
		case age > want*snapDeadFactor:
			f = append(f, Finding{Crit, "snap.stale." + d.Name, "Snapshot berhenti",
				fmt.Sprintf("%s: terakhir %s lalu, seharusnya tiap %s — timernya bisa saja masih `active` tanpa menghasilkan apa pun",
					d.Name, roundAge(age), roundAge(want)), d.Name})
		case age > want*2+snapStaleGrace:
			f = append(f, Finding{Warn, "snap.stale." + d.Name, "Snapshot terlambat",
				fmt.Sprintf("%s: terakhir %s lalu, seharusnya tiap %s",
					d.Name, roundAge(age), roundAge(want)), d.Name})
		}
	}
	return f
}

// snapInterval menurunkan "seharusnya tiap berapa lama" dari retensi yang
// tertulis, bukan dari satu ambang tetap.
//
// Ini bagian yang paling mudah salah. Ambang tetap 6 jam akan menandai enam
// dataset SEHAT sekaligus di host yang kebijakannya memang harian — dan
// aturan yang menyala untuk keadaan normal adalah cara tercepat membuat
// seluruh daftar temuan diabaikan. Yang menentukan bukan selera kita, tapi
// apa yang diminta berkasnya sendiri.
func snapInterval(p *collector.SnapPolicy) time.Duration {
	switch {
	case p.Hourly > 0:
		return time.Hour
	case p.Daily > 0:
		return 24 * time.Hour
	case p.Monthly > 0:
		return 30 * 24 * time.Hour
	case p.Yearly > 0:
		return 365 * 24 * time.Hour
	}
	return 0
}

// roundAge menulis durasi dengan satuan yang wajar dibaca manusia. "8 hari"
// terbaca sekali lihat; "192h0m0s" harus dihitung dulu, dan temuan yang harus
// dihitung dulu akan dilewati orang yang sedang buru-buru.
func roundAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d hari", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d jam", int(d.Hours()))
	case d >= time.Hour:
		return "1 jam"
	default:
		return fmt.Sprintf("%d menit", int(d.Minutes()))
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func evalContainers(cs []collector.Container) []Finding {
	var f []Finding
	for _, c := range cs {
		running := strings.EqualFold(c.State, "running")

		if !running {
			f = append(f, Finding{Warn, "ctr.down." + c.Name, "Container tidak jalan",
				fmt.Sprintf("%s: %s", c.Name, c.Status), c.Name})
			continue
		}

		// Jebakan yang mahal: statusnya `Up`, jadi semua terlihat baik-baik
		// saja, tapi container tidak terjangkau siapa pun.
		if len(c.Networks) == 0 {
			f = append(f, Finding{Crit, "ctr.nonet." + c.Name, "Jalan tapi tanpa network",
				c.Name + " berstatus Up tapi tidak terhubung ke network mana pun — terlihat sehat, sebenarnya tak terjangkau", c.Name})
		}

		if strings.EqualFold(c.Health, "unhealthy") {
			f = append(f, Finding{Crit, "ctr.unhealthy." + c.Name, "Healthcheck gagal",
				c.Name + " melaporkan unhealthy", c.Name})
		}

		f = append(f, evalPorts(c)...)
	}
	return f
}

// portSensitif adalah layanan yang berbahaya kalau terjangkau dari luar
// localhost. Daftarnya SENGAJA pendek.
//
// Versi pertama menandai SEMUA port yang ter-publish ke 0.0.0.0, dan di server
// sungguhan hasilnya 19 dari 25 temuan — padahal mem-publish port justru cara
// aplikasi web dijangkau; itu bukan kecelakaan, itu tujuannya. Aturan yang
// menyala untuk keadaan normal melatih orang mengabaikan seluruh daftarnya,
// dan setelah itu temuan yang sungguhan ikut tidak terbaca.
//
// Yang tersisa di sini cuma yang biasanya TIDAK punya autentikasi kuat, atau
// yang kalau tembus berarti seluruh host ikut jatuh. Kalau ada yang kurang
// atau kelebihan untuk mesin Anda, di sinilah tempat mengubahnya.
var portSensitif = map[int]struct {
	nama  string
	level Level
}{
	5432:  {"PostgreSQL", Warn},
	3306:  {"MySQL/MariaDB", Warn},
	33060: {"MySQL X Protocol", Warn},
	1433:  {"SQL Server", Warn},
	27017: {"MongoDB", Warn},
	6379:  {"Redis", Warn},
	11211: {"Memcached", Warn},
	5984:  {"CouchDB", Warn},
	9200:  {"Elasticsearch", Warn},
	9300:  {"Elasticsearch (transport)", Warn},
	8086:  {"InfluxDB", Warn},
	5672:  {"RabbitMQ", Warn},
	2049:  {"NFS", Warn},
	445:   {"SMB", Warn},
	3389:  {"RDP", Warn},
	5900:  {"VNC", Warn},
	21:    {"FTP", Warn},
	// Dua ini kritis, bukan sekadar perhatian: API Docker tanpa autentikasi
	// setara memberi root di host ini kepada siapa pun yang bisa menjangkaunya,
	// dan Telnet mengirim kata sandi sebagai teks polos.
	2375:  {"API Docker tanpa TLS", Crit},
	10250: {"kubelet", Crit},
	23:    {"Telnet", Crit},
}

// evalPorts memeriksa port yang ter-publish ke luar localhost. Yang tidak ada
// di daftar sensitif TIDAK jadi temuan — ia tetap ditampilkan sebagai chip di
// kartunya, karena melihatnya berguna, tapi melihatnya bukan berarti alarm.
func evalPorts(c collector.Container) []Finding {
	var f []Finding
	for _, p := range c.PublishedPorts {
		svc, sensitif := portSensitif[publicPort(p)]
		if !sensitif {
			continue
		}
		f = append(f, Finding{svc.level, "ctr.exposed." + c.Name + "." + p,
			svc.nama + " terjangkau dari seluruh jaringan",
			fmt.Sprintf("%s mem-publish %s — layanan seperti ini biasanya mengandalkan "+
				"jaringan sebagai pembatas, bukan autentikasinya sendiri", c.Name, p), c.Name})
	}
	return f
}

// publicPort mengambil nomor port dari bentuk "alamat:publik->privat/proto".
func publicPort(s string) int {
	head, _, ok := strings.Cut(s, "->")
	if !ok {
		return 0
	}
	i := strings.LastIndex(head, ":")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(head[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// sortByLevel menaruh kritis di atas tanpa mengacak urutan dalam satu tingkat,
// supaya daftarnya tidak melompat-lompat antar refresh.
func sortByLevel(f []Finding) []Finding {
	out := make([]Finding, 0, len(f))
	for _, want := range []Level{Crit, Warn} {
		for _, x := range f {
			if x.Level == want {
				out = append(out, x)
			}
		}
	}
	return out
}
