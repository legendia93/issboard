// issboard — dashboard kesehatan host, satu binary.
//
// Bagian baca terbuka; aksi (container, scrub, SMART, ARC) butuh login dan
// aksi root dikerjakan issboard-helper — issboard sendiri tidak pernah root.
//
// Dijalankan lewat systemd socket activation: yang enabled adalah
// issboard.socket, bukan issboard.service. Proses ini keluar sendiri setelah
// idle dan dinyalakan lagi oleh systemd saat ada koneksi berikutnya.
package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/legendia93/issboard/internal/api"
	"github.com/legendia93/issboard/internal/auth"
	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
)

//go:embed web
var webFS embed.FS

func main() {
	cfgPath := flag.String("config", "/etc/issboard.yaml", "berkas konfigurasi")
	demo := flag.Bool("demo", false, "sajikan data palsu; tidak menyentuh sistem sama sekali")
	webDir := flag.String("web", "", "layani berkas web dari folder ini, bukan dari yang ter-embed")
	setPw := flag.Bool("set-password", false, "atur kata sandi operator (jalankan sebagai root), lalu keluar")
	pwUser := flag.String("user", "admin", "nama operator untuk -set-password")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config %s: %v", *cfgPath, err)
	}
	if *setPw {
		if err := setPassword(cfg.AuthFile, *pwUser); err != nil {
			fmt.Fprintf(os.Stderr, "GAGAL: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *demo {
		cfg.Demo = true
	}
	if cfg.Demo {
		log.Printf("issboard: MODE DEMO — seluruh data palsu, sistem tidak disentuh")
	}

	lns, activated, err := listeners(cfg.Listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	for _, ln := range lns {
		if activated {
			log.Printf("issboard: socket dari systemd (%s)", ln.Addr())
		} else {
			log.Printf("issboard: listen sendiri di %s", ln.Addr())
		}
	}

	// 🔴 Idle-exit HANYA masuk akal di bawah socket activation.
	//
	// Tanpa systemd yang memegang socket, tidak ada apa pun yang menyalakan
	// proses ini lagi setelah ia keluar — dashboard-nya mati diam-diam dan
	// baru ketahuan saat dibuka. Di Alpine, Void, atau saat `go run` dipakai
	// untuk mengembangkan, itu bukan hemat sumber daya, itu kegagalan.
	//
	// Jadi timernya dinonaktifkan sendiri, bukan diserahkan ke pengguna untuk
	// mengingat menulis `idle_timeout: 0` di config.
	idleFor := cfg.IdleTimeout
	if !activated && idleFor > 0 {
		log.Printf("issboard: tanpa socket activation, idle_timeout %s diabaikan — "+
			"tidak ada yang akan menyalakan ulang kalau prosesnya keluar", idleFor)
		idleFor = 0
	}
	idle := newIdleTimer(idleFor)

	// Saat menggarap tampilan, berkas ter-embed berarti tiap perubahan CSS
	// butuh compile ulang. Flag ini melayaninya dari disk supaya cukup
	// refresh browser. Hanya untuk mengembangkan: yang dipasang di server
	// tetap satu binary tanpa berkas pendamping.
	var static fs.FS
	if *webDir != "" {
		static = os.DirFS(*webDir)
		log.Printf("issboard: melayani web dari %s (bukan yang ter-embed)", *webDir)
	} else {
		static, err = fs.Sub(webFS, "web")
		if err != nil {
			log.Fatalf("embed web: %v", err)
		}
	}

	apiSrv := api.New(cfg, collector.NewCache(), idle.touch)
	// Dipanggil sesudah halaman kelola menyimpan pengaturan. Flag -demo tetap
	// berlaku: memuat ulang berkas tidak boleh diam-diam mematikan mode demo
	// dan membuat halaman palsu mulai menyentuh sistem sungguhan.
	apiSrv.Reload = func() (config.Config, error) {
		c, err := config.Load(*cfgPath)
		c.Demo = c.Demo || *demo
		return c, err
	}

	srv := &http.Server{
		Handler:           apiSrv.Routes(newAssets(static, *webDir == "")),
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
			log.Printf("issboard: idle %s, keluar — socket tetap mendengarkan", idleFor)
		}
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()

	// Satu server, banyak listener: tiap alamat dilayani goroutine sendiri,
	// dan srv.Shutdown menutup semuanya sekaligus.
	var wg sync.WaitGroup
	for _, ln := range lns {
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("serve %s: %v", ln.Addr(), err)
			}
		}(ln)
	}
	wg.Wait()
}

