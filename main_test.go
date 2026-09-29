package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// activatedFDs menentukan apakah proses ini menerima socket dari systemd.
// Salah membacanya berakibat fatal ke dua arah: mengira diaktifkan padahal
// tidak (mengambil fd yang bukan miliknya), atau mengira TIDAK diaktifkan
// padahal iya (lalu mencoba bind ke alamat yang sedang dipegang systemd,
// gagal, dan mati berulang sampai socket-nya ikut gagal).
func TestActivatedFDs(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())

	for _, c := range []struct {
		nama      string
		listenPID string
		listenFDs string
		mau       int
	}{
		{"tanpa socket activation", "", "", 0},
		{"satu socket", pid, "1", 1},
		{"dua socket — loopback + tailnet", pid, "2", 2},
		{"empat socket", pid, "4", 4},
		// LISTEN_PID ikut terwarisi anak proses. Anak yang salah mengira
		// dirinya yang diaktifkan akan mengambil alih fd milik induknya.
		{"LISTEN_PID milik proses lain", "999999", "2", 0},
		{"LISTEN_FDS bukan angka", pid, "banyak", 0},
		{"LISTEN_FDS nol", pid, "0", 0},
		{"LISTEN_FDS negatif", pid, "-1", 0},
	} {
		t.Run(c.nama, func(t *testing.T) {
			t.Setenv("LISTEN_PID", c.listenPID)
			t.Setenv("LISTEN_FDS", c.listenFDs)
			if got := activatedFDs(); got != c.mau {
				t.Errorf("mau %d, dapat %d", c.mau, got)
			}
		})
	}
}

// Tanpa socket activation, issboard harus membuka alamatnya sendiri —
// itulah yang membuat `go run` dan container tetap bisa dipakai.
func TestListenersTanpaActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")

	lns, activated, err := listeners("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()
	if activated {
		t.Error("tidak boleh mengaku socket-activated")
	}
	if len(lns) != 1 {
		t.Fatalf("mau 1 listener, dapat %d", len(lns))
	}
}

func TestAsetBertandaVersiIsinya(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte(`<link rel="stylesheet" href="style.css"><script src="app.js"></script><script src="hilang.js"></script>`)},
		"style.css":  {Data: []byte("body{}")},
		"app.js":     {Data: []byte("1")},
	}
	a := newAssets(fsys, false)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	body := get("/").Body.String()
	v1 := a.version("style.css")
	if !strings.Contains(body, `href="style.css?v=`+v1+`"`) || !strings.Contains(body, `src="app.js?v=`) {
		t.Fatalf("rujukan tidak bertanda versi: %s", body)
	}
	// Berkas yang tidak ada dibiarkan apa adanya, supaya 404-nya terlihat.
	if !strings.Contains(body, `src="hilang.js"`) {
		t.Errorf("rujukan ke berkas yang tidak ada ikut diubah: %s", body)
	}

	// Isi berubah → versi berubah. Inilah seluruh gunanya.
	fsys["style.css"] = &fstest.MapFile{Data: []byte("body{color:red}")}
	if body := get("/index.html").Body.String(); strings.Contains(body, v1) {
		t.Error("versi tidak berubah setelah isi berkas berubah")
	}

	if cc := get("/style.css?v=" + a.version("style.css")).Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("aset bertanda versi: Cache-Control %q", cc)
	}
	if cc := get("/style.css").Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Error("aset TANPA versi tidak boleh immutable")
	}
}
