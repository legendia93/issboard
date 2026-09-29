package ops

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSys merekam SETIAP perintah yang dijalankan. Test di sini mengunci
// janji terpenting helper: teks dari permintaan tidak pernah sampai ke exec
// kecuali persis sama dengan sesuatu yang dilaporkan sistem sendiri.
type fakeSys struct {
	ran   [][]string
	files map[string]string
	perms map[string]os.FileMode
}

func newFake() (*fakeSys, *Executor) {
	f := &fakeSys{perms: map[string]os.FileMode{}, files: map[string]string{
		"/proc/meminfo":                "MemTotal:       16384000 kB\n",
		"/proc/spl/kstat/zfs/arcstats": "size 4 100\nc_max 4 8000000000\nc_min 4 500000000\n",
	}}
	e := &Executor{
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			f.ran = append(f.ran, append([]string{name}, args...))
			switch {
			case name == "zpool" && args[0] == "list":
				return "kolam\ncadangan\n", nil
			case name == "zfs" && args[0] == "list" && args[len(args)-1] == "filesystem,volume":
				return "kolam\nkolam/data\n", nil
			case name == "zfs" && args[0] == "list" && args[len(args)-1] == "snapshot":
				return "kolam/data@lama\n", nil
			case name == "smartctl" && args[0] == "--scan":
				return "/dev/sda -d sat # /dev/sda [SAT], ATA device\n/dev/nvme0 -d nvme # /dev/nvme0, NVMe device\n", nil
			}
			return "", nil
		},
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := f.files[p]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		},
		WriteFile: func(p string, b []byte, perm os.FileMode) error {
			f.files[p] = string(b)
			f.perms[p] = perm
			return nil
		},
		Remove: func(p string) error { delete(f.files, p); return nil },
	}
	e.Now = func() time.Time { return time.Date(2026, 9, 29, 16, 20, 0, 0, time.UTC) }
	return f, e
}

