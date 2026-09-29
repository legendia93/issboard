package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func creds(t *testing.T) Credentials {
	t.Helper()
	h, err := HashPasswordIter("rahasia-panjang", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return Credentials{User: "admin", Hash: h, Secret: NewSecret()}
}

func TestVerify(t *testing.T) {
	c := creds(t)
	if !c.Verify("admin", "rahasia-panjang") {
		t.Error("kata sandi benar ditolak")
	}
	for _, k := range [][2]string{{"admin", "salah"}, {"root", "rahasia-panjang"}, {"", ""}} {
		if c.Verify(k[0], k[1]) {
			t.Errorf("%q/%q diterima", k[0], k[1])
		}
	}
}

func TestSesiBertandaTangan(t *testing.T) {
	c := creds(t)
	now := time.Now()
	val, s := c.NewSession(now)

	if got, ok := c.ParseSession(val, now); !ok || got.User != "admin" {
		t.Fatal("sesi sah ditolak")
	}
	// Sesi harus selamat dari restart proses: tidak ada state di memori,
	// jadi kredensial yang dimuat ulang dari berkas tetap menerimanya.
	c2 := Credentials{User: c.User, Hash: c.Hash, Secret: append([]byte(nil), c.Secret...)}
	if _, ok := c2.ParseSession(val, now); !ok {
		t.Error("sesi hilang setelah kredensial dimuat ulang — idle-exit akan me-logout orang")
	}
	if _, ok := c.ParseSession(val, now.Add(SessionTTL+time.Second)); ok {
		t.Error("sesi kedaluwarsa diterima")
	}
	// Rahasia baru (set-password) me-logout semua orang.
	c3 := c
	c3.Secret = NewSecret()
	if _, ok := c3.ParseSession(val, now); ok {
		t.Error("sesi lama diterima setelah rahasia diganti")
	}
	// Payload yang diubah tanpa tanda tangan baru harus ditolak.
	p, m, _ := strings.Cut(val, ".")
	if _, ok := c.ParseSession(p+"x."+m, now); ok {
		t.Error("payload yang dirusak diterima")
	}
	if !c.CheckCSRF(s, c.CSRF(s)) || c.CheckCSRF(s, "") || c.CheckCSRF(s, "00") {
		t.Error("pemeriksaan CSRF salah")
	}
}

func TestSimpanMuat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth")
	if _, err := Load(p); err != ErrNotConfigured {
		t.Fatalf("berkas tidak ada harus ErrNotConfigured, dapat %v", err)
	}
	c := creds(t)
	if err := Save(p, c, -1); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o640 {
		t.Errorf("mode %o, harus 0640", st.Mode().Perm())
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verify("admin", "rahasia-panjang") {
		t.Error("kredensial yang dimuat tidak memverifikasi")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Now()
	for i := 0; i < l.Max; i++ {
		if ok, _ := l.Allowed("1.2.3.4", now); !ok {
			t.Fatalf("ditahan terlalu dini pada percobaan %d", i)
		}
		l.Fail("1.2.3.4", now)
	}
	if ok, _ := l.Allowed("1.2.3.4", now); ok {
		t.Error("tidak ditahan setelah batas")
	}
	if ok, _ := l.Allowed("5.6.7.8", now); !ok {
		t.Error("alamat lain ikut ditahan")
	}
	if ok, _ := l.Allowed("1.2.3.4", now.Add(l.Window)); !ok {
		t.Error("masih ditahan setelah jendela lewat")
	}
}
