package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AgentEnvFile: kredensial notifikasi, 0600 root, diserahkan systemd ke
// issboard-agent lewat EnvironmentFile. Berkas milik issboard sendiri (dipasang
// paketnya), jadi helper boleh menyuntingnya — tapi HANYA kunci di notifyKeys;
// baris lain dan komentar dibiarkan persis.
const AgentEnvFile = "/etc/issboard/agent.env"

// notifyKeys adalah seluruh kunci yang boleh diubah dari halaman kelola.
//
// 🔴 Validasinya bukan kerapian. Nilai ini ditulis ke berkas yang dibaca
// systemd sebagai KUNCI=NILAI per baris; nilai yang memuat baris baru akan
// menyelundupkan variabel lingkungan apa pun ke proses agent. Karena itu tiap
// nilai dicocokkan dengan himpunan karakter yang sempit — tanpa spasi, tanpa
// kutip, tanpa baris baru — bukan cuma "tidak kosong".
var notifyKeys = map[string]struct {
	re     *regexp.Regexp
	secret bool
	label  string
}{
	"ISSBOARD_TELEGRAM_TOKEN":   {regexp.MustCompile(`^[0-9]{5,15}:[A-Za-z0-9_-]{30,64}$`), true, "token bot Telegram"},
	"ISSBOARD_TELEGRAM_CHAT_ID": {regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`), false, "chat id Telegram"},
	"ISSBOARD_NTFY_URL":         {regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`), false, "alamat server ntfy"},
	// Di ntfy.sh publik, topik ADALAH rahasianya: siapa pun yang tahu namanya
	// bisa ikut membaca. Diperlakukan seperti token.
	"ISSBOARD_NTFY_TOPIC": {regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`), true, "topik ntfy"},
	"ISSBOARD_NTFY_TOKEN": {regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`), true, "token ntfy"},
}