// ran mencari perintah zfs yang MENGUBAH sesuatu (bukan list).
func (f *fakeSys) mutations() []string {
	var out []string
	for _, c := range f.ran {
		if (c[0] == "zfs" && c[1] != "list") || c[0] == "systemd-run" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

func (f *fakeSys) last() string {
	if len(f.ran) == 0 {
		return ""
	}
	return strings.Join(f.ran[len(f.ran)-1], " ")
}

func TestScrubHanyaPoolYangAda(t *testing.T) {
	f, e := newFake()
	if r := e.Handle(context.Background(), Request{Action: ScrubStart, Target: "kolam"}); !r.OK {
		t.Fatalf("pool sah ditolak: %s", r.Error)
	}
	if f.last() != "zpool scrub kolam" {
		t.Errorf("perintah salah: %q", f.last())
	}
	e.Handle(context.Background(), Request{Action: ScrubStop, Target: "kolam"})
	if f.last() != "zpool scrub -s kolam" {
		t.Errorf("perintah stop salah: %q", f.last())
	}

	for _, jahat := range []string{"kolam/data", "kolam;reboot", "-s", "", "kolam ", "../kolam"} {
		f.ran = nil
		if r := e.Handle(context.Background(), Request{Action: ScrubStart, Target: jahat}); r.OK {
			t.Errorf("target %q diterima", jahat)
		}
		for _, c := range f.ran {
			if c[0] == "zpool" && c[1] == "scrub" {
				t.Errorf("target %q sampai ke exec: %v", jahat, c)
			}
		}
	}
}

func TestSmartArgumenDariScan(t *testing.T) {
	f, e := newFake()
	if r := e.Handle(context.Background(), Request{Action: SmartShort, Target: "/dev/sda"}); !r.OK {
		t.Fatal(r.Error)
	}
	// -d sat berasal dari hasil scan, bukan dari permintaan.
	if f.last() != "smartctl -t short -d sat /dev/sda" {
		t.Errorf("perintah salah: %q", f.last())
	}
	for _, jahat := range []string{"/dev/sdb", "/dev/sda -d sat", "--scan", "/dev/sda;x"} {
		f.ran = nil
		if r := e.Handle(context.Background(), Request{Action: SmartLong, Target: jahat}); r.OK {
			t.Errorf("perangkat %q diterima", jahat)
		}
		if strings.Contains(f.last(), "-t") {
			t.Errorf("perangkat %q sampai ke exec: %q", jahat, f.last())
		}
	}
}

func TestAksiTakDikenal(t *testing.T) {
	f, e := newFake()
	if r := e.Handle(context.Background(), Request{Action: "shell", Target: "rm -rf /"}); r.OK {
		t.Error("aksi tak dikenal diterima")
	}
	if len(f.ran) != 0 {
		t.Errorf("aksi tak dikenal menjalankan sesuatu: %v", f.ran)
	}
}

func TestARC(t *testing.T) {
	f, e := newFake()
	e.PersistDir = t.TempDir()
	own := filepath.Join(e.PersistDir, filepath.Base(ARCPersistFile))

	// Di bawah c_min: ZFS akan mengabaikannya diam-diam, jadi harus ditolak.
	if r := e.Handle(context.Background(), Request{Action: ARCSet, Value: 100 << 20}); r.OK {
		t.Error("nilai di bawah batas diterima")
	}
	if _, ok := f.files[ARCParam]; ok {
		t.Error("nilai yang ditolak tetap ditulis ke /sys")
	}

	if r := e.Handle(context.Background(), Request{Action: ARCSet, Value: 4 << 30, Persist: true}); !r.OK {
		t.Fatal(r.Error)
	}
	if f.files[ARCParam] != "4294967296\n" {
		t.Errorf("isi parameter %q", f.files[ARCParam])
	}
	if !strings.Contains(f.files[own], "options zfs zfs_arc_max=4294967296") {
		t.Errorf("berkas permanen: %q", f.files[own])
	}

	// Berkas milik orang lain yang juga mengatur zfs_arc_max: tolak, dan
	// jangan ubah apa pun — termasuk nilai yang berjalan.
	os.WriteFile(filepath.Join(e.PersistDir, "zfs.conf"), []byte("options zfs zfs_arc_max=2147483648\n"), 0o644)
	delete(f.files, ARCParam)
	r := e.Handle(context.Background(), Request{Action: ARCSet, Value: 6 << 30, Persist: true})
	if r.OK || !strings.Contains(r.Error, "zfs.conf") {
		t.Errorf("konflik tidak dilaporkan: %+v", r)
	}
	if _, ok := f.files[ARCParam]; ok {
		t.Error("nilai tetap ditulis walau ada konflik")
	}
}

func TestParseModprobe(t *testing.T) {
	v, ok := parseModprobeARC("# komentar\noptions zfs zfs_arc_min=1 zfs_arc_max=0x40000000 # 1G\noptions other x=1\n")
	if !ok || v != 1<<30 {
		t.Errorf("dapat %d %v", v, ok)
	}
	if _, ok := parseModprobeARC("#options zfs zfs_arc_max=5\n"); ok {
		t.Error("baris komentar terbaca")
	}
}

func TestParseCronDanTimer(t *testing.T) {
	cs := parseCron("SHELL=/bin/sh\n# x\n24 0 8-14 * * root /usr/lib/zfs-linux/scrub\n@reboot root zpool status\n", "/etc/cron.d/z")
	if len(cs) != 2 || cs[0].Schedule != "24 0 8-14 * *" || cs[0].User != "root" || cs[1].Schedule != "@reboot" {
		t.Errorf("cron: %+v", cs)
	}
	ts, err := parseTimers([]byte(`[{"next":1790674410800773,"left":1,"last":0,"passed":null,"unit":"a.timer","activates":"a.service"}]`))
	if err != nil || len(ts) != 1 || ts[0].Next == nil || ts[0].Last != nil {
		t.Errorf("timer: %+v %v", ts, err)
	}
}

// e2scrub_all milik ext4 bukan scrub ZFS. Menghitungnya akan menyembunyikan
// catatan "tidak ada scrub terjadwal" di mesin yang justru perlu melihatnya.
func TestTanpaZFSTanpaCatatanScrub(t *testing.T) {
	if n := strings.Join(scheduleNotes(Schedule{}, false), "|"); strings.Contains(n, "scrub") {
		t.Errorf("mesin tanpa ZFS diberi catatan scrub: %q", n)
	}
}

// Call dan helper bicara lewat socket unix sungguhan: satu baris JSON masuk,
// satu baris keluar — persis yang dilakukan systemd dengan Accept=yes.
func TestCallLewatSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, e := newFake()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		var req Request
		json.Unmarshal(line, &req)
		b, _ := json.Marshal(e.Handle(context.Background(), req))
		c.Write(append(b, '\n'))
	}()
	r, err := Call(context.Background(), sock, Request{Action: ScrubStart, Target: "tidak-ada"})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || !strings.Contains(r.Error, "tidak ada di zpool list") {
		t.Errorf("jawaban: %+v", r)
	}

	if _, err := Call(context.Background(), filepath.Join(t.TempDir(), "x.sock"), Request{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("socket tak ada harus ErrUnavailable, dapat %v", err)
	}
}

func TestE2scrubBukanScrubZFS(t *testing.T) {
	s := Schedule{Cron: []CronEntry{{Command: "test -e /run/systemd/system || /sbin/e2scrub_all -A -r"}}}
	notes := strings.Join(scheduleNotes(s, true), "|")
	if !strings.Contains(notes, "tidak ada scrub") {
		t.Errorf("e2scrub dianggap scrub ZFS: %q", notes)
	}
	s = Schedule{Cron: []CronEntry{{Command: "/usr/lib/zfs-linux/scrub"}},
		Timers: []Timer{{Unit: "zfs-scrub-monthly@kolam.timer"}}}
	if !strings.Contains(strings.Join(scheduleNotes(s, true), "|"), "dua kali") {
		t.Error("scrub ganda tidak dicatat")
	}
}

func TestSnapshotDibuatDenganNamaOtomatis(t *testing.T) {
	f, e := newFake()
	r := e.Handle(context.Background(), Request{Action: SnapCreate, Target: "kolam/data", Name: "sebelum-upgrade"})
	if !r.OK {
		t.Fatal(r.Error)
	}
	if got := f.mutations(); len(got) != 1 || got[0] != "zfs snapshot kolam/data@issboard_2026-09-29_16:20:00_sebelum-upgrade" {
		t.Errorf("perintah: %v", got)
	}

	for _, req := range []Request{
		{Action: SnapCreate, Target: "kolam/lain"},
		{Action: SnapCreate, Target: "kolam/data", Name: "-r"},
		{Action: SnapCreate, Target: "kolam/data", Name: "a b"},
		{Action: SnapCreate, Target: "kolam/data", Name: "x@y"},
		{Action: SnapCreate, Target: "kolam/data@lama"},
	} {
		f.ran = nil
		if r := e.Handle(context.Background(), req); r.OK {
			t.Errorf("%+v diterima", req)
		}
		if m := f.mutations(); len(m) != 0 {
			t.Errorf("%+v sampai ke exec: %v", req, m)
		}
	}
}

func TestHapusSnapshotHanyaSatu(t *testing.T) {
	f, e := newFake()
	if r := e.Handle(context.Background(), Request{Action: SnapDestroy, Target: "kolam/data@lama"}); !r.OK {
		t.Fatal(r.Error)
	}
	if got := f.mutations(); len(got) != 1 || got[0] != "zfs destroy kolam/data@lama" {
		t.Errorf("perintah: %v", got)
	}
	// Dataset (tanpa @) tidak boleh pernah bisa dihapus lewat jalur ini,
	// walaupun ada di daftar dataset.
	for _, jahat := range []string{"kolam/data", "kolam", "kolam/data@lama -r", "kolam/data@baru", "-r kolam/data@lama"} {
		f.ran = nil
		if r := e.Handle(context.Background(), Request{Action: SnapDestroy, Target: jahat}); r.OK {
			t.Errorf("%q diterima", jahat)
		}
		if m := f.mutations(); len(m) != 0 {
			t.Errorf("%q sampai ke exec: %v", jahat, m)
		}
	}
}

func TestBuatDatasetTanpaMountDiNamespaceHelper(t *testing.T) {
	f, e := newFake()
	r := e.Handle(context.Background(), Request{Action: DSCreate, Target: "kolam", Name: "baru",
		Props: map[string]string{"compression": "zstd", "atime": "inherit", "quota": "50G"}})
	if !r.OK {
		t.Fatal(r.Error)
	}
	m := f.mutations()
	if len(m) != 2 || m[0] != "zfs create -u -o compression=zstd -o quota=50G kolam/baru" ||
		!strings.HasSuffix(m[1], "zfs mount kolam/baru") || !strings.HasPrefix(m[1], "systemd-run --wait") {
		t.Errorf("perintah: %v", m)
	}

	for _, req := range []Request{
		{Action: DSCreate, Target: "kolam", Name: "data"},   // sudah ada
		{Action: DSCreate, Target: "tidak-ada", Name: "x"},  // induk tak ada
		{Action: DSCreate, Target: "kolam", Name: "../etc"}, // nama
		{Action: DSCreate, Target: "kolam", Name: "-o"},     // opsi
		{Action: DSCreate, Target: "kolam", Name: "a/b"},    // dua tingkat
		{Action: DSCreate, Target: "kolam", Name: "x", Props: map[string]string{"mountpoint": "/etc"}},
		{Action: DSCreate, Target: "kolam", Name: "x", Props: map[string]string{"compression": "lz4 -o x=y"}},
	} {
		f.ran = nil
		if r := e.Handle(context.Background(), req); r.OK {
			t.Errorf("%+v diterima", req)
		}
		if m := f.mutations(); len(m) != 0 {
			t.Errorf("%+v sampai ke exec: %v", req, m)
		}
	}
}

func TestUbahProperti(t *testing.T) {
	f, e := newFake()
	e.Handle(context.Background(), Request{Action: DSSet, Target: "kolam/data", Prop: "quota", PropValue: "none"})
	e.Handle(context.Background(), Request{Action: DSSet, Target: "kolam/data", Prop: "compression", PropValue: "inherit"})
	m := f.mutations()
	if len(m) != 2 || m[0] != "zfs set quota=none kolam/data" || m[1] != "zfs inherit compression kolam/data" {
		t.Errorf("perintah: %v", m)
	}
	for _, req := range []Request{
		{Action: DSSet, Target: "kolam/data", Prop: "mountpoint", PropValue: "/"},
		{Action: DSSet, Target: "kolam/data", Prop: "quota", PropValue: "inherit"},
		{Action: DSSet, Target: "kolam/data", Prop: "quota", PropValue: "10G;x"},
		{Action: DSSet, Target: "kolam/data", Prop: "recordsize", PropValue: "3K"},
		{Action: DSSet, Target: "kolam/x", Prop: "atime", PropValue: "off"},
	} {
		f.ran = nil
		if r := e.Handle(context.Background(), req); r.OK {
			t.Errorf("%+v diterima", req)
		}
		if m := f.mutations(); len(m) != 0 {
			t.Errorf("%+v sampai ke exec: %v", req, m)
		}
	}
}

func TestParseSnapshotsTerbaruDulu(t *testing.T) {
	s := parseSnapshots("k/d@a\t100\t200\t1000\nk/d@b\t5\t6\t2000\n")
	if len(s) != 2 || s[0].Name != "k/d@b" || s[1].Used != 100 {
		t.Errorf("%+v", s)
	}
}

// Setelah pemilik mematikan scrub cron Debian dengan cara yang dianjurkan
// Debian (properti per-pool), catatan "scrub dua kali" harus padam. Ini
// terjadi sungguhan: catatannya tetap menyala sesudah diperbaiki.
func TestScrubCronDebianDimatikanLewatProperti(t *testing.T) {
	mk := func() Schedule {
		return Schedule{
			Cron:   []CronEntry{{Command: "if [ -x /usr/lib/zfs-linux/scrub ]; then /usr/lib/zfs-linux/scrub; fi"}},
			Timers: []Timer{{Unit: "zfs-scrub-monthly@a.timer"}},
		}
	}

	s := mk()
	markDebianScrub(s.Cron, parsePoolProps("a\tdisable\nb\t-\n"))
	if s.Cron[0].Inactive != "" || !strings.Contains(strings.Join(scheduleNotes(s, true), "|"), "dua kali") {
		t.Error("satu pool masih di-scrub cron, tapi catatan ganda padam")
	}

	s = mk()
	markDebianScrub(s.Cron, parsePoolProps("a\tdisable\nb\tdisable\n"))
	if s.Cron[0].Inactive == "" {
		t.Error("baris cron tidak ditandai tidak aktif")
	}
	if n := strings.Join(scheduleNotes(s, true), "|"); strings.Contains(n, "scrub") {
		t.Errorf("catatan scrub masih menyala: %q", n)
	}
}

const tokenLama = "123456789:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const tokenBaru = "987654321:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

func envFake() (*fakeSys, *Executor) {
	f, e := newFake()
	e.AgentEnv = "/etc/issboard/agent.env"
	e.SettingsFile = "/etc/issboard/settings.conf"
	e.AgentBin = "/usr/bin/issboard-agent"
	f.files[e.AgentEnv] = "# kredensial\n#ISSBOARD_NTFY_TOPIC=contoh\nISSBOARD_TELEGRAM_TOKEN=" + tokenLama +
		"\nISSBOARD_TELEGRAM_CHAT_ID=-1000000000001\nLAIN=biarkan\n"
	return f, e
}

func TestNotifySetHanyaKunciYangDiminta(t *testing.T) {
	f, e := envFake()
	r := e.Handle(context.Background(), Request{Action: NotifySet, Props: map[string]string{
		"ISSBOARD_TELEGRAM_TOKEN":   tokenBaru,
		"ISSBOARD_TELEGRAM_CHAT_ID": "",
		"ISSBOARD_NTFY_URL":         "https://ntfy.example.org",
	}})
	if !r.OK {
		t.Fatal(r.Error)
	}
	got := f.files[e.AgentEnv]
	for _, want := range []string{"# kredensial\n", "#ISSBOARD_NTFY_TOPIC=contoh\n", "ISSBOARD_TELEGRAM_TOKEN=" + tokenBaru + "\n",
		"LAIN=biarkan\n", "ISSBOARD_NTFY_URL=https://ntfy.example.org\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("hilang %q dari:\n%s", want, got)
		}
	}
	if strings.Contains(got, tokenLama) || strings.Contains(got, "CHAT_ID") {
		t.Errorf("nilai lama/terhapus masih ada:\n%s", got)
	}
	if f.perms[e.AgentEnv] != 0o600 {
		t.Errorf("mode %o, harus 0600", f.perms[e.AgentEnv])
	}
}

