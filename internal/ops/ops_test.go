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
)

// fakeSys merekam SETIAP perintah yang dijalankan. Test di sini mengunci
// janji terpenting helper: teks dari permintaan tidak pernah sampai ke exec
// kecuali persis sama dengan sesuatu yang dilaporkan sistem sendiri.
type fakeSys struct {
	ran   [][]string
	files map[string]string
}

func newFake() (*fakeSys, *Executor) {
	f := &fakeSys{files: map[string]string{
		"/proc/meminfo":                "MemTotal:       16384000 kB\n",
		"/proc/spl/kstat/zfs/arcstats": "size 4 100\nc_max 4 8000000000\nc_min 4 500000000\n",
	}}
	e := &Executor{
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			f.ran = append(f.ran, append([]string{name}, args...))
			switch {
			case name == "zpool" && args[0] == "list":
				return "kolam\ncadangan\n", nil
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
		WriteFile: func(p string, b []byte) error { f.files[p] = string(b); return nil },
		Remove:    func(p string) error { delete(f.files, p); return nil },
	}
	return f, e
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
