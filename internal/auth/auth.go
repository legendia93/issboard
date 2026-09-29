// Package auth adalah autentikasi satu operator untuk endpoint bermutasi.
//
// 🔴 Sesi TIDAK disimpan di memori. issboard keluar sendiri setelah idle
// (socket activation), dan session store di memori berarti semua orang
// ter-logout tiap kali halaman ditinggal lima menit — yang akan terlihat
// seperti bug acak, bukan akibat desain. Sesinya stateless: cookie yang
// ditandatangani HMAC dengan rahasia dari berkas kredensial. RAM idle tetap
// 0 MB, dan tidak ada berkas state baru.
//
// Nol dependensi di luar pustaka standar: crypto/pbkdf2 dan crypto/hmac.
package auth

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Iterasi PBKDF2-SHA256 sesuai anjuran OWASP. Biayanya ~0,2 detik per
// percobaan login — sengaja: itu juga yang membuat tebakan massal mahal.
const Iterations = 600_000

// SessionTTL: berapa lama satu login berlaku. Tidak diperpanjang otomatis;
// operator yang sama akan login lagi besok, dan itu harga yang murah.
const SessionTTL = 12 * time.Hour

var ErrNotConfigured = errors.New("autentikasi belum diatur")

// Credentials adalah isi berkas kredensial (bawaan /etc/issboard/auth,
// 0640 root:issboard). Formatnya "kunci: nilai", sama dengan config.
type Credentials struct {
	User   string
	Hash   string // pbkdf2-sha256$<iterasi>$<garam b64>$<hash b64>
	Secret []byte // kunci HMAC sesi; mengganti ini me-logout semua orang
}

func Load(path string) (Credentials, error) {
	var c Credentials
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, ErrNotConfigured
		}
		return c, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.HasPrefix(strings.TrimSpace(k), "#") {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "user":
			c.User = v
		case "password":
			c.Hash = v
		case "secret":
			c.Secret, err = hex.DecodeString(v)
			if err != nil {
				return c, fmt.Errorf("%s: secret bukan hex: %w", path, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	if c.User == "" || c.Hash == "" || len(c.Secret) < 32 {
		return c, fmt.Errorf("%s tidak lengkap (butuh user, password, secret ≥32 byte)", path)
	}
	return c, nil
}

// Save menulis berkas kredensial secara atomik. gid < 0 = biarkan grupnya.
func Save(path string, c Credentials, gid int) error {
	var b bytes.Buffer
	b.WriteString("# Ditulis `issboard -set-password`. Jangan disunting tangan.\n")
	b.WriteString("# Mengganti secret me-logout semua sesi.\n")
	fmt.Fprintf(&b, "user: %s\npassword: %s\nsecret: %s\n", c.User, c.Hash, hex.EncodeToString(c.Secret))

	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if gid >= 0 {
		if err := tmp.Chown(0, gid); err != nil {
			tmp.Close()
			return err
		}
	}
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func NewSecret() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

func HashPassword(pw string) (string, error) { return HashPasswordIter(pw, Iterations) }

// HashPasswordIter hanya untuk mode demo dan test, di mana kata sandinya
// memang bukan rahasia dan 600 ribu iterasi cuma memperlambat.
func HashPasswordIter(pw string, iter int) (string, error) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	k, err := pbkdf2.Key(sha256.New, pw, salt, iter, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iter, enc.EncodeToString(salt), enc.EncodeToString(k)), nil
}

// Verify membandingkan dalam waktu konstan, dan menghitung hash BAHKAN kalau
// nama user-nya salah — kalau tidak, lama jawaban membocorkan nama user.
func (c Credentials) Verify(user, pw string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(c.User)) == 1
	p := strings.Split(c.Hash, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(p[2])
	want, err2 := enc.DecodeString(p[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1 && userOK
}

// Session adalah isi cookie yang sudah diverifikasi.
type Session struct {
	User    string
	Expires time.Time
	nonce   string
}

// NewSession membuat nilai cookie: payload.mac, keduanya base64url.
func (c Credentials) NewSession(now time.Time) (string, Session) {
	n := make([]byte, 16)
	_, _ = rand.Read(n)
	s := Session{User: c.User, Expires: now.Add(SessionTTL), nonce: hex.EncodeToString(n)}
	payload := s.User + "\n" + strconv.FormatInt(s.Expires.Unix(), 10) + "\n" + s.nonce
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(c.mac("session", payload)), s
}

// ParseSession memeriksa tanda tangan, masa berlaku, dan bahwa user-nya masih
// user yang sama di berkas kredensial.
func (c Credentials) ParseSession(v string, now time.Time) (Session, bool) {
	enc := base64.RawURLEncoding
	p64, m64, ok := strings.Cut(v, ".")
	if !ok {
		return Session{}, false
	}
	payload, err1 := enc.DecodeString(p64)
	mac, err2 := enc.DecodeString(m64)
	if err1 != nil || err2 != nil || !hmac.Equal(mac, c.mac("session", string(payload))) {
		return Session{}, false
	}
	f := strings.Split(string(payload), "\n")
	if len(f) != 3 || f[0] != c.User {
		return Session{}, false
	}
	exp, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return Session{}, false
	}
	return Session{User: f[0], Expires: time.Unix(exp, 0), nonce: f[2]}, true
}

// CSRF adalah token yang terikat ke sesi. Ia diturunkan, bukan disimpan:
// HMAC dari nonce sesi, jadi tetap stateless dan berganti tiap login.
func (c Credentials) CSRF(s Session) string {
	return hex.EncodeToString(c.mac("csrf", s.nonce))
}

func (c Credentials) CheckCSRF(s Session, token string) bool {
	return token != "" && hmac.Equal([]byte(token), []byte(c.CSRF(s)))
}

func (c Credentials) mac(purpose, msg string) []byte {
	h := hmac.New(sha256.New, c.Secret)
	h.Write([]byte(purpose + "\n" + msg))
	return h.Sum(nil)
}

// Limiter membatasi percobaan login gagal per alamat.
//
// Di memori, dan itu cukup: penyerang yang menebak terus-menerus justru
// menahan proses tetap hidup, jadi hitungannya tidak pernah hilang karena
// idle-exit. Yang hilang saat idle hanyalah hitungan orang yang sudah
// berhenti mencoba.
type Limiter struct {
	mu     sync.Mutex
	Max    int
	Window time.Duration
	fails  map[string][]time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{Max: 5, Window: 15 * time.Minute, fails: map[string][]time.Time{}}
}

// Allowed mengembalikan false kalau alamat ini sudah terlalu banyak gagal,
// beserta sisa waktu sampai boleh mencoba lagi.
func (l *Limiter) Allowed(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fs := l.prune(key, now)
	if len(fs) < l.Max {
		return true, 0
	}
	return false, fs[0].Add(l.Window).Sub(now)
}

func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.prune(key, now), now)
	// Batas ukuran peta: tiap alamat baru menambah entri, dan peta yang tumbuh
	// tanpa batas adalah celah kehabisan memori tersendiri.
	if len(l.fails) > 1024 {
		for k := range l.fails {
			if k != key {
				delete(l.fails, k)
			}
		}
	}
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	fs := l.fails[key]
	i := 0
	for i < len(fs) && now.Sub(fs[i]) >= l.Window {
		i++
	}
	fs = fs[i:]
	if len(fs) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = fs
	}
	return fs
}
