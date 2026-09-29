package ops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Executor mengerjakan permintaan di sisi root. Semua sentuhan ke sistem
// lewat field fungsi supaya bisa diuji tanpa root, tanpa ZFS, tanpa disk.
type Executor struct {
	Run       func(ctx context.Context, name string, args ...string) (string, error)
	ReadFile  func(path string) ([]byte, error)
	WriteFile func(path string, data []byte) error
	Remove    func(path string) error
	// PersistDir adalah folder modprobe.d; bisa diganti di test.
	PersistDir string
}

// NewExecutor memakai sistem sungguhan.
func NewExecutor() *Executor {
	return &Executor{
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("%s %s: %w: %s", name,
					strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return string(out), nil
		},
		ReadFile:   os.ReadFile,
		WriteFile:  writeAtomic,
		Remove:     os.Remove,
		PersistDir: ModprobeDir,
	}
}

// Handle memvalidasi lalu mengerjakan satu permintaan.
//
// Urutannya selalu: cocokkan target dengan daftar dari sistem → baru
// jalankan. Tidak ada satu pun cabang di bawah yang meneruskan teks dari
// permintaan ke exec tanpa lewat daftar itu.
func (e *Executor) Handle(ctx context.Context, req Request) Response {
	out, err := e.handle(ctx, req)
	if err != nil {
		return Response{OK: false, Output: out, Error: err.Error()}
	}
	return Response{OK: true, Output: out}
}

func (e *Executor) handle(ctx context.Context, req Request) (string, error) {
	switch req.Action {
	case ScrubStart, ScrubStop, ScrubPause:
		pool, err := e.pool(ctx, req.Target)
		if err != nil {
			return "", err
		}
		args := []string{"scrub"}
		switch req.Action {
		case ScrubStop:
			args = append(args, "-s")
		case ScrubPause:
			args = append(args, "-p")
		}
		return e.Run(ctx, "zpool", append(args, pool)...)

	case SmartShort, SmartLong, SmartAbort:
		dev, err := e.device(ctx, req.Target)
		if err != nil {
			return "", err
		}
		var args []string
		switch req.Action {
		case SmartShort:
			args = []string{"-t", "short"}
		case SmartLong:
			args = []string{"-t", "long"}
		case SmartAbort:
			args = []string{"-X"}
		}
		return e.Run(ctx, "smartctl", append(args, dev...)...)

	case SmartRefresh:
		// --no-block: pengumpul SMART bisa makan puluhan detik per disk, dan
		// hasilnya memang dibaca dari berkas cache, bukan dari jawaban ini.
		return e.Run(ctx, "systemctl", "start", "--no-block", "issboard-smart.service")

	case ARCSet:
		return e.setARC(req.Value, req.Persist)
	}
	return "", fmt.Errorf("aksi tidak dikenal: %q", req.Action)
}

// pool mengembalikan nama pool HANYA kalau persis ada di `zpool list`.
func (e *Executor) pool(ctx context.Context, want string) (string, error) {
	out, err := e.Run(ctx, "zpool", "list", "-H", "-o", "name")
	if err != nil {
		return "", err
	}
	for _, name := range strings.Fields(out) {
		if name == want {
			return name, nil
		}
	}
	return "", fmt.Errorf("pool %q tidak ada di zpool list", want)
}

// device mengembalikan argumen smartctl untuk perangkat yang persis ada di
// `smartctl --scan`, TERMASUK `-d tipe` dari hasil scan itu sendiri.
//
// Argumen yang dikembalikan seluruhnya berasal dari keluaran scan, bukan dari
// permintaan: teks permintaan hanya dipakai untuk mencari barisnya.
func (e *Executor) device(ctx context.Context, want string) ([]string, error) {
	out, err := e.Run(ctx, "smartctl", "--scan")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == want {
			return append(f[1:], f[0]), nil
		}
	}
	return nil, fmt.Errorf("perangkat %q tidak ada di smartctl --scan", want)
}

func (e *Executor) setARC(v int64, persist bool) (string, error) {
	stats, err := e.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return "", fmt.Errorf("ZFS tidak termuat: %w", err)
	}
	_, _, cmin := parseArcstats(string(stats))
	mem, err := e.ReadFile("/proc/meminfo")
	if err != nil {
		return "", err
	}
	var total int64
	for _, line := range strings.Split(string(mem), "\n") {
		if s, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(s), " kB"), 10, 64)
			total = kb * 1024
		}
	}
	if err := ValidateARC(v, total, cmin); err != nil {
		return "", err
	}

	own := filepath.Join(e.PersistDir, filepath.Base(ARCPersistFile))

	// Konflik diperiksa SEBELUM apa pun diubah. modprobe membaca berkas
	// menurut abjad dan nilai terakhir menang, jadi zfs_arc_max di berkas
	// lain bisa diam-diam mengalahkan milik kita setelah reboot. Menulis
	// berkas yang kalah lalu melapor "tersimpan" adalah kebohongan.
	if persist {
		var lain []string
		for _, p := range ReadPersistedARC(e.PersistDir) {
			if filepath.Base(p.File) != filepath.Base(own) {
				lain = append(lain, p.File)
			}
		}
		if len(lain) > 0 {
			return "", fmt.Errorf("zfs_arc_max sudah diatur di %s — hapus atau samakan di sana dulu; "+
				"issboard tidak menyunting berkas milik orang lain", strings.Join(lain, ", "))
		}
	}

	if err := e.WriteFile(ARCParam, []byte(strconv.FormatInt(v, 10)+"\n")); err != nil {
		return "", fmt.Errorf("tulis %s: %w", ARCParam, err)
	}

	var msg []string
	// Baca ulang c_max: satu-satunya bukti bahwa ZFS menerima nilainya.
	if b, err := e.ReadFile("/proc/spl/kstat/zfs/arcstats"); err == nil {
		_, cmax, _ := parseArcstats(string(b))
		msg = append(msg, "c_max sekarang "+human(cmax))
		if v != 0 && cmax != v {
			msg = append(msg, "⚠ ZFS tidak memakai nilai yang diminta ("+human(v)+")")
		}
	}

	if persist {
		if v == 0 {
			if err := e.Remove(own); err != nil && !os.IsNotExist(err) {
				return strings.Join(msg, "; "), fmt.Errorf("hapus %s: %w", own, err)
			}
			msg = append(msg, "pengaturan permanen dihapus ("+own+")")
		} else {
			body := "# Ditulis issboard-helper. Hapus berkas ini untuk kembali ke bawaan ZFS.\n" +
				"options zfs zfs_arc_max=" + strconv.FormatInt(v, 10) + "\n"
			if err := e.WriteFile(own, []byte(body)); err != nil {
				return strings.Join(msg, "; "), fmt.Errorf("tulis %s: %w", own, err)
			}
			msg = append(msg, "disimpan di "+own)
		}
	}
	return strings.Join(msg, "; "), nil
}

// writeAtomic menulis lewat berkas sementara lalu rename, kecuali untuk /sys:
// berkas parameter modul bukan berkas biasa dan harus ditulis langsung.
func writeAtomic(path string, data []byte) error {
	if strings.HasPrefix(path, "/sys/") {
		return os.WriteFile(path, data, 0o644)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".issboard-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
