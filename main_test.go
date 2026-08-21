package main

import (
	"os"
	"strconv"
	"testing"
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
