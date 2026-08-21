package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Container struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	Health string `json:"health,omitempty"`
	// Networks kosong pada container yang jalan adalah jebakan yang pernah
	// menyebabkan monitoring buta 5 jam: container hidup tapi tak terjangkau.
	Networks []string `json:"networks"`
	// PublishedPorts hanya berisi yang terikat ke alamat non-loopback, karena
	// itulah yang menjangkau LAN dan seluruh tailnet.
	PublishedPorts []string `json:"published_ports"`
}

func CollectContainers(ctx context.Context, socket string) ([]Container, error) {
	cl := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://docker/v1.43/containers/json?all=1", nil)
	if err != nil {
		return nil, err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("socket docker %s: %w", socket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker menjawab %s", resp.Status)
	}
	return parseContainers(resp.Body)
}

// parseContainers dipisahkan dari pemanggilan HTTP supaya bisa diuji dengan
// jawaban Docker yang direkam. Di sinilah bentuk data dunia nyata paling
// sering mengejutkan — network kosong, port 0.0.0.0, status ber-embel-embel.
func parseContainers(r io.Reader) ([]Container, error) {
	var raw []struct {
		Names  []string `json:"Names"`
		Image  string   `json:"Image"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
		Ports  []struct {
			IP          string `json:"IP"`
			PrivatePort int    `json:"PrivatePort"`
			PublicPort  int    `json:"PublicPort"`
			Type        string `json:"Type"`
		} `json:"Ports"`
		NetworkSettings struct {
			Networks map[string]struct{} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}

	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		c := Container{Image: r.Image, State: r.State, Status: r.Status}
		c.Health = healthFromStatus(r.Status)
		if len(r.Names) > 0 {
			c.Name = strings.TrimPrefix(r.Names[0], "/")
		}
		for n := range r.NetworkSettings.Networks {
			c.Networks = append(c.Networks, n)
		}
		// Urutan map tidak tentu; tanpa ini daftar network berganti urutan
		// tiap refresh dan halaman terlihat berkedip tanpa ada yang berubah.
		sort.Strings(c.Networks)

		seen := map[string]bool{}
		for _, p := range r.Ports {
			// Hanya yang terikat ke alamat non-loopback: itulah yang
			// menjangkau LAN dan seluruh jaringan mesh.
			if p.PublicPort == 0 || p.IP == "" || p.IP == "127.0.0.1" || p.IP == "::1" {
				continue
			}
			// `docker run -p 3000:3000` menghasilkan DUA entri: 0.0.0.0 dan ::.
			// Itu satu port yang sama, dipublikasikan di IPv4 dan IPv6 —
			// menampilkannya dua kali menggandakan tiap chip di UI dan tiap
			// temuan di notifikasi. Keduanya dinormalkan jadi "*", yang juga
			// lebih jujur artinya: semua alamat.
			addr := p.IP
			if addr == "0.0.0.0" || addr == "::" {
				addr = "*"
			}
			s := fmt.Sprintf("%s:%d->%d/%s", addr, p.PublicPort, p.PrivatePort, p.Type)
			if !seen[s] {
				seen[s] = true
				c.PublishedPorts = append(c.PublishedPorts, s)
			}
		}
		sort.Strings(c.PublishedPorts)
		out = append(out, c)
	}
	return out, nil
}

// healthFromStatus mengambil hasil healthcheck dari teks status.
//
// Docker TIDAK memberikan field terpisah di /containers/json: satu-satunya
// tempat hasil healthcheck muncul adalah embel-embel di Status, seperti
// "Up 2 hours (unhealthy)". Tanpa ini, aturan "healthcheck gagal" di
// internal/health tidak pernah bisa menyala di data sungguhan — ia cuma
// bekerja di mode demo, yang justru bentuk kebutaan paling menyesatkan.
func healthFromStatus(status string) string {
	i := strings.LastIndex(status, "(")
	j := strings.LastIndex(status, ")")
	if i < 0 || j < i {
		return ""
	}
	switch inner := strings.ToLower(strings.TrimSpace(status[i+1 : j])); {
	case inner == "healthy":
		return "healthy"
	case inner == "unhealthy":
		return "unhealthy"
	case strings.HasPrefix(inner, "health: starting"):
		return "starting"
	}
	return ""
}
