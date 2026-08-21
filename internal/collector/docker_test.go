package collector

import "strings"

import "testing"

// Jawaban /containers/json yang dipangkas seperlunya. Tiga jebakan sekaligus:
// container Up TANPA network, port ter-publish ke 0.0.0.0, dan healthcheck
// yang hasilnya cuma menempel di teks Status.
const jawabanDocker = `[
 {"Names":["/web"],"Image":"demo/web:1.4","State":"running","Status":"Up 6 days",
  "Ports":[{"IP":"127.0.0.1","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}],
  "NetworkSettings":{"Networks":{"proxy":{},"bridge":{}}}},

 {"Names":["/basis-data"],"Image":"demo/pg:16","State":"running","Status":"Up 6 days (healthy)",
  "Ports":[{"IP":"0.0.0.0","PrivatePort":5432,"PublicPort":5432,"Type":"tcp"},
           {"IP":"::","PrivatePort":5432,"PublicPort":5432,"Type":"tcp"}],
  "NetworkSettings":{"Networks":{"bridge":{}}}},

 {"Names":["/pekerja"],"Image":"demo/worker:1","State":"running","Status":"Up 2 hours",
  "Ports":[],"NetworkSettings":{"Networks":{}}},

 {"Names":["/cache"],"Image":"demo/redis:7","State":"running","Status":"Up 6 days (unhealthy)",
  "Ports":[],"NetworkSettings":{"Networks":{"bridge":{}}}},

 {"Names":["/pencadang"],"Image":"demo/backup:2","State":"exited","Status":"Exited (1) 3 hours ago",
  "Ports":[],"NetworkSettings":{"Networks":{}}}
]`

func TestParseContainers(t *testing.T) {
	cs, err := parseContainers(strings.NewReader(jawabanDocker))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 {
		t.Fatalf("mau 5 container, dapat %d", len(cs))
	}

	by := map[string]Container{}
	for _, c := range cs {
		by[c.Name] = c
	}

	// Nama Docker berawalan "/" dan itu bukan bagian namanya.
	if _, ok := by["web"]; !ok {
		t.Fatalf("awalan / tidak dibuang: %v", cs[0].Name)
	}

	// Port yang cuma terikat ke loopback TIDAK terjangkau jaringan, jadi
	// tidak boleh muncul sebagai port terbuka.
	if n := len(by["web"].PublishedPorts); n != 0 {
		t.Errorf("port loopback ikut terdaftar: %v", by["web"].PublishedPorts)
	}

	// 0.0.0.0 dan :: keduanya menjangkau seluruh jaringan.
	db := by["basis-data"]
	if len(db.PublishedPorts) != 2 {
		t.Errorf("port 0.0.0.0/:: harus terdaftar, dapat %v", db.PublishedPorts)
	}

	// 🔴 Jebakan yang jadi akar insiden monitoring buta: Up, tapi tanpa network.
	if len(by["pekerja"].Networks) != 0 {
		t.Errorf("container tanpa network salah dibaca: %v", by["pekerja"].Networks)
	}
	if by["pekerja"].State != "running" {
		t.Errorf("state harus tetap running: %q", by["pekerja"].State)
	}
}

// Docker tidak memberi field healthcheck terpisah di /containers/json —
// satu-satunya tempatnya adalah embel-embel di Status.
func TestHealthFromStatus(t *testing.T) {
	for _, c := range []struct{ status, mau string }{
		{"Up 6 days", ""},
		{"Up 6 days (healthy)", "healthy"},
		{"Up 2 hours (unhealthy)", "unhealthy"},
		{"Up 5 seconds (health: starting)", "starting"},
		{"Exited (1) 3 hours ago", ""},
		{"Exited (137) 2 days ago", ""},
		{"Up 3 days (Paused)", ""},
	} {
		if got := healthFromStatus(c.status); got != c.mau {
			t.Errorf("%q: mau %q, dapat %q", c.status, c.mau, got)
		}
	}
}

// Urutan map di Go acak. Tanpa pengurutan, daftar network dan port berganti
// urutan tiap refresh dan halaman terlihat berkedip tanpa ada yang berubah.
func TestParseContainersUrutanStabil(t *testing.T) {
	var sebelumnya []string
	for i := 0; i < 8; i++ {
		cs, err := parseContainers(strings.NewReader(jawabanDocker))
		if err != nil {
			t.Fatal(err)
		}
		var kini []string
		for _, c := range cs {
			kini = append(kini, c.Name+":"+strings.Join(c.Networks, ",")+
				"|"+strings.Join(c.PublishedPorts, ","))
		}
		if sebelumnya != nil {
			for j := range kini {
				if kini[j] != sebelumnya[j] {
					t.Fatalf("urutan berubah antar pembacaan:\n  %s\n  %s", sebelumnya[j], kini[j])
				}
			}
		}
		sebelumnya = kini
	}
}

func TestParseContainersJSONRusak(t *testing.T) {
	if _, err := parseContainers(strings.NewReader("{bukan json")); err == nil {
		t.Error("JSON rusak harus jadi error, bukan daftar kosong yang terlihat sehat")
	}
}
