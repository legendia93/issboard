// Package api melayani JSON untuk dashboard.
//
// Bagian baca (GET) terbuka seperti sejak v1. Endpoint bermutasi yang akan
// datang SEMUANYA lewat s.mutate (session.go) — sesi, CSRF, audit — lebih dulu.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/legendia93/issboard/internal/auth"
	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
	"github.com/legendia93/issboard/internal/health"
	"github.com/legendia93/issboard/internal/history"
)

type Server struct {
	cfg   config.Config
	cache *collector.Cache
	// Touch dipanggil tiap request supaya pengatur idle tahu ada yang melihat.
	Touch func()

	limiter *auth.Limiter
	// demoCreds: di mode demo, login dengan demo/demo.
	// Rahasianya acak per proses — sesi demo tidak berarti apa-apa di luar.
	demoCreds auth.Credentials
}

func New(cfg config.Config, cache *collector.Cache, touch func()) *Server {
	if touch == nil {
		touch = func() {}
	}
	s := &Server{cfg: cfg, cache: cache, Touch: touch, limiter: auth.NewLimiter()}
	if cfg.Demo {
		// Iterasi kecil: kata sandinya memang tertulis di sini.
		h, _ := auth.HashPasswordIter("demo", 1000)
		s.demoCreds = auth.Credentials{User: "demo", Hash: h, Secret: auth.NewSecret()}
	}
	return s
}

func (s *Server) Routes(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/history", s.handleHistory)

	mux.HandleFunc("GET /api/v1/session", s.handleSession)
	mux.HandleFunc("POST /api/v1/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/logout", s.handleLogout)

	mux.Handle("/", static)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		// Dashboard read-only yang di-embed: tidak ada aset pihak ketiga.
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now()})
}

// statusResponse menyematkan Snapshot apa adanya lalu menambahkan vonis.
//
// Vonisnya dihitung di sini, bukan di JavaScript, supaya issboard-agent
// (fase 2) memakai aturan yang persis sama untuk mengirim notifikasi. Aturan
// yang disalin ke dua bahasa akan berbeda pelan-pelan, dan yang gagal duluan
// justru jalur alert — satu-satunya yang bekerja saat halaman tidak dibuka.
type statusResponse struct {
	collector.Snapshot
	Verdict  health.Verdict   `json:"verdict"`
	Findings []health.Finding `json:"findings"`
	Demo     bool             `json:"demo,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var snap collector.Snapshot
	if s.cfg.Demo {
		snap = collector.DemoSnapshot()
	} else {
		snap = s.cache.Collect(r.Context(), collector.Options{
			SmartCache:     s.cfg.SmartCache,
			DockerSocket:   s.cfg.DockerSocket,
			Pools:          s.cfg.Pools,
			SnapPolicyFile: s.cfg.SnapPolicyFile,
			SnapExempt:     s.cfg.SnapExempt,
		})
	}

	fs := health.Evaluate(snap)
	writeJSON(w, http.StatusOK, statusResponse{
		Snapshot: snap,
		Verdict:  health.Summarize(fs),
		Findings: fs,
		Demo:     s.cfg.Demo,
	})
}

// historyResponse menyertakan catatan, bukan cuma titik-titiknya.
//
// Grafik kosong punya dua arti yang sangat berbeda — "mesin ini baru dipasang"
// dan "timer agent-nya mati" — dan tanpa catatan ini keduanya terlihat sama:
// halaman yang tenang. Itu persis bentuk kebutaan yang dihindari di seluruh
// proyek ini (design.md §5).
type historyResponse struct {
	history.File
	Note string `json:"note,omitempty"`
}

// handleHistory MEMBACA berkas yang ditulis issboard-agent. Ia tidak
// mengumpulkan apa pun dan tidak pernah menulis: persis pola cache SMART.
//
// Kalau agent-nya mati, satu-satunya tempat hal itu bisa ketahuan adalah di
// sini. Agent tidak bisa mengabari bahwa dirinya sendiri berhenti jalan.
func (s *Server) handleHistory(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Demo {
		writeJSON(w, http.StatusOK, historyResponse{File: history.DemoFile()})
		return
	}

	h, err := history.Load(s.cfg.HistoryFile)
	resp := historyResponse{File: h}
	switch {
	case err != nil:
		resp.Note = "riwayat tidak terbaca: " + err.Error()
	case h.WrittenAt.IsZero():
		resp.Note = "belum ada riwayat — apakah issboard-agent.timer sudah aktif?"
	case time.Since(h.WrittenAt) > 5*time.Minute:
		// Agent menulis tiap menit. Lebih dari lima menit berarti timernya
		// mati, dan itu temuan tersendiri — bukan sekadar grafik yang pendek.
		resp.Note = "riwayat berhenti " + h.WrittenAt.Format("15:04") +
			" — periksa issboard-agent.timer"
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
