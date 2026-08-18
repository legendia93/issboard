// Package api melayani JSON read-only untuk v1.
//
// Router sengaja tidak dikunci ke GET saja: docs/design.md §6 meminta bentuknya
// siap untuk endpoint bermutasi menyusul, tanpa harus dibongkar.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
	"github.com/legendia93/issboard/internal/health"
)

type Server struct {
	cfg   config.Config
	cache *collector.Cache
	// Touch dipanggil tiap request supaya pengatur idle tahu ada yang melihat.
	Touch func()
}

func New(cfg config.Config, cache *collector.Cache, touch func()) *Server {
	if touch == nil {
		touch = func() {}
	}
	return &Server{cfg: cfg, cache: cache, Touch: touch}
}

func (s *Server) Routes(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
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
			SmartCache:   s.cfg.SmartCache,
			DockerSocket: s.cfg.DockerSocket,
			Pools:        s.cfg.Pools,
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
