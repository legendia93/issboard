package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tulis(t *testing.T, isi string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "issboard.yaml")
	if err := os.WriteFile(p, []byte(isi), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKunciNilai(t *testing.T) {
	c, err := Load(tulis(t, `
# komentar diabaikan
listen: 127.0.0.1:8080
idle_timeout: 90s
smart_cache: /tmp/smart.json
pools: satu, dua ,  tiga
alert_repeat: 6h
notify_min_level: crit
ntfy_topic: "topik-berkutip"
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" {
		t.Errorf("listen: %q", c.Listen)
	}
	if c.IdleTimeout != 90*time.Second {
		t.Errorf("idle_timeout: %v", c.IdleTimeout)
	}
	if c.AlertRepeat != 6*time.Hour {
		t.Errorf("alert_repeat: %v", c.AlertRepeat)
	}
	if c.NotifyMinLevel != "crit" {
		t.Errorf("notify_min_level: %q", c.NotifyMinLevel)
	}
	// Tanda kutip dibuang, spasi di sekitar koma juga.
	if c.NtfyTopic != "topik-berkutip" {
		t.Errorf("ntfy_topic: %q", c.NtfyTopic)
	}
	if len(c.Pools) != 3 || c.Pools[2] != "tiga" {
		t.Errorf("pools: %q", c.Pools)
	}
}

// Nilai yang mengandung ":" — URL dan alamat — tidak boleh terpotong.
func TestLoadNilaiBerisiTitikDua(t *testing.T) {
	c, _ := Load(tulis(t, "ntfy_url: https://ntfy.contoh/tanpa:port\nlisten: [::1]:9955\n"))
	if c.NtfyURL != "https://ntfy.contoh/tanpa:port" {
		t.Errorf("URL terpotong: %q", c.NtfyURL)
	}
	if c.Listen != "[::1]:9955" {
		t.Errorf("alamat IPv6 terpotong: %q", c.Listen)
	}
}

// 🔴 Berkas config yang tidak ada BUKAN error: issboard harus tetap hidup
// justru saat sistemnya kacau.
func TestLoadBerkasTidakAda(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "tidak-ada.yaml"))
	if err != nil {
		t.Fatalf("berkas hilang tidak boleh menggagalkan: %v", err)
	}
	if c.Listen != Default().Listen || c.HistoryFile != Default().HistoryFile {
		t.Errorf("bawaan tidak dipakai: %+v", c)
	}
}

func TestLoadBarisNgawurDilewati(t *testing.T) {
	c, err := Load(tulis(t, "baris tanpa titik dua\nlisten: 127.0.0.1:1234\nidle_timeout: bukan-durasi\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:1234" {
		t.Errorf("baris sah setelah baris ngawur ikut hilang: %q", c.Listen)
	}
	// Durasi ngawur jatuh ke bawaan, bukan ke 0 yang berarti "jangan pernah keluar".
	if c.IdleTimeout != Default().IdleTimeout {
		t.Errorf("durasi ngawur harus jatuh ke bawaan, dapat %v", c.IdleTimeout)
	}
}

// 🔴 Token lewat lingkungan adalah jalur yang dianjurkan: /etc/issboard.yaml
// boleh dibaca siapa saja, sedangkan EnvironmentFile systemd bisa 0600.
func TestEnvMenimpaBerkas(t *testing.T) {
	t.Setenv("ISSBOARD_NTFY_TOPIC", "dari-lingkungan")
	t.Setenv("ISSBOARD_TELEGRAM_TOKEN", "rahasia")

	c, err := Load(tulis(t, "ntfy_topic: dari-berkas\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.NtfyTopic != "dari-lingkungan" {
		t.Errorf("lingkungan harus menimpa berkas: %q", c.NtfyTopic)
	}
	if c.TelegramToken != "rahasia" {
		t.Errorf("token dari lingkungan tidak terbaca: %q", c.TelegramToken)
	}
}

// Berlaku juga saat berkasnya tidak ada — pemasangan lewat Docker/tarball
// sering hanya memakai variabel lingkungan.
func TestEnvTanpaBerkasConfig(t *testing.T) {
	t.Setenv("ISSBOARD_NTFY_TOPIC", "cuma-lingkungan")
	c, _ := Load(filepath.Join(t.TempDir(), "tidak-ada.yaml"))
	if c.NtfyTopic != "cuma-lingkungan" {
		t.Errorf("lingkungan diabaikan saat berkas tidak ada: %q", c.NtfyTopic)
	}
}

// Variabel kosong tidak boleh menghapus nilai yang sudah benar di berkas:
// EnvironmentFile yang barisnya masih dikomentari akan terbaca kosong.
func TestEnvKosongTidakMenghapus(t *testing.T) {
	t.Setenv("ISSBOARD_NTFY_TOPIC", "")
	c, _ := Load(tulis(t, "ntfy_topic: dari-berkas\n"))
	if c.NtfyTopic != "dari-berkas" {
		t.Errorf("variabel kosong menghapus nilai berkas: %q", c.NtfyTopic)
	}
}

// Dipakai container: di sana tidak ada berkas config yang enak disunting,
// dan alamat bawaan 127.0.0.1 membuat prosesnya tak terjangkau dari luar
// namespace-nya sendiri.
func TestEnvListen(t *testing.T) {
	t.Setenv("ISSBOARD_LISTEN", "0.0.0.0:9955")
	c, _ := Load(tulis(t, "listen: 127.0.0.1:9955\n"))
	if c.Listen != "0.0.0.0:9955" {
		t.Errorf("ISSBOARD_LISTEN diabaikan: %q", c.Listen)
	}
}

func TestDefaultMasukAkal(t *testing.T) {
	d := Default()
	if d.HistoryFile == "" || d.AlertState == "" {
		t.Error("jalur riwayat/state harus punya bawaan")
	}
	if d.AlertRepeat <= 0 {
		t.Error("alert_repeat bawaan harus positif")
	}
	if d.NotifyMinLevel != "warn" {
		t.Errorf("bawaan notify_min_level harus warn, dapat %q", d.NotifyMinLevel)
	}
}