// Baris baru di nilai = variabel lingkungan selundupan untuk proses agent.
func TestNotifySetMenolakNilaiSelundupan(t *testing.T) {
	f, e := envFake()
	before := f.files[e.AgentEnv]
	for _, props := range []map[string]string{
		{"ISSBOARD_TELEGRAM_CHAT_ID": "-100\nLD_PRELOAD=/tmp/x.so"},
		{"ISSBOARD_TELEGRAM_TOKEN": "bukan token"},
		{"ISSBOARD_NTFY_URL": "file:///etc/shadow"},
		{"ISSBOARD_NTFY_URL": "https://a.b/ x"},
		{"PATH": "/tmp"},
	} {
		if r := e.Handle(context.Background(), Request{Action: NotifySet, Props: props}); r.OK {
			t.Errorf("%v diterima", props)
		}
	}
	if f.files[e.AgentEnv] != before {
		t.Error("berkas berubah walau semua permintaan ditolak")
	}
}

func TestNotifyStatusTidakMembawaRahasia(t *testing.T) {
	_, e := envFake()
	r := e.Handle(context.Background(), Request{Action: NotifyStatus})
	if !r.OK {
		t.Fatal(r.Error)
	}
	if strings.Contains(r.Output, "AAAAAAAAAAAA") {
		t.Fatalf("token bocor ke jawaban: %s", r.Output)
	}
	var st map[string]NotifyValue
	json.Unmarshal([]byte(r.Output), &st)
	if tk := st["ISSBOARD_TELEGRAM_TOKEN"]; !tk.Set || tk.Hint != "••••AAAA" || tk.Value != "" {
		t.Errorf("token: %+v", tk)
	}
	if ci := st["ISSBOARD_TELEGRAM_CHAT_ID"]; ci.Value != "-1000000000001" {
		t.Errorf("chat id bukan rahasia dan harus terlihat: %+v", ci)
	}
	if st["ISSBOARD_NTFY_TOPIC"].Set {
		t.Error("baris berkomentar terbaca sebagai terisi")
	}
}

