// Package config memuat konfigurasi issboard.
//
// Sengaja tanpa dependensi YAML: formatnya sesederhana "kunci: nilai", dan
// satu binary statis tanpa dependensi adalah alasan utama proyek ini memilih
// Go (lihat docs/design.md §3.1).
package config

import (
	"bufio"
	"os"
	"strings"
	"time"
)

type Config struct {
	// Listen dipakai HANYA kalau systemd tidak menyerahkan socket.
	Listen string

	// IdleTimeout: binary keluar sendiri setelah sekian lama tanpa request,
	// lalu systemd menyalakannya lagi lewat socket activation.
	IdleTimeout time.Duration

	// SmartCache adalah file JSON yang ditulis unit root terpisah.
	// issboard TIDAK PERNAH memanggil smartctl sendiri — lihat plan 5.
	SmartCache string

	// DockerSocket dibaca read-only untuk daftar container.
	DockerSocket string

	// Pools yang ditampilkan. Kosong = deteksi otomatis lewat `zpool list`.
	Pools []string

	// SnapPolicyFile adalah berkas kebijakan snapshot (format sanoid) yang
	// dibandingkan dengan dataset nyata. Berkas yang tidak ada mematikan
	// seluruh aturan cakupan snapshot — itu perilaku yang benar untuk mesin
	// yang memang tidak memakai snapshot terkelola.
	SnapPolicyFile string

	// SnapExempt: dataset yang sengaja boleh tidak tercakup kebijakan.
	// Pola `pool/data/*` ikut mencakup keturunannya.
	SnapExempt []string

	// AuthFile: kredensial operator (hash kata sandi + rahasia sesi), ditulis
	// `issboard -set-password`. Berkas yang tidak ada = semua endpoint
	// bermutasi menolak. Bagian baca tetap terbuka seperti sebelumnya.
	AuthFile string

	// HelperSocket: socket issboard-helper, satu-satunya jalan ke aksi root
	// (scrub, SMART self-test, batas ARC). issboard sendiri tidak pernah root.
	HelperSocket string

	// --- Di bawah ini hanya dipakai issboard-agent (unit bertimer terpisah).
	//
	// 🔴 issboard sendiri TIDAK PERNAH memakainya untuk mengirim apa pun.
	// Dashboard ini tidak hidup saat halamannya tidak dibuka, jadi ia secara
	// desain tidak bisa jadi sumber alert (docs/design.md §3.2). Yang
	// mengirim adalah issboard-agent; issboard cuma berbagi berkas config.

	// HistoryFile ditulis agent, DIBACA issboard — pola yang sama dengan
	// cache SMART, dan alasan yang sama: RAM idle tetap 0 MB.
	HistoryFile string

	// AlertState adalah ingatan "sudah dikabari", supaya temuan yang bertahan
	// tidak mengirim notifikasi tiap menit selamanya.
	AlertState string

	// AlertRepeat: jeda diam sebelum temuan yang masih ada dikabari lagi.
	AlertRepeat time.Duration

	// NotifyMinLevel: "warn" (semua) atau "crit" (hanya yang kritis).
	NotifyMinLevel string

	// Kanal notifikasi. Boleh dua-duanya, boleh tidak sama sekali.
	//
	// ⚠️ Token JANGAN ditaruh di /etc/issboard.yaml yang dibaca semua orang.
	// Pakai variabel lingkungan lewat EnvironmentFile systemd yang permisinya
	// ketat — lihat systemd/issboard-agent.service.
	NtfyURL        string
	NtfyTopic      string
	NtfyToken      string
	TelegramToken  string
	TelegramChatID string

	// Demo menyajikan data palsu dan TIDAK menyentuh sistem sama sekali:
	// tidak ada zpool, tidak ada socket Docker, tidak ada cache SMART dibaca.
	// Dipakai untuk menggarap tampilan kondisi sakit, dan supaya screenshot
	// serta rekaman layar aman dibagikan — halaman ini menampilkan hostname,
	// alamat IP, nama pool, dan nama app.
	Demo bool
}

