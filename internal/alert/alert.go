// Package alert mengingat temuan mana yang sudah dikabari, dan memutuskan apa
// yang layak mengganggu orang siklus ini.
//
// 🔴 De-duplikasi bukan penyempurnaan, ini syarat supaya alertnya berguna.
// Disk dengan reallocated sector memicu temuan yang PERSIS SAMA tiap 60 detik
// selamanya. Tanpa ingatan "sudah dikabari", notifikasinya jadi berisik — dan
// alert yang berisik selalu berakhir sama: diabaikan, lalu dimatikan. Setelah
// itu tidak ada bedanya dengan tidak punya alert sama sekali.
//
// Aturan temuannya sendiri TIDAK ada di sini. Itu milik internal/health, yang
// dipakai bersama dashboard, supaya tidak ada dua salinan aturan yang berbeda
// pelan-pelan (docs/design.md §6).
package alert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/legendia93/issboard/internal/health"
)

// Entry adalah ingatan tentang satu temuan, lintas siklus dan lintas reboot.
type Entry struct {
	Level   health.Level `json:"level"`
	Title   string       `json:"title"`
	Detail  string       `json:"detail"`
	Subject string       `json:"subject,omitempty"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// NotifiedAt kosong berarti temuan ini terlihat tapi BELUM pernah
	// terkirim — mis. kanalnya belum dikonfigurasi, atau kirimannya gagal.
	// Bedanya penting: yang belum terkirim harus dicoba lagi, bukan didiamkan.
	NotifiedAt    time.Time    `json:"notified_at,omitempty"`
	NotifiedLevel health.Level `json:"notified_level,omitempty"`
}

type State struct {
	UpdatedAt time.Time        `json:"updated_at"`
	Active    map[string]Entry `json:"active"`
}

// firstRun benar kalau state ini belum pernah ditulis. Dipakai untuk memberi
// bingkai yang jujur pada pesan pertama: temuannya boleh jadi sudah
// berbulan-bulan umurnya, yang baru cuma pemantauannya.
func (st State) firstRun() bool { return st.UpdatedAt.IsZero() }

// Options mengumpulkan segala yang bisa berbeda antar pemanggilan.
type Options struct {
	Now time.Time

	// MinLevel: tingkat terendah yang layak dikirim.
	MinLevel health.Level

	// Repeat: jeda diam sebelum temuan yang masih bertahan disebut lagi.
	// Bukan untuk mendesak, tapi supaya masalah menahun tidak hilang dari
	// ingatan sepenuhnya. 0 mematikan pengingat.
	Repeat time.Duration

	// Settled false berarti pengumpulan datanya sendiri bermasalah, jadi
	// temuan yang hilang JANGAN dianggap pulih. Kalau `zpool` gagal dipanggil,
	// seluruh temuan pool ikut lenyap dari daftar — mengabarkannya sebagai
	// "pulih" persis saat kita kehilangan kemampuan melihat pool adalah
	// kebohongan yang paling mahal di program ini.
	Settled bool
}

// ParseLevel membaca nilai config jadi tingkat.
//
// Nilai yang tidak dikenal jatuh ke Warn, bukan ke Crit: salah ketik di config
// sebaiknya membuat alertnya terlalu ramai, bukan diam-diam membisukan
// setengah temuan.
func ParseLevel(s string) health.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "crit", "critical", "kritis":
		return health.Crit
	default:
		return health.Warn
	}
}

// Item adalah satu temuan sebagaimana muncul di pesan notifikasi.
//
// Bukan health.Finding apa adanya: yang dikirim ke orang perlu tahu SEJAK
// KAPAN, dan umur itu hanya ada di ingatan lintas siklus, bukan di temuannya.
type Item struct {
	Key     string
	Level   health.Level
	Title   string
	Detail  string
	Subject string
	Since   time.Time
}

// Decision adalah hasil satu siklus: apa yang perlu dikirim, dan kenapa.
type Decision struct {
	New       []Item // belum pernah dikabari sama sekali
	Worse     []Item // dulu perhatian, sekarang kritis
	Reminder  []Item // masih bertahan, sudah lama tidak disebut
	Recovered []Item // hilang dari daftar; pernah dikabari
	FirstRun  bool
}

func (d Decision) Any() bool {
	return len(d.New)+len(d.Worse)+len(d.Reminder)+len(d.Recovered) > 0
}

// Urgent hanya melihat yang benar-benar berubah jadi buruk. Pengingat dan
// kabar pulih tidak pernah membangunkan orang di tengah malam.
func (d Decision) Urgent() bool {
	for _, list := range [][]Item{d.New, d.Worse} {
		for _, it := range list {
			if it.Level == health.Crit {
				return true
			}
		}
	}
	return false
}

// Plan membandingkan temuan siklus ini dengan ingatan, TANPA mengubah state.
//
// Pemisahan ini disengaja: kalau kirimannya gagal, state tidak boleh
// terlanjur bergerak. Yang mencatat adalah MarkSent (hanya setelah benar-benar
// terkirim) dan Observe (yang mencatat kehadiran, terkirim atau tidak).
func (st State) Plan(fs []health.Finding, o Options) Decision {
	d := Decision{FirstRun: st.firstRun()}
	seen := map[string]bool{}

	for _, f := range fs {
		if rank(f.Level) < rank(o.MinLevel) {
			continue
		}
		seen[f.Key] = true
		prev, known := st.Active[f.Key]

		since := o.Now
		if known {
			since = prev.FirstSeen
		}
		it := Item{Key: f.Key, Level: f.Level, Title: f.Title,
			Detail: f.Detail, Subject: f.Subject, Since: since}

		switch {
		case !known || prev.NotifiedAt.IsZero():
			d.New = append(d.New, it)
		case f.Level == health.Crit && prev.NotifiedLevel == health.Warn:
			// Naik tingkat selalu dikabari ulang, seberapa pun barunya pesan
			// terakhir: "mulai penuh" jadi "hampir penuh" adalah kabar baru.
			d.Worse = append(d.Worse, it)
		case o.Repeat > 0 && o.Now.Sub(prev.NotifiedAt) >= o.Repeat:
			d.Reminder = append(d.Reminder, it)
		}
	}

	if !o.Settled {
		return d
	}
	for key, e := range st.Active {
		if seen[key] {
			continue
		}
		// Yang tidak pernah terkirim tidak perlu diumumkan pulih — orangnya
		// belum pernah tahu ada masalahnya.
		if e.NotifiedAt.IsZero() {
			continue
		}
		d.Recovered = append(d.Recovered, Item{Key: key, Level: e.Level,
			Title: e.Title, Detail: e.Detail, Subject: e.Subject, Since: e.FirstSeen})
	}
	// Iterasi map tidak berurutan; tanpa ini daftar pulih berganti urutan tiap
	// kali dan dua pesan yang sama jadi sulit dibandingkan.
	sort.Slice(d.Recovered, func(i, j int) bool { return d.Recovered[i].Key < d.Recovered[j].Key })
	return d
}

// staleEntry membatasi berapa lama temuan yang sudah hilang boleh menggantung
// menunggu kabar pulihnya terkirim.
const staleEntry = 7 * 24 * time.Hour

// MarkSent dipanggil HANYA setelah pesannya benar-benar terkirim. Kalau
// kirimannya gagal, ingatan sengaja dibiarkan apa adanya supaya siklus
// berikutnya mencoba lagi — alert yang hilang karena jaringan sedang putus
// adalah kegagalan paling mahal dari sistem seperti ini.
func (st *State) MarkSent(d Decision, now time.Time) {
	st.ensure()
	for _, list := range [][]Item{d.New, d.Worse, d.Reminder} {
		for _, it := range list {
			e, ok := st.Active[it.Key]
			if !ok {
				// Temuan baru belum tentu tercatat: Plan sengaja tidak
				// mengubah apa pun, dan Observe berjalan setelah ini.
				e = Entry{Title: it.Title, Detail: it.Detail,
					Subject: it.Subject, FirstSeen: it.Since, LastSeen: now}
			}
			e.Level = it.Level
			e.NotifiedAt = now
			e.NotifiedLevel = it.Level
			st.Active[it.Key] = e
		}
	}
	// Ceritanya sudah ditutup: kabar pulihnya terkirim, jadi ingatannya tidak
	// perlu disimpan lagi. Observe sengaja MENAHAN entri ini sampai baris di
	// bawah dijalankan, supaya kiriman yang gagal dicoba lagi siklus berikutnya.
	for _, it := range d.Recovered {
		delete(st.Active, it.Key)
	}
}

// Observe mencatat apa yang terlihat siklus ini — terkirim atau tidak.
//
// Temuan yang terlihat tapi belum sempat dikabari tetap dicatat kehadirannya
// dengan NotifiedAt kosong, supaya umurnya benar saat akhirnya terkirim.
func (st *State) Observe(fs []health.Finding, o Options) {
	st.ensure()
	seen := map[string]bool{}

	for _, f := range fs {
		if rank(f.Level) < rank(o.MinLevel) {
			continue
		}
		seen[f.Key] = true
		e := st.Active[f.Key]
		if e.FirstSeen.IsZero() {
			e.FirstSeen = o.Now
		}
		e.Level, e.Title, e.Detail, e.Subject = f.Level, f.Title, f.Detail, f.Subject
		e.LastSeen = o.Now
		st.Active[f.Key] = e
	}

	if !o.Settled {
		// Pengumpulan tidak lengkap: ingatan ditahan apa adanya. Temuan yang
		// "hilang" mungkin cuma tidak terlihat, dan melupakannya membuat
		// siklus berikutnya mengabarinya lagi sebagai temuan baru.
		return
	}
	for key, e := range st.Active {
		if seen[key] {
			continue
		}
		// Yang belum pernah dikabari boleh langsung dilupakan: tidak ada
		// cerita yang perlu ditutup.
		//
		// Yang SUDAH dikabari ditahan sampai kabar pulihnya benar-benar
		// terkirim — MarkSent yang menghapusnya. Kalau dihapus di sini,
		// satu kiriman yang gagal karena jaringan sedang putus membuat kabar
		// "sudah beres" hilang selamanya, dan orang tetap mengira masalahnya
		// masih ada. Alasannya sama persis dengan kenapa MarkSent tidak
		// dipanggil saat pengiriman gagal.
		//
		// Batas staleEntry menjaga berkas ini tidak tumbuh selamanya kalau
		// kabar pulihnya memang tidak akan pernah bisa terkirim — misalnya
		// tidak ada kanal yang dikonfigurasi sama sekali.
		if e.NotifiedAt.IsZero() || o.Now.Sub(e.LastSeen) > staleEntry {
			delete(st.Active, key)
		}
	}
}

func (st *State) ensure() {
	if st.Active == nil {
		st.Active = map[string]Entry{}
	}
}

// Load membaca ingatan. Berkas yang belum ada bukan error — mesin yang baru
// dipasangi issboard memang belum punya.
func Load(path string) (State, error) {
	st := State{Active: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		// State rusak diperlakukan seperti mesin baru: satu pesan ringkasan
		// yang berlebih jauh lebih murah daripada agent yang mati dan
		// membungkam seluruh alert.
		return State{Active: map[string]Entry{}}, err
	}
	if st.Active == nil {
		st.Active = map[string]Entry{}
	}
	return st, nil
}

// Save menulis atomik: tulis ke berkas sementara di folder yang sama, lalu
// rename. Sama seperti cache SMART — pembaca tidak boleh pernah mendapat
// berkas separuh jadi, apalagi berkas yang membuat semua temuan terlihat baru.
func Save(path string, st State) error {
	st.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
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
	// 0640: bukan kredensial, tapi isinya menyebut kondisi mesin sedetail
	// temuannya. Tidak ada alasan seluruh sistem bisa membacanya.
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func rank(l health.Level) int {
	switch l {
	case health.Crit:
		return 2
	case health.Warn:
		return 1
	default:
		return 0
	}
}
