package ops

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Schedule menjawab "apa yang berjalan sendiri di mesin ini, dan kapan" untuk
// hal-hal yang menjaga data: scrub, trim, snapshot, SMART self-test.
//
// Cockpit menampilkan timer systemd, tapi tidak cron dan tidak smartd — dan
// Debian menjadwalkan scrub bawaannya lewat CRON (/etc/cron.d/zfsutils-linux).
// Menampilkan timer saja berarti melewatkan scrub yang justru paling umum.
//
// Semuanya DIBACA tanpa root: systemctl list-timers dan berkas di /etc.
type Schedule struct {
	Timers []Timer     `json:"timers"`
	Other  int         `json:"other_timers"`
	Cron   []CronEntry `json:"cron"`
	Smartd *Smartd     `json:"smartd,omitempty"`
	// Notes: hal yang baru terlihat saat semua sumber dijejerkan, seperti
	// scrub yang dijadwalkan dua kali lewat dua mekanisme berbeda.
	Notes  []string `json:"notes,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

type Timer struct {
	Unit      string     `json:"unit"`
	Activates string     `json:"activates"`
	Next      *time.Time `json:"next,omitempty"`
	Last      *time.Time `json:"last,omitempty"`
}

type CronEntry struct {
	File     string `json:"file"`
	Schedule string `json:"schedule"`
	User     string `json:"user,omitempty"`
	Command  string `json:"command"`
}

type Smartd struct {
	File string `json:"file"`
	// Lines: baris aktif smartd.conf apa adanya.
	Lines []string `json:"lines"`
	// SelfTests: ada direktif -s, artinya smartd menjadwalkan self-test sendiri.
	SelfTests bool `json:"self_tests"`
}

// relevant memilih yang menyangkut penyimpanan. Timer lain (apt, logrotate,
// fstrim milik ext4, ...) dihitung saja, tidak didaftar: di mesin biasa ada
// dua puluhan, dan daftar sepanjang itu menenggelamkan tiga baris yang dicari.
var relevant = regexp.MustCompile(`(?i)zpool|zfs|scrub|trim|sanoid|syncoid|smart|issboard`)

// ReadSchedule mengumpulkan ketiga sumber. Kegagalan satu sumber dicatat,
// sisanya tetap dikembalikan — pola yang sama dengan collector.
func ReadSchedule(ctx context.Context) Schedule {
	var s Schedule

	out, err := exec.CommandContext(ctx, "systemctl", "list-timers", "--all", "--output=json", "--no-pager").Output()
	if err != nil {
		s.Errors = append(s.Errors, "systemctl list-timers: "+err.Error())
	} else if ts, err := parseTimers(out); err != nil {
		s.Errors = append(s.Errors, "systemctl list-timers: "+err.Error())
	} else {
		for _, t := range ts {
			if relevant.MatchString(t.Unit) || relevant.MatchString(t.Activates) {
				s.Timers = append(s.Timers, t)
			} else {
				s.Other++
			}
		}
	}

	files := []string{"/etc/crontab"}
	more, _ := filepath.Glob("/etc/cron.d/*")
	files = append(files, more...)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, c := range parseCron(string(b), f) {
			if relevant.MatchString(c.Command) {
				s.Cron = append(s.Cron, c)
			}
		}
	}

	for _, f := range []string{"/etc/smartd.conf", "/etc/smartmontools/smartd.conf"} {
		if b, err := os.ReadFile(f); err == nil {
			s.Smartd = parseSmartd(string(b), f)
			break
		}
	}

	_, err = os.Stat("/proc/spl/kstat/zfs")
	s.Notes = scheduleNotes(s, err == nil)
	return s
}

// parseTimers membaca `systemctl list-timers --output=json`. Waktunya dalam
// MIKROdetik sejak epoch, dan 0/null berarti "tidak ada" — bukan 1970.
func parseTimers(b []byte) ([]Timer, error) {
	var raw []struct {
		Next      *int64 `json:"next"`
		Last      *int64 `json:"last"`
		Unit      string `json:"unit"`
		Activates string `json:"activates"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	usec := func(p *int64) *time.Time {
		if p == nil || *p <= 0 {
			return nil
		}
		t := time.UnixMicro(*p)
		return &t
	}
	out := make([]Timer, 0, len(raw))
	for _, r := range raw {
		out = append(out, Timer{Unit: r.Unit, Activates: r.Activates, Next: usec(r.Next), Last: usec(r.Last)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out, nil
}

// parseCron membaca format sistem (/etc/crontab, /etc/cron.d): lima kolom
// waktu ATAU @kata-kunci, lalu user, lalu perintah.
func parseCron(s, file string) []CronEntry {
	var out []CronEntry
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		// Baris variabel lingkungan: SHELL=/bin/sh, PATH=...
		if strings.Contains(f[0], "=") {
			continue
		}
		n := 5
		if strings.HasPrefix(f[0], "@") {
			n = 1
		}
		if len(f) < n+2 {
			continue
		}
		out = append(out, CronEntry{
			File:     file,
			Schedule: strings.Join(f[:n], " "),
			User:     f[n],
			Command:  strings.Join(f[n+1:], " "),
		})
	}
	return out
}

func parseSmartd(s, file string) *Smartd {
	sd := &Smartd{File: file}
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sd.Lines = append(sd.Lines, line)
		for _, w := range strings.Fields(line) {
			if w == "-s" {
				sd.SelfTests = true
			}
		}
	}
	return sd
}

// zfsScrub sengaja lebih sempit dari `relevant`: e2scrub_all milik ext4 juga
// mengandung "scrub", dan menghitungnya sebagai scrub ZFS akan membuat catatan
// "scrub terjadwal ada" benar di mesin yang pool-nya tidak pernah di-scrub.
var zfsScrub = regexp.MustCompile(`(?i)zpool\s+scrub|zfs[-/\w]*scrub`)

// hasZFS: catatan scrub hanya berarti di host yang memang memuat ZFS. Di mesin
// tanpa pool, "tidak ada scrub terjadwal" selalu benar dan tidak berarti apa-apa.
func scheduleNotes(s Schedule, hasZFS bool) []string {
	var notes []string
	cronScrub, timerScrub := false, false
	for _, c := range s.Cron {
		if zfsScrub.MatchString(c.Command) {
			cronScrub = true
		}
	}
	for _, t := range s.Timers {
		if zfsScrub.MatchString(t.Unit) || zfsScrub.MatchString(t.Activates) {
			timerScrub = true
		}
	}
	switch {
	case !hasZFS:
	case cronScrub && timerScrub:
		notes = append(notes, "scrub dijadwalkan lewat cron DAN timer systemd — "+
			"pool yang sama bisa di-scrub dua kali; periksa apakah itu disengaja")
	case !cronScrub && !timerScrub:
		notes = append(notes, "tidak ada scrub terjadwal yang terlihat di cron maupun timer systemd")
	}
	if s.Smartd != nil && !s.Smartd.SelfTests {
		notes = append(notes, "smartd.conf tidak punya direktif -s: tidak ada SMART self-test terjadwal")
	}
	return notes
}
