package ops

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Nama yang BELUM ada — tag snapshot baru, anak dataset baru — tidak bisa
// dicocokkan dengan daftar dari sistem. Untuk itu saja dipakai himpunan
// karakter yang sempit, dan selalu diawali huruf/angka supaya tidak pernah
// terbaca sebagai opsi (`-r`, `--help`). Bagian nama yang SUDAH ada (induk
// dataset) tetap lewat daftar-putih.
var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	sizeRe = regexp.MustCompile(`^[1-9][0-9]*(\.[0-9]+)?[KMGTP]?$`)
)

func ValidName(n string) error {
	if !nameRe.MatchString(n) || strings.Contains(n, "..") {
		return fmt.Errorf("nama %q tidak sah: huruf, angka, _ . : - saja, maks 64, diawali huruf/angka", n)
	}
	return nil
}

func ValidTag(t string) error {
	if t != "" && !tagRe.MatchString(t) {
		return fmt.Errorf("tag %q tidak sah: huruf, angka, _ - saja, maks 32", t)
	}
	return nil
}

// SnapName menyusun nama snapshot. Awalan `issboard_` membedakannya dari
// buatan sanoid (`autosnap_`) — sanoid tidak memangkas yang bukan miliknya,
// jadi snapshot manual TIDAK ikut kedaluwarsa dan harus dihapus sendiri.
func SnapName(dataset, tag string, now time.Time) string {
	n := dataset + "@issboard_" + now.Format("2006-01-02_15:04:05")
	if tag != "" {
		n += "_" + tag
	}
	return n
}

// Properti yang boleh diubah dari dashboard. Daftar tertutup, dan nilainya
// juga: `mountpoint`, `canmount`, `encryption`, `sharenfs` sengaja tidak ada —
// salah isi di sana memindahkan atau menyembunyikan data, bukan sekadar
// mengubah perilakunya.
var props = map[string]struct {
	values  []string
	size    bool // menerima ukuran atau "none"
	inherit bool // boleh "inherit" (zfs inherit)
	help    string
}{
	"compression":    {values: compressionValues(), inherit: true, help: "kompresi blok baru; data lama tidak diubah"},
	"atime":          {values: []string{"on", "off"}, inherit: true, help: "catat waktu akses tiap baca"},
	"relatime":       {values: []string{"on", "off"}, inherit: true, help: "atime hanya diperbarui sesekali"},
	"recordsize":     {values: []string{"4K", "8K", "16K", "32K", "64K", "128K", "256K", "512K", "1M"}, inherit: true, help: "ukuran blok maksimum untuk berkas baru"},
	"readonly":       {values: []string{"on", "off"}, inherit: true, help: "tolak semua tulisan"},
	"quota":          {size: true, help: "batas dataset beserta anak & snapshot-nya"},
	"refquota":       {size: true, help: "batas data dataset ini saja"},
	"reservation":    {size: true, help: "ruang yang dijamin untuk dataset ini beserta anaknya"},
	"refreservation": {size: true, help: "ruang yang dijamin untuk data dataset ini saja"},
}

func compressionValues() []string {
	v := []string{"on", "off", "lz4", "zstd", "zstd-fast", "zle", "lzjb", "gzip"}
	for i := 1; i <= 9; i++ {
		v = append(v, "gzip-"+strconv.Itoa(i))
	}
	for i := 1; i <= 19; i++ {
		v = append(v, "zstd-"+strconv.Itoa(i))
	}
	return v
}

// PropNames diurutkan supaya UI dan `zfs get` selalu memakai urutan sama.
func PropNames() []string {
	var n []string
	for k := range props {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// PropSpec adalah bentuk yang dikirim ke UI untuk menggambar pilihan.
type PropSpec struct {
	Name    string   `json:"name"`
	Values  []string `json:"values,omitempty"`
	Size    bool     `json:"size,omitempty"`
	Inherit bool     `json:"inherit,omitempty"`
	Help    string   `json:"help"`
}

func PropSpecs() []PropSpec {
	var out []PropSpec
	for _, n := range PropNames() {
		p := props[n]
		out = append(out, PropSpec{Name: n, Values: p.values, Size: p.size, Inherit: p.inherit, Help: p.help})
	}
	return out
}

func ValidateProp(k, v string) error {
	p, ok := props[k]
	if !ok {
		return fmt.Errorf("properti %q tidak bisa diubah dari dashboard", k)
	}
	if v == "inherit" && p.inherit {
		return nil
	}
	if p.size && (v == "none" || sizeRe.MatchString(v)) {
		return nil
	}
	for _, a := range p.values {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("nilai %q tidak sah untuk %s", v, k)
}

// ---------- baca (tanpa root) ----------

type Snapshot struct {
	Name       string    `json:"name"`
	Used       int64     `json:"used_bytes"`
	Referenced int64     `json:"referenced_bytes"`
	Created    time.Time `json:"created"`
}

type Prop struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

func runOut(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// DatasetNames: daftar-putih filesystem & volume, dari zfs sendiri.
func DatasetNames(ctx context.Context) ([]string, error) {
	out, err := runOut(ctx, "zfs", "list", "-H", "-o", "name", "-t", "filesystem,volume")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func ListSnapshots(ctx context.Context, dataset string) ([]Snapshot, error) {
	out, err := runOut(ctx, "zfs", "list", "-Hp", "-t", "snapshot", "-d", "1",
		"-o", "name,used,referenced,creation", "-s", "creation", dataset)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(out), nil
}

func parseSnapshots(out string) []Snapshot {
	var s []Snapshot
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		used, _ := strconv.ParseInt(f[1], 10, 64)
		ref, _ := strconv.ParseInt(f[2], 10, 64)
		c, _ := strconv.ParseInt(f[3], 10, 64)
		s = append(s, Snapshot{Name: f[0], Used: used, Referenced: ref, Created: time.Unix(c, 0)})
	}
	// Terbaru dulu: yang paling sering dicari adalah yang baru saja dibuat.
	sort.SliceStable(s, func(i, j int) bool { return s[i].Created.After(s[j].Created) })
	return s
}

func GetProps(ctx context.Context, dataset string) ([]Prop, error) {
	out, err := runOut(ctx, "zfs", "get", "-H", "-o", "property,value,source",
		strings.Join(PropNames(), ","), dataset)
	if err != nil {
		return nil, err
	}
	return parseProps(out), nil
}

func parseProps(out string) []Prop {
	var p []Prop
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 {
			p = append(p, Prop{Name: f[0], Value: f[1], Source: f[2]})
		}
	}
	return p
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
