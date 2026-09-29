package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/legendia93/issboard/internal/auth"
	"github.com/legendia93/issboard/internal/config"
)

func post(h http.Handler, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler, user, pw string) (cookie, csrf string, code int) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"user": user, "password": pw})
	w := post(h, "/api/v1/login", string(b), nil)
	if w.Code != http.StatusOK {
		return "", "", w.Code
	}
	var body struct{ Csrf string }
	json.Unmarshal(w.Body.Bytes(), &body)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Error("cookie sesi harus HttpOnly + SameSite=Strict")
			}
			return c.Name + "=" + c.Value, body.Csrf, w.Code
		}
	}
	t.Fatal("login berhasil tanpa cookie")
	return
}

func TestLoginDemo(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	h := srv(cfg)

	if _, _, code := login(t, h, "demo", "salah"); code != http.StatusUnauthorized {
		t.Fatalf("kata sandi salah: HTTP %d", code)
	}
	ck, csrf, code := login(t, h, "demo", "demo")
	if code != http.StatusOK || csrf == "" {
		t.Fatalf("login demo gagal: %d", code)
	}

	// /session dengan cookie menyerahkan token CSRF yang sama dengan login.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	r.Header.Set("Cookie", ck)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["authenticated"] != true || body["csrf"] != csrf {
		t.Errorf("sesi setelah login: %v", body)
	}

	// Login dari situs lain ditolak walau kata sandinya benar.
	b, _ := json.Marshal(map[string]string{"user": "demo", "password": "demo"})
	if w := post(h, "/api/v1/login", string(b), map[string]string{"Origin": "https://jahat.example"}); w.Code != http.StatusForbidden {
		t.Errorf("login origin asing: HTTP %d, harus 403", w.Code)
	}
}

func TestLoginDibatasi(t *testing.T) {
	cfg := config.Default()
	cfg.Demo = true
	h := srv(cfg)
	for i := 0; i < auth.NewLimiter().Max; i++ {
		login(t, h, "demo", "salah")
	}
	// Bahkan kata sandi yang BENAR ditahan: kalau tidak, pembatasnya cuma
	// memperlambat penebak yang kebetulan belum beruntung.
	if _, _, code := login(t, h, "demo", "demo"); code != http.StatusTooManyRequests {
		t.Errorf("setelah batas: HTTP %d, harus 429", code)
	}
}

func TestSesiMelaporkanBelumDiatur(t *testing.T) {
	cfg := config.Default()
	cfg.AuthFile = filepath.Join(t.TempDir(), "tidak-ada")
	_, body := ambil(t, srv(cfg), "/api/v1/session")
	if body["configured"] != false || body["authenticated"] != false {
		t.Errorf("sesi tanpa kredensial: %v", body)
	}
	if _, ada := body["csrf"]; ada {
		t.Error("token CSRF dibagikan tanpa sesi")
	}
}