func NotifyKeys() []string {
	var k []string
	for n := range notifyKeys {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

func ValidateNotify(key, val string) error {
	spec, ok := notifyKeys[key]
	if !ok {
		return fmt.Errorf("%q bukan pengaturan notifikasi yang bisa diubah", key)
	}
	if val != "" && !spec.re.MatchString(val) {
		return fmt.Errorf("%s tidak sah", spec.label)
	}
	return nil
}

// NotifyValue adalah keadaan satu kunci tanpa pernah membawa rahasianya:
// untuk kunci rahasia hanya empat karakter terakhir, cukup untuk mengenali
// "token yang mana" tanpa bisa dipakai.
type NotifyValue struct {
	Set    bool   `json:"set"`
	Value  string `json:"value,omitempty"` // hanya untuk yang bukan rahasia
	Hint   string `json:"hint,omitempty"`  // hanya untuk yang rahasia
	Secret bool   `json:"secret"`
}

func hint(v string) string {
	if len(v) <= 8 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

// parseEnv membaca format EnvironmentFile: KUNCI=NILAI, boleh berkutip.
func parseEnv(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m
}

func (e *Executor) notifyStatus() (string, error) {
	b, err := e.ReadFile(e.AgentEnv)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	env := parseEnv(b)
	out := map[string]NotifyValue{}
	for k, spec := range notifyKeys {
		v := env[k]
		nv := NotifyValue{Set: v != "", Secret: spec.secret}
		if v != "" {
			if spec.secret {
				nv.Hint = hint(v)
			} else {
				nv.Value = v
			}
		}
		out[k] = nv
	}
	j, _ := json.Marshal(out)
	return string(j), nil
}

// notifySet mengubah kunci yang diminta di agent.env. Nilai kosong =
// kuncinya dihapus. Baris lain, komentar, dan urutannya dibiarkan.
func (e *Executor) notifySet(changes map[string]string) (string, error) {
	if len(changes) == 0 {
		return "", fmt.Errorf("tidak ada yang diubah")
	}
	for k, v := range changes {
		if err := ValidateNotify(k, v); err != nil {
			return "", err
		}
	}
	b, err := e.ReadFile(e.AgentEnv)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	done := map[string]bool{}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		t := strings.TrimSpace(line)
		k, _, ok := strings.Cut(t, "=")
		k = strings.TrimSpace(k)
		if v, managed := changes[k]; ok && managed && !strings.HasPrefix(t, "#") {
			if !done[k] && v != "" {
				lines = append(lines, k+"="+v)
			}
			done[k] = true // baris ganda berikutnya ikut dibuang
			continue
		}
		lines = append(lines, line)
	}
	var added []string
	for _, k := range NotifyKeys() {
		if v, ok := changes[k]; ok && !done[k] && v != "" {
			added = append(added, k+"="+v)
		}
	}
	if len(added) > 0 {
		lines = append(lines, "", "# diatur dari halaman kelola issboard")
		lines = append(lines, added...)
	}
	if err := e.WriteFile(e.AgentEnv, []byte(strings.TrimLeft(strings.Join(lines, "\n"), "\n")+"\n"), 0o600); err != nil {
		return "", err
	}

	var names []string
	for k, v := range changes {
		act := "diubah"
		if v == "" {
			act = "dihapus"
		}
		names = append(names, notifyKeys[k].label+" "+act)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + " — berlaku di putaran agent berikutnya (≤1 menit)", nil
}

// notifyTest menjalankan issboard-agent -test DENGAN LINGKUNGAN YANG SAMA
// seperti timernya: sebagai user issboard, dengan agent.env yang sama.
//
// Lewat systemd-run, bukan exec langsung. Helper sendiri tidak punya jaringan
// (RestrictAddressFamilies tanpa AF_INET) — dan anak prosesnya mewarisi itu.
// Tes yang berjalan di lingkungan lain dari timernya juga tidak membuktikan
// apa pun tentang timernya.
func (e *Executor) notifyTest(ctx context.Context) (string, error) {
	out, err := e.Run(ctx, "systemd-run", "--wait", "--pipe", "--collect", "--quiet",
		"--unit=issboard-notify-test-"+fmt.Sprint(e.Now().Unix()),
		"-p", "User=issboard", "-p", "Group=issboard",
		"-p", "EnvironmentFile=-"+e.AgentEnv,
		"-p", "After=network-online.target",
		e.AgentBin, "-config", "/etc/issboard.yaml", "-test")
	return strings.TrimSpace(out), err
}

// agentBin mencari issboard-agent di sebelah helper: /usr/libexec →
// /usr/bin (paket), /usr/local/libexec → /usr/local/bin (install.sh).
func agentBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "/usr/bin/issboard-agent"
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "bin", "issboard-agent")
}

// ---------- pengaturan aplikasi (settings.conf) ----------

var (
	patternRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*(/[A-Za-z0-9][A-Za-z0-9_.:-]*)*(/\*)?$`)
	poolRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
)

// settingsKeys: kunci config yang boleh diatur dari halaman kelola. Kunci
// yang menentukan JALUR berkas (history_file, smart_cache, docker_socket,
// auth_file, helper_socket) sengaja tidak ada: mengarahkannya ke tempat lain
// dari browser adalah cara memindahkan apa yang dibaca atau ditulis proses
// yang berjalan dengan hak lebih.
var settingsKeys = map[string]func(string) error{
	"notify_min_level": func(v string) error {
		if v != "warn" && v != "crit" {
			return fmt.Errorf("notify_min_level harus warn atau crit")
		}
		return nil
	},
	"alert_repeat": durationIn(time.Hour, 30*24*time.Hour),
	"idle_timeout": func(v string) error {
		if v == "0" || v == "0s" {
			return nil
		}
		return durationIn(time.Minute, 24*time.Hour)(v)
	},
	"snapshot_exempt": listOf(func(p string) bool { return patternRe.MatchString(p) && !strings.Contains(p, "..") }),
	"pools":           listOf(poolRe.MatchString),
}

func durationIn(lo, hi time.Duration) func(string) error {
	return func(v string) error {
		d, err := time.ParseDuration(v)
		if err != nil || d < lo || d > hi {
			return fmt.Errorf("%q harus durasi antara %s dan %s", v, lo, hi)
		}
		return nil
	}
}

func listOf(ok func(string) bool) func(string) error {
	return func(v string) error {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" && !ok(p) {
				return fmt.Errorf("%q tidak sah", p)
			}
		}
		return nil
	}
}

func SettingsKeys() []string {
	var k []string
	for n := range settingsKeys {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

func ValidateSetting(key, val string) error {
	f, ok := settingsKeys[key]
	if !ok {
		return fmt.Errorf("%q tidak bisa diatur dari halaman kelola", key)
	}
	if val == "" {
		return nil
	}
	if strings.ContainsAny(val, "\n\r#") {
		return fmt.Errorf("nilai %s memuat karakter terlarang", key)
	}
	return f(val)
}

// normList merapikan daftar jadi "a, b, c" tanpa entri kosong.
func normList(v string) string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// settingsSet menulis ulang settings.conf: berkas ini seluruhnya milik
// halaman kelola, jadi ditulis utuh dari kunci yang ada. Nilai kosong =
// kuncinya dihapus, dan berkas config utama berlaku lagi untuknya.
func (e *Executor) settingsSet(changes map[string]string) (string, error) {
	if len(changes) == 0 {
		return "", fmt.Errorf("tidak ada yang diubah")
	}
	for k, v := range changes {
		if err := ValidateSetting(k, v); err != nil {
			return "", err
		}
	}
	cur := map[string]string{}
	if b, err := e.ReadFile(e.SettingsFile); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, ":"); ok && settingsKeys[strings.TrimSpace(k)] != nil {
				cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	for k, v := range changes {
		if k == "snapshot_exempt" || k == "pools" {
			v = normList(v)
		}
		if v == "" {
			delete(cur, k)
		} else {
			cur[k] = v
		}
	}
	var sb strings.Builder
	sb.WriteString("# Ditulis halaman kelola issboard. Nilai di sini menimpa /etc/issboard.yaml.\n")
	sb.WriteString("# Hapus sebuah baris (atau berkas ini) untuk kembali ke nilai di sana.\n")
	for _, k := range SettingsKeys() {
		if v, ok := cur[k]; ok {
			sb.WriteString(k + ": " + v + "\n")
		}
	}
	if err := e.WriteFile(e.SettingsFile, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	var names []string
	for k := range changes {
		names = append(names, k)
	}
	sort.Strings(names)
	return "tersimpan: " + strings.Join(names, ", "), nil
}
