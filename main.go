// issboard — dashboard kesehatan host, satu binary, read-only (v1).
//
// Dijalankan lewat systemd socket activation: yang enabled adalah
// issboard.socket, bukan issboard.service. Proses ini keluar sendiri setelah
// idle dan dinyalakan lagi oleh systemd saat ada koneksi berikutnya.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/legendia93/issboard/internal/api"
	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
)

//go:embed web
var webFS embed.FS

func main() {
	cfgPath := flag.String("config", "/etc/issboard.yaml", "berkas konfigurasi")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config %s: %v", *cfgPath, err)
	}

	ln, activated, err := listener(cfg.Listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if activated {
		log.Printf("issboard: socket dari systemd (%s)", ln.Addr())
	} else {
		log.Printf("issboard: listen sendiri di %s", ln.Addr())
	}

	idle := newIdleTimer(cfg.IdleTimeout)
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}

	srv := &http.Server{
		Handler:           api.New(cfg, collector.NewCache(), idle.touch).Routes(http.FileServerFS(static)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		select {
		case <-ctx.Done():
			log.Print("issboard: sinyal berhenti")
		case <-idle.expired():
			// Ini jalur normal, bukan kegagalan: systemd akan menyalakan
			// ulang lewat socket saat halaman dibuka lagi.
			log.Printf("issboard: idle %s, keluar — socket tetap mendengarkan", cfg.IdleTimeout)
		}
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// listener memakai fd 3 kalau systemd yang menyerahkannya (socket activation),
// selain itu membuka sendiri supaya `go run` tetap bisa dipakai saat mengembangkan.
//
// Protokolnya kecil dan stabil, jadi diimplementasikan langsung daripada
// menarik dependensi — alasan yang sama dengan memilih Go: nol dependensi runtime.
func listener(addr string) (net.Listener, bool, error) {
	if os.Getenv("LISTEN_PID") == "" || os.Getenv("LISTEN_FDS") != "1" {
		ln, err := net.Listen("tcp", addr)
		return ln, false, err
	}
	if pid := os.Getpid(); os.Getenv("LISTEN_PID") != itoa(pid) {
		ln, err := net.Listen("tcp", addr)
		return ln, false, err
	}
	const firstFD = 3
	f := os.NewFile(firstFD, "systemd-socket")
	ln, err := net.FileListener(f)
	if err != nil {
		return nil, false, err
	}
	_ = f.Close()
	return ln, true, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// idleTimer memberi tahu saat tidak ada request selama d.
type idleTimer struct {
	mu   sync.Mutex
	d    time.Duration
	t    *time.Timer
	done chan struct{}
	once sync.Once
}

func newIdleTimer(d time.Duration) *idleTimer {
	it := &idleTimer{d: d, done: make(chan struct{})}
	if d <= 0 {
		return it // 0 = jangan pernah keluar sendiri
	}
	it.t = time.AfterFunc(d, it.fire)
	return it
}

func (it *idleTimer) touch() {
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.t != nil {
		it.t.Reset(it.d)
	}
}

func (it *idleTimer) fire() { it.once.Do(func() { close(it.done) }) }

func (it *idleTimer) expired() <-chan struct{} { return it.done }
