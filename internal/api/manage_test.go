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
	{"/api/v1/snapshots", `{"dataset":"pool-cepat/app","tag":"uji"}`},
	{"/api/v1/snapshots/destroy", `{"snapshot":"pool-cepat/app@issboard_x"}`},
	{"/api/v1/datasets", `{"parent":"pool-cepat","name":"baru","props":{"compression":"zstd"}}`},
	{"/api/v1/datasets/props", `{"dataset":"pool-cepat/app","prop":"atime","value":"off"}`},
	{"/api/v1/sanoid/run", ""},
	{"/api/v1/settings/notify", `{"values":{"ISSBOARD_TELEGRAM_CHAT_ID":"-100123"}}`},
	{"/api/v1/settings/notify/test", ""},
	{"/api/v1/settings/app", `{"values":{"alert_repeat":"6h"}}`},
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
	for _, m := range []struct{ path, body string }{
		{"/api/v1/arc", `{"max_bytes":1}`},
		{"/api/v1/snapshots", `{"dataset":"pool-cepat/tidak-ada"}`},
		{"/api/v1/snapshots", `{"dataset":"pool-cepat/app","tag":"-r"}`},
		{"/api/v1/snapshots/destroy", `{"snapshot":"pool-cepat/app"}`},
		{"/api/v1/datasets", `{"parent":"pool-cepat","name":"../x"}`},
		{"/api/v1/datasets", `{"parent":"pool-cepat","name":"x","props":{"mountpoint":"/"}}`},
		{"/api/v1/datasets/props", `{"dataset":"pool-cepat/app","prop":"mountpoint","value":"/"}`},
		{"/api/v1/settings/notify", `{"values":{"ISSBOARD_TELEGRAM_CHAT_ID":"-1\nLD_PRELOAD=x"}}`},
		{"/api/v1/settings/notify", `{"values":{"PATH":"/tmp"}}`},
		{"/api/v1/settings/app", `{"values":{"history_file":"/tmp/x"}}`},
		{"/api/v1/settings/app", `{"values":{}}`},
	} {
		if w := post(h, m.path, m.body, ok); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: HTTP %d, harus 400", m.path, m.body, w.Code)
		}
	}
}

// Keadaan kredensial bukan untuk siapa pun yang bisa membuka dashboard baca.
func TestPengaturanButuhSesi(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	h := srv(cfg)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("tanpa sesi: HTTP %d, harus 401", w.Code)
	}

	ck, _, _ := login(t, h, "demo", "demo")
	r := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	r.Header.Set("Cookie", ck)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"alert_repeat"`) ||
		!strings.Contains(w.Body.String(), "ISSBOARD_TELEGRAM_TOKEN") {
		t.Errorf("dengan sesi: HTTP %d %s", w.Code, w.Body.String())
	}
}
