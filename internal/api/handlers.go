// Package api melayani JSON read-only untuk v1.
//
// Router sengaja tidak dikunci ke GET saja: plan bagian 6 meminta bentuknya
// siap untuk endpoint bermutasi menyusul, tanpa harus dibongkar.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
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

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.cache.Collect(r.Context(), collector.Options{
		SmartCache:   s.cfg.SmartCache,
		DockerSocket: s.cfg.DockerSocket,
		Pools:        s.cfg.Pools,
	})
	writeJSON(w, http.StatusOK, snap)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
