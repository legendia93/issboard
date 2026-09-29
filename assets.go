package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// assets menyajikan berkas web, dan menandai setiap CSS/JS yang dirujuk HTML
// dengan versi isinya: `style.css` → `style.css?v=3f2a…`.
//
// 🔴 Kenapa bukan cukup Cache-Control. Di belakang Cloudflare, header dari
// issboard DITIMPA: "Browser Cache TTL" bawaan Cloudflare memberi browser
// max-age 4 jam, apa pun yang dikirim issboard. Setelah upgrade, HTML baru
// tersaji bersama CSS lama dari cache browser — halaman kelola sempat tampil
// tanpa gaya sama sekali, dan hanya lewat domain itu. Pengaturan CDN bisa
// dibetulkan, tapi pemasangan berikutnya di belakang CDN lain akan jatuh ke
// lubang yang sama. URL yang berubah setiap isinya berubah tidak bergantung
// pada pengaturan siapa pun.
//
// Versinya PER BERKAS, dari isinya: upgrade yang hanya mengubah admin.js tidak
// memaksa browser mengambil ulang style.css.
type assets struct {
	fsys fs.FS
	next http.Handler
	// frozen: berkasnya ter-embed dan tidak bisa berubah selama proses hidup,
	// jadi hash cukup dihitung sekali. Dengan -web (mengembangkan) berkas di
	// disk berubah tanpa restart, jadi hash dihitung ulang tiap permintaan.
	frozen bool

	mu     sync.Mutex
	hashes map[string]string
}

func newAssets(fsys fs.FS, frozen bool) *assets {
	return &assets{fsys: fsys, next: http.FileServerFS(fsys), frozen: frozen, hashes: map[string]string{}}
}

// Hanya rujukan relatif ke berkas sendiri. URL absolut atau berskema tidak
// pernah ada di halaman ini (CSP default-src 'self'), dan tidak disentuh.
var assetRef = regexp.MustCompile(`(href|src)="([A-Za-z0-9_./-]+\.(?:css|js))"`)

func (a *assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if strings.HasSuffix(name, ".html") {
		if b, err := fs.ReadFile(a.fsys, name); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(a.stamp(b))
			return
		}
	}
	// Aset yang bertanda versi tidak akan pernah berubah di URL yang sama:
	// isinya berubah = versinya berubah = URL-nya berubah. Jadi boleh
	// disimpan selamanya — dan itu juga yang membuat CDN yang menimpa
	// header jadi tidak relevan.
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	a.next.ServeHTTP(w, r)
}

func (a *assets) stamp(html []byte) []byte {
	return assetRef.ReplaceAllFunc(html, func(m []byte) []byte {
		sub := assetRef.FindSubmatch(m)
		v := a.version(string(sub[2]))
		if v == "" {
			return m // berkasnya tidak ada: biarkan 404 terlihat apa adanya
		}
		return []byte(string(sub[1]) + `="` + string(sub[2]) + "?v=" + v + `"`)
	})
}

func (a *assets) version(name string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.hashes[name]; ok && a.frozen {
		return v
	}
	b, err := fs.ReadFile(a.fsys, strings.TrimPrefix(name, "./"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	v := hex.EncodeToString(sum[:5])
	a.hashes[name] = v
	return v
}
