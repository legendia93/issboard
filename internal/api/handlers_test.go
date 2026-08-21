package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
)

func srv(cfg config.Config) http.Handler {
	return New(cfg, collector.NewCache(), nil).Routes(http.NotFoundHandler())
}

func ambil(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s bukan JSON sah: %v", path, err)
	}
	return w, body
}

// Vonis dihitung di SERVER, bukan di JavaScript, supaya agent memakai aturan
// yang persis sama. Kalau field ini hilang, dashboard dan alert mulai
// berbeda pelan-pelan.
func TestStatusMemuatVonis(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true

	w, body := ambil(t, srv(cfg), "/api/v1/status")
	if w.Code != http.StatusOK {
		t.Fatalf("status HTTP %d", w.Code)
	}
	for _, k := range []string{"verdict", "findings", "host", "pools", "smart", "containers"} {
		if _, ada := body[k]; !ada {
			t.Errorf("field %q hilang dari /status", k)
		}
	}
	if body["demo"] != true {
		t.Error("mode demo harus ditandai di responsnya")
	}
}

func TestHeaderKeamanan(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	w, _ := ambil(t, srv(cfg), "/api/v1/status")

	// CSP inilah yang membuat "tidak ada aset dari luar" jadi jaminan, bukan
	// sekadar niat — termasuk melarang script inline.
	if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("CSP salah: %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff hilang")
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("referrer-policy hilang")
	}
}

func TestHealthTidakMengumpulkanApaPun(t *testing.T) {
	w, body := ambil(t, srv(config.Default()), "/api/v1/health")
	if w.Code != http.StatusOK || body["ok"] != true {
		t.Errorf("liveness gagal: %d %v", w.Code, body)
	}
}

func TestHistoryDemo(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true

	_, body := ambil(t, srv(cfg), "/api/v1/history")
	if _, ada := body["fine"]; !ada {
		t.Fatalf("lapis halus hilang: %v", body)
	}
	if _, ada := body["coarse"]; !ada {
		t.Error("lapis kasar hilang")
	}
	if body["note"] != nil {
		t.Errorf("riwayat demo tidak boleh memberi catatan masalah: %v", body["note"])
	}
}

// 🔴 Grafik kosong punya dua arti — mesin baru, atau timer agent mati — dan
// keduanya sama-sama menghasilkan halaman yang tenang. Catatan inilah yang
// membedakannya, dan agent tidak bisa mengabari bahwa dirinya sendiri mati.
func TestHistoryBelumAdaMemberiCatatan(t *testing.T) {
	cfg := config.Default()
	cfg.HistoryFile = filepath.Join(t.TempDir(), "belum-ada.json")

	_, body := ambil(t, srv(cfg), "/api/v1/history")
	if body["note"] == nil {
		t.Error("riwayat yang belum ada harus memberi catatan, bukan diam")
	}
}

func TestHistoryBasiMemberiCatatan(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.json")
	lama := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	os.WriteFile(p, []byte(`{"written_at":"`+lama+`","fine":{"step_seconds":60,"points":[]}}`), 0o644)

	cfg := config.Default()
	cfg.HistoryFile = p

	_, body := ambil(t, srv(cfg), "/api/v1/history")
	if body["note"] == nil {
		t.Error("riwayat yang berhenti 2 jam lalu harus memberi catatan")
	}
}

// Endpoint riwayat MEMBACA berkas dan tidak mengumpulkan apa pun. Kalau ia
// sampai memanggil zpool atau smartctl, janji terbesar desain ini bocor.
func TestHistoryTidakMenulisBerkas(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.HistoryFile = filepath.Join(dir, "history.json")

	ambil(t, srv(cfg), "/api/v1/history")
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Errorf("endpoint riwayat menulis sesuatu: %v", ents)
	}
}