func Default() Config {
	return Config{
		Listen:       "127.0.0.1:9955",
		IdleTimeout:  5 * time.Minute,
		SmartCache:   "/var/cache/issboard/smart.json",
		DockerSocket: "/var/run/docker.sock",

		SnapPolicyFile: "/etc/sanoid/sanoid.conf",

		AuthFile:     "/etc/issboard/auth",
		HelperSocket: "/run/issboard-helper.sock",

		HistoryFile: "/var/lib/issboard/history.json",
		AlertState:  "/var/lib/issboard/alert-state.json",
		// Sehari sekali: cukup untuk menahan masalah menahun tetap terlihat,
		// cukup jarang untuk tidak jadi kebisingan yang dimatikan orang.
		AlertRepeat:    24 * time.Hour,
		NotifyMinLevel: "warn",
		NtfyURL:        "https://ntfy.sh",
	}
}

// Load membaca file konfigurasi. File yang tidak ada bukan error: default
// dipakai apa adanya, supaya issboard tetap hidup justru saat sistem kacau.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.applyEnv()
			return c, nil
		}
		return c, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)

		switch key {
		case "listen":
			c.Listen = val
		case "idle_timeout":
			if d, err := time.ParseDuration(val); err == nil {
				c.IdleTimeout = d
			}
		case "smart_cache":
			c.SmartCache = val
		case "docker_socket":
			c.DockerSocket = val
		case "snapshot_policy":
			c.SnapPolicyFile = val
		case "snapshot_exempt":
			c.SnapExempt = splitList(val)
		case "auth_file":
			c.AuthFile = val
		case "helper_socket":
			c.HelperSocket = val
		case "demo":
			c.Demo = val == "true" || val == "yes" || val == "1"
		case "history_file":
			c.HistoryFile = val
		case "alert_state":
			c.AlertState = val
		case "alert_repeat":
			if d, err := time.ParseDuration(val); err == nil {
				c.AlertRepeat = d
			}
		case "notify_min_level":
			c.NotifyMinLevel = val
		case "ntfy_url":
			c.NtfyURL = val
		case "ntfy_topic":
			c.NtfyTopic = val
		case "ntfy_token":
			c.NtfyToken = val
		case "telegram_token":
			c.TelegramToken = val
		case "telegram_chat_id":
			c.TelegramChatID = val
		case "pools":
			c.Pools = splitList(val)
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	c.applyEnv()
	return c, nil
}

func splitList(val string) []string {
	var out []string
	for _, p := range strings.Split(val, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyEnv membiarkan variabel lingkungan menimpa berkas config.
//
// Ini jalur yang DIANJURKAN untuk token: systemd bisa memuatnya lewat
// EnvironmentFile dari berkas ber-permisi 0600 milik root, sementara
// /etc/issboard.yaml boleh tetap bisa dibaca siapa saja. Kredensial di dalam
// berkas config adalah cara paling mudah token ikut ter-commit ke repo —
// dan repo ini publik.
func (c *Config) applyEnv() {
	for _, e := range []struct {
		key string
		dst *string
	}{
		// Di dalam container tidak ada berkas config yang enak disunting, dan
		// alamat bawaan 127.0.0.1 membuat prosesnya tak terjangkau dari luar
		// namespace-nya sendiri. Ini satu-satunya alasan alamat bisa diatur
		// lewat lingkungan — bukan supaya dashboard ini ditaruh di LAN.
		{"ISSBOARD_LISTEN", &c.Listen},
		{"ISSBOARD_NTFY_URL", &c.NtfyURL},
		{"ISSBOARD_NTFY_TOPIC", &c.NtfyTopic},
		{"ISSBOARD_NTFY_TOKEN", &c.NtfyToken},
		{"ISSBOARD_TELEGRAM_TOKEN", &c.TelegramToken},
		{"ISSBOARD_TELEGRAM_CHAT_ID", &c.TelegramChatID},
	} {
		if v := strings.TrimSpace(os.Getenv(e.key)); v != "" {
			*e.dst = v
		}
	}
}
