package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		c := Container{Image: r.Image, State: r.State, Status: r.Status}
		if len(r.Names) > 0 {
			c.Name = r.Names[0]
			if len(c.Name) > 0 && c.Name[0] == '/' {
				c.Name = c.Name[1:]
			}
		}
		for n := range r.NetworkSettings.Networks {
			c.Networks = append(c.Networks, n)
		}
		seen := map[string]bool{}
		for _, p := range r.Ports {
			if p.PublicPort == 0 || p.IP == "" || p.IP == "127.0.0.1" || p.IP == "::1" {
				continue
			}
			s := fmt.Sprintf("%s:%d->%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type)
			if !seen[s] {
				seen[s] = true
				c.PublishedPorts = append(c.PublishedPorts, s)
			}
		}
		out = append(out, c)
	}
	return out, nil
}