func TestNotifyTestLewatSystemdRunSebagaiIssboard(t *testing.T) {
	f, e := envFake()
	e.Handle(context.Background(), Request{Action: NotifyTest})
	cmd := f.last()
	for _, want := range []string{"systemd-run --wait --pipe", "User=issboard", "EnvironmentFile=-/etc/issboard/agent.env", "/usr/bin/issboard-agent -config /etc/issboard.yaml -test"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("perintah tidak memuat %q: %s", want, cmd)
		}
	}
}

func TestSettingsSet(t *testing.T) {
	f, e := envFake()
	f.files[e.SettingsFile] = "alert_repeat: 12h\nidle_timeout: 10m\n"
	r := e.Handle(context.Background(), Request{Action: SettingsSet, Props: map[string]string{
		"alert_repeat": "6h", "idle_timeout": "", "snapshot_exempt": " kolam/rekaman/* ,, kolam/scratch ",
	}})
	if !r.OK {
		t.Fatal(r.Error)
	}
	got := f.files[e.SettingsFile]
	if !strings.Contains(got, "alert_repeat: 6h\n") || strings.Contains(got, "idle_timeout") ||
		!strings.Contains(got, "snapshot_exempt: kolam/rekaman/*, kolam/scratch\n") {
		t.Errorf("isi:\n%s", got)
	}
	for _, props := range []map[string]string{
		{"history_file": "/tmp/x"},
		{"alert_repeat": "1s"},
		{"notify_min_level": "semua"},
		{"snapshot_exempt": "../etc"},
		{"pools": "a\nhistory_file: /tmp"},
	} {
		if r := e.Handle(context.Background(), Request{Action: SettingsSet, Props: props}); r.OK {
			t.Errorf("%v diterima", props)
		}
	}
}