// listeners memakai fd yang diserahkan systemd (socket activation), selain itu
// membuka sendiri supaya `go run` tetap bisa dipakai saat mengembangkan.
//
// 🔴 SEMUA fd yang diserahkan dipakai, bukan cuma yang pertama.
//
// Satu unit socket boleh punya beberapa ListenStream — dan itu bukan kasus
// tepi: begitu alamat tailnet ditambahkan di samping loopback, systemd
// menyerahkan DUA fd. Versi pertama kode ini hanya menerima LISTEN_FDS=1 dan
// jatuh ke jalur cadangan net.Listen untuk selain itu — ke alamat yang justru
// sedang dipegang systemd. Hasilnya "address already in use", proses keluar
// seketika, systemd menyalakannya lagi tiap koneksi, lalu socket-nya sendiri
// ikut gagal kena start limit. Dashboard mati total, dan penyebabnya tidak
// terlihat dari pesan systemd mana pun.
//
// Protokolnya kecil dan stabil, jadi diimplementasikan langsung daripada
// menarik dependensi — alasan yang sama dengan memilih Go: nol dependensi runtime.
func listeners(addr string) ([]net.Listener, bool, error) {
	n := activatedFDs()
	if n == 0 {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, false, err
		}
		return []net.Listener{ln}, false, nil
	}

	const firstFD = 3
	out := make([]net.Listener, 0, n)
	for i := 0; i < n; i++ {
		f := os.NewFile(uintptr(firstFD+i), "systemd-socket")
		ln, err := net.FileListener(f)
		_ = f.Close()
		if err != nil {
			return nil, true, fmt.Errorf("fd %d dari systemd: %w", firstFD+i, err)
		}
		out = append(out, ln)
	}
	return out, true, nil
}

// activatedFDs mengembalikan berapa socket yang diserahkan systemd ke proses
// INI, atau 0 kalau bukan socket activation. LISTEN_PID diperiksa karena
// variabel itu ikut terwarisi anak proses, dan anak yang salah mengira
// dirinya yang diaktifkan akan mengambil alih fd milik induknya.
func activatedFDs() int {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) {
		return 0
	}
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n < 1 {
		return 0
	}
	return n
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

// setPassword menulis berkas kredensial: hash PBKDF2 dan rahasia sesi BARU.
//
// Rahasia selalu diganti, jadi mengganti kata sandi sekaligus me-logout semua
// sesi yang ada — termasuk HP yang tertinggal dalam keadaan masuk.
//
// Berkasnya 0640 root:issboard: issboard boleh membaca untuk memeriksa login,
// tapi tidak bisa menulisnya, dan user lain tidak bisa membaca hash-nya.
func setPassword(path, name string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("jalankan sebagai root: sudo issboard -set-password")
	}
	gid := -1
	if g, err := user.LookupGroup("issboard"); err == nil {
		gid, _ = strconv.Atoi(g.Gid)
	} else {
		fmt.Fprintln(os.Stderr, "PERINGATAN: grup issboard tidak ada — berkas akan milik root saja, "+
			"dan dashboard tidak bisa membacanya sampai grupnya dibuat.")
	}

	pw, err := readSecret("Kata sandi baru: ")
	if err != nil {
		return err
	}
	if len([]rune(pw)) < 10 {
		return fmt.Errorf("kata sandi minimal 10 karakter")
	}
	again, err := readSecret("Ulangi: ")
	if err != nil {
		return err
	}
	if pw != again {
		return fmt.Errorf("kedua kata sandi tidak sama")
	}

	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := auth.Save(path, auth.Credentials{User: name, Hash: h, Secret: auth.NewSecret()}, gid); err != nil {
		return err
	}
	fmt.Printf("Tersimpan di %s untuk user %q. Semua sesi lama ter-logout.\n", path, name)
	return nil
}

// Satu pembaca untuk seluruh proses: pembaca baru per panggilan bisa menelan
// baris kedua ke penyangganya saat masukan disalurkan lewat pipa.
var stdin = bufio.NewReader(os.Stdin)

// readSecret membaca satu baris tanpa gema kalau stdin adalah terminal.
// Memakai stty, bukan pustaka terminal: janji nol dependensi tetap utuh, dan
// stty ada di tiap sistem yang punya terminal untuk diketik.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	echoOff := exec.Command("stty", "-echo")
	echoOff.Stdin = os.Stdin
	if echoOff.Run() == nil {
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			_ = on.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("tidak ada masukan: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
