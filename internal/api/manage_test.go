package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/legendia93/issboard/internal/config"
)

// Semua endpoint bermutasi. Menambah endpoint baru TANPA menambahkannya di
// sini berarti ia tidak ikut dikunci — jadi daftar ini sengaja ditulis tangan.
var mutasi = []struct{ path, body string }{
	{"/api/v1/containers/web/stop", ""},
	{"/api/v1/containers/web/start", ""},
	{"/api/v1/containers/web/restart", ""},
	{"/api/v1/containers/web/remove", ""},
	{"/api/v1/pools/kolam/scrub/start", ""},
	{"/api/v1/pools/kolam/scrub/stop", ""},
	{"/api/v1/smart/short", `{"device":"/dev/sda"}`},
	{"/api/v1/smart/refresh", ""},
	{"/api/v1/arc", `{"max_bytes":0}`},
}

// Kunci terpenting fase 7: tanpa sesi harus 401, bukan 200 yang diam-diam
// bekerja. Diuji di mode demo DAN mode sungguhan tanpa berkas kredensial.
func TestMutasiTanpaSesi401(t *testing.T) {
	demo := config.Default()
	demo.Demo = true
	nyata := config.Default()
	nyata.AuthFile = filepath.Join(t.TempDir(), "tidak-ada")

	for nama, cfg := range map[string]config.Config{"demo": demo, "tanpa-kredensial": nyata} {
		h := srv(cfg)
		for _, m := range mutasi {
			if w := post(h, m.path, m.body, nil); w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s: HTTP %d, harus 401", nama, m.path, w.Code)
			}
		}
	}
}

// GET ke endpoint aksi tidak boleh menjalankan apa pun: prefetch browser dan
// crawler memakai GET.
func TestAksiBukanGET(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	h := srv(cfg)
	for _, m := range mutasi {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, m.path, nil))
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), `"ok": true`) {
			t.Errorf("GET %s menjalankan aksi", m.path)
		}
	}
}

func TestAksiDenganSesiDemo(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	h := srv(cfg)

	ck, csrf, code := login(t, h, "demo", "demo")
	if code != http.StatusOK {
		t.Fatalf("login demo gagal: %d", code)
	}
	// Sesi tanpa token CSRF: ditolak.
	if w := post(h, "/api/v1/containers/web/stop", "", map[string]string{"Cookie": ck}); w.Code != http.StatusForbidden {
		t.Errorf("tanpa CSRF: HTTP %d, harus 403", w.Code)
	}
	// Dari situs lain, walau membawa token: ditolak.
	if w := post(h, "/api/v1/containers/web/stop", "", map[string]string{
		"Cookie": ck, "X-CSRF-Token": csrf, "Origin": "https://jahat.example"}); w.Code != http.StatusForbidden {
		t.Errorf("origin asing: HTTP %d, harus 403", w.Code)
	}

	ok := map[string]string{"Cookie": ck, "X-CSRF-Token": csrf}
	for _, m := range mutasi {
		if w := post(h, m.path, m.body, ok); w.Code != http.StatusOK {
			t.Errorf("%s dengan sesi: HTTP %d %s", m.path, w.Code, w.Body.String())
		}
	}
	// Aksi yang tidak dikenal tetap 400 walau sudah masuk.
	if w := post(h, "/api/v1/containers/web/exec", "", ok); w.Code != http.StatusBadRequest {
		t.Errorf("aksi tak dikenal: HTTP %d, harus 400", w.Code)
	}
	if w := post(h, "/api/v1/arc", `{"max_bytes":1}`, ok); w.Code != http.StatusBadRequest {
		t.Errorf("ARC di luar rentang: HTTP %d, harus 400", w.Code)
	}
}
