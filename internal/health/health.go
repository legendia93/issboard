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
)

// Evaluate mengembalikan temuan, terurut: kritis dulu.
func Evaluate(s collector.Snapshot) []Finding {
	var f []Finding
	f = append(f, evalPools(s.Pools)...)
	f = append(f, evalSmart(s.Smart)...)
	f = append(f, evalContainers(s.Containers)...)
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

		for _, p := range c.PublishedPorts {
			if strings.HasPrefix(p, "0.0.0.0:") || strings.HasPrefix(p, ":::") {
				f = append(f, Finding{Warn, "ctr.exposed." + c.Name + "." + p,
					"Port terbuka ke semua alamat",
					fmt.Sprintf("%s mem-publish %s — terjangkau dari seluruh jaringan, bukan cuma localhost", c.Name, p), c.Name})
			}
		}
	}
	return f
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
