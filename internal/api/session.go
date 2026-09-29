package api

// Sesi dan pembungkus aksi (fase 7).
//
// Aturan dari design.md §8 yang ditagih di sini: begitu ada satu endpoint yang
// bermutasi, autentikasi wajib LEBIH DULU. Setiap aksi didaftarkan lewat
// s.mutate, yang menolak tanpa sesi sebelum menyentuh apa pun.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/legendia93/issboard/internal/auth"
)

const cookieName = "issboard_session"

// badRequest menandai kesalahan dari pihak peminta (400), dibedakan dari
// kegagalan sistem di belakangnya (502).
type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

func bad(format string, a ...any) error { return badRequest{fmt.Sprintf(format, a...)} }

// unavailable menandai bagian di belakang aksi yang tidak terjangkau sama
// sekali (503) — mis. helper root belum terpasang — dibedakan dari aksi yang
// dijalankan lalu gagal (502).
type unavailable struct{ err error }

func (e unavailable) Error() string { return e.err.Error() }
func (e unavailable) Unwrap() error { return e.err }

func (s *Server) creds() (auth.Credentials, error) {
	if s.cfg.Demo {
		return s.demoCreds, nil
	}
	return auth.Load(s.cfg.AuthFile)
}

func (s *Server) session(r *http.Request) (auth.Credentials, auth.Session, bool) {
	c, err := s.creds()
	if err != nil {
		return c, auth.Session{}, false
	}
	ck, err := r.Cookie(cookieName)
	if err != nil {
		return c, auth.Session{}, false
	}
	sess, ok := c.ParseSession(ck.Value, time.Now())
	return c, sess, ok
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// sameOrigin menolak permintaan yang JELAS datang dari situs lain. Header ini
// dikirim browser modern dan tidak bisa dipalsukan halaman lain; permintaan
// tanpa keduanya (curl, test) tetap harus membawa token CSRF.
func sameOrigin(r *http.Request) bool {
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// ---------- sesi ----------

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	_, err := s.creds()
	out := map[string]any{"configured": err == nil, "authenticated": false, "demo": s.cfg.Demo}
	if err != nil && !errors.Is(err, auth.ErrNotConfigured) {
		// Berkas ada tapi rusak atau tak terbaca: itu harus terlihat, bukan
		// menyamar jadi "belum diatur".
		out["error"] = err.Error()
	}
	if c, sess, ok := s.session(r); ok {
		out["authenticated"] = true
		out["user"] = sess.User
		out["expires"] = sess.Expires
		// Token CSRF hanya bisa dibaca halaman se-asal: SOP browser mencegah
		// situs lain membaca jawaban ini, jadi di sinilah aman menyerahkannya.
		out["csrf"] = c.CSRF(sess)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "asal permintaan ditolak"})
		return
	}
	c, err := s.creds()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false,
			"error": "autentikasi belum diatur — jalankan `sudo issboard -set-password` di host"})
		return
	}

	ip := clientIP(r)
	now := time.Now()
	if ok, wait := s.limiter.Allowed(ip, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		log.Printf("audit: login DITAHAN ip=%s (terlalu banyak gagal)", ip)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false,
			"error": fmt.Sprintf("terlalu banyak percobaan gagal — coba lagi %d menit lagi", int(wait.Minutes())+1)})
		return
	}

	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "permintaan tidak terbaca"})
		return
	}
	if !c.Verify(body.User, body.Password) {
		s.limiter.Fail(ip, now)
		log.Printf("audit: login GAGAL user=%q ip=%s", body.User, ip)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "nama atau kata sandi salah"})
		return
	}
	s.limiter.Reset(ip)

	val, sess := c.NewSession(now)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: val, Path: "/",
		MaxAge:   int(auth.SessionTTL.Seconds()),
		HttpOnly: true,
		// Strict: cookie tidak ikut pada permintaan yang dipicu situs lain,
		// termasuk navigasi biasa. Menutup sebagian besar CSRF sendirian.
		SameSite: http.SameSiteStrictMode,
		// Secure hanya kalau sambungannya memang HTTPS. Tailnet melayani
		// HTTP polos (terenkripsi WireGuard di bawahnya), dan cookie Secure
		// di HTTP polos tidak pernah disimpan browser — login akan "berhasil"
		// lalu langsung lupa.
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	log.Printf("audit: login ok user=%q ip=%s", sess.User, ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": sess.User, "csrf": c.CSRF(sess)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "asal permintaan ditolak"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- pembungkus aksi ----------

// mutate adalah SATU-SATUNYA jalan menuju aksi. Urutannya: asal → sesi →
// CSRF → audit "mulai" → aksi → audit "hasil". Tidak ada handler aksi yang
// didaftarkan tanpa lewat sini, dan test menguncinya.
func (s *Server) mutate(action string, h func(r *http.Request, actor string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "asal permintaan ditolak"})
			return
		}
		c, sess, ok := s.session(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "perlu masuk dulu"})
			return
		}
		if !c.CheckCSRF(sess, r.Header.Get("X-CSRF-Token")) {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "token CSRF tidak cocok — muat ulang halaman"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)

		ip := clientIP(r)
		target := r.PathValue("name")
		// Dicatat SEBELUM dijalankan: kalau aksinya menggantung atau prosesnya
		// terbunuh, jejak bahwa ia diminta — dan oleh siapa — tetap ada.
		log.Printf("audit: mulai user=%q ip=%s aksi=%s target=%q", sess.User, ip, action, target)
		out, err := h(r, sess.User)
		if err != nil {
			log.Printf("audit: GAGAL user=%q ip=%s aksi=%s target=%q: %v", sess.User, ip, action, target, err)
			code := http.StatusBadGateway
			var br badRequest
			if errors.As(err, &br) {
				code = http.StatusBadRequest
			} else if errors.As(err, new(unavailable)) {
				code = http.StatusServiceUnavailable
			}
			writeJSON(w, code, map[string]any{"ok": false, "error": err.Error(), "output": out})
			return
		}
		log.Printf("audit: ok user=%q ip=%s aksi=%s target=%q", sess.User, ip, action, target)
		s.cache.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
	}
}
