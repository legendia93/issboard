// Package config memuat konfigurasi issboard.
//
// Sengaja tanpa dependensi YAML: formatnya sesederhana "kunci: nilai", dan
// satu binary statis tanpa dependensi adalah alasan utama proyek ini memilih
// Go (lihat plan bagian 5).
package config

import (
	"bufio"
	"os"
	"strings"
	"time"
)

type Config struct {
	// Listen dipakai HANYA kalau systemd tidak menyerahkan socket.
	Listen string

	// IdleTimeout: binary keluar sendiri setelah sekian lama tanpa request,
	// lalu systemd menyalakannya lagi lewat socket activation.
	IdleTimeout time.Duration

	// SmartCache adalah file JSON yang ditulis unit root terpisah.
	// issboard TIDAK PERNAH memanggil smartctl sendiri — lihat plan 5.
	SmartCache string

	// DockerSocket dibaca read-only untuk daftar container.
	DockerSocket string

	// Pools yang ditampilkan. Kosong = deteksi otomatis lewat `zpool list`.
	Pools []string
}

func Default() Config {
	return Config{
		Listen:       "127.0.0.1:9955",
		IdleTimeout:  5 * time.Minute,
		SmartCache:   "/var/cache/issboard/smart.json",
		DockerSocket: "/var/run/docker.sock",
	}
}

// Load membaca file konfigurasi. File yang tidak ada bukan error: default
// dipakai apa adanya, supaya issboard tetap hidup justru saat sistem kacau.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)

		switch key {
		case "listen":
			c.Listen = val
		case "idle_timeout":
			if d, err := time.ParseDuration(val); err == nil {
				c.IdleTimeout = d
			}
		case "smart_cache":
			c.SmartCache = val
		case "docker_socket":
			c.DockerSocket = val
		case "pools":
			c.Pools = nil
			for _, p := range strings.Split(val, ",") {
				if p = strings.TrimSpace(p); p != "" {
					c.Pools = append(c.Pools, p)
				}
			}
		}
	}
	return c, sc.Err()
}
