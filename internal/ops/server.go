package ops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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
	Now        func() time.Time
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
		Now:        time.Now,
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

	case SnapCreate:
		ds, err := e.dataset(ctx, req.Target)
		if err != nil {
			return "", err
		}
		if err := ValidTag(req.Name); err != nil {
			return "", err
		}
		name := SnapName(ds, req.Name, e.Now())
		args := []string{"snapshot"}
		if req.Recursive {
			args = append(args, "-r")
		}
		if _, err := e.Run(ctx, "zfs", append(args, name)...); err != nil {
			return "", err
		}
		return "dibuat " + name, nil

	case SnapDestroy:
		snap, err := e.snapshot(ctx, req.Target)
		if err != nil {
			return "", err
		}
		// Tanpa -r dan tanpa -R, selamanya: satu snapshot, satu nama. Snapshot
		// yang punya clone ditolak ZFS sendiri, dan itu memang yang diinginkan.
		if _, err := e.Run(ctx, "zfs", "destroy", snap); err != nil {
			return "", err
		}
		return "dihapus " + snap, nil

	case DSCreate:
		return e.createDataset(ctx, req)

	case DSSet:
		ds, err := e.dataset(ctx, req.Target)
		if err != nil {
			return "", err
		}
		if err := ValidateProp(req.Prop, req.PropValue); err != nil {
			return "", err
		}
		if req.PropValue == "inherit" {
			if _, err := e.Run(ctx, "zfs", "inherit", req.Prop, ds); err != nil {
				return "", err
			}
			return req.Prop + " " + ds + " kembali mewarisi induknya", nil
		}
		if _, err := e.Run(ctx, "zfs", "set", req.Prop+"="+req.PropValue, ds); err != nil {
			return "", err
		}
		return req.Prop + "=" + req.PropValue + " pada " + ds, nil

	case SanoidRun:
		// Lewat unit-nya, bukan memanggil sanoid langsung: unit itulah yang
		// dijalankan timer, dengan config dan lingkungan yang sama. Kalau
		// dashboard menjalankan sanoid dengan cara lain, hasil "berhasil" di
		// sini tidak membuktikan apa pun tentang jadwal yang sesungguhnya.
		return e.Run(ctx, "systemctl", "start", "--no-block", "sanoid.service")
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

// dataset mengembalikan nama filesystem/volume HANYA kalau persis ada.
func (e *Executor) dataset(ctx context.Context, want string) (string, error) {
	out, err := e.Run(ctx, "zfs", "list", "-H", "-o", "name", "-t", "filesystem,volume")
	if err != nil {
		return "", err
	}
	if contains(strings.Fields(out), want) {
		return want, nil
	}
	return "", fmt.Errorf("dataset %q tidak ada di zfs list", want)
}

func (e *Executor) snapshot(ctx context.Context, want string) (string, error) {
	if !strings.Contains(want, "@") {
		return "", fmt.Errorf("%q bukan nama snapshot", want)
	}
	out, err := e.Run(ctx, "zfs", "list", "-H", "-o", "name", "-t", "snapshot")
	if err != nil {
		return "", err
	}
	if contains(strings.Fields(out), want) {
		return want, nil
	}
	return "", fmt.Errorf("snapshot %q tidak ada di zfs list", want)
}

// createDataset membuat anak dataset TANPA me-mount-nya (-u), lalu me-mount
// lewat systemd-run.
//
// 🔴 Helper berjalan di mount namespace-nya sendiri (ProtectSystem=strict
// membuatnya). `zfs create` biasa akan me-mount dataset baru HANYA di dalam
// namespace itu — di host ia terlihat tidak ter-mount, lalu hilang begitu
// helper keluar. systemd-run menjalankan `zfs mount` sebagai unit sementara
// di namespace host, tempat mount itu memang harus berada.
func (e *Executor) createDataset(ctx context.Context, req Request) (string, error) {
	parent, err := e.dataset(ctx, req.Target)
	if err != nil {
		return "", err
	}
	if err := ValidName(req.Name); err != nil {
		return "", err
	}
	child := parent + "/" + req.Name
	if _, err := e.dataset(ctx, child); err == nil {
		return "", fmt.Errorf("dataset %q sudah ada", child)
	}

	args := []string{"create", "-u"}
	keys := make([]string, 0, len(req.Props))
	for k := range req.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := req.Props[k]
		if v == "" || v == "inherit" {
			continue // dataset baru sudah mewarisi dengan sendirinya
		}
		if err := ValidateProp(k, v); err != nil {
			return "", err
		}
		args = append(args, "-o", k+"="+v)
	}
	if _, err := e.Run(ctx, "zfs", append(args, child)...); err != nil {
		return "", err
	}

	msg := "dibuat " + child
	if _, err := e.Run(ctx, "systemd-run", "--wait", "--collect", "--quiet",
		"--unit=issboard-mount-"+strconv.FormatInt(e.Now().UnixNano(), 36),
		"zfs", "mount", child); err != nil {
		// Datasetnya SUDAH ada; gagal mount bukan alasan untuk mengaku gagal
		// total — orang perlu tahu dua fakta itu terpisah.
		msg += "; ⚠ belum ter-mount: " + err.Error()
	} else {
		msg += " dan ter-mount"
	}
	return msg, nil
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
