package collector

import (
	"math"
	"runtime"
	"time"
)

// DemoSnapshot mengembalikan data palsu yang tidak menyentuh sistem sama sekali.
//
// Ada dua alasan, dan yang kedua yang membuatnya dikerjakan sejak awal:
//
//  1. Tampilan kondisi sakit — disk memburuk, pool hampir penuh, container Up
//     tanpa network — mustahil digarap kalau harus menunggu hal itu benar-benar
//     terjadi di mesin nyata. Tanpa ini, aturan "sehat pastel, bermasalah pekat"
//     tidak bisa dilihat hasilnya.
//
//  2. Dashboard ini menampilkan hostname, alamat IP, nama pool, dan nama app.
//     Mode demo membuat screenshot dan rekaman layar aman dibagikan — persis
//     hal yang .gitignore repo ini susah payah jaga.
//
// Semua nama di bawah sengaja generik. Jangan pernah menaruh nama mesin nyata
// di sini: berkas ini publik.
func DemoSnapshot() Snapshot {
	now := time.Now()

	// Sedikit gerakan supaya angkanya tidak terlihat beku, tapi tetap
	// deterministik terhadap waktu — bukan acak, supaya screenshot bisa diulang.
	wobble := func(base, amp float64, periodSec float64) float64 {
		return base + amp*math.Sin(float64(now.Unix())/periodSec)
	}

	const gib = int64(1) << 30

	return Snapshot{
		CollectedAt: now,
		System: System{
			Distro:    "Demo Linux 13 (contoh)",
			Kernel:    "6.12.0-demo-amd64",
			Arch:      "amd64",
			CPUModel:  "Demo CPU 8-Core @ 3.4GHz",
			CPUCores:  8,
			CPUTempC:  round2(wobble(48, 3, 120)),
			GoVersion: runtime.Version(),
		},
		Host: Host{
			CPUPercent:    round2(wobble(18, 9, 110)),
			Hostname:      "demo-host",
			Uptime:        int64(37*24*time.Hour/time.Second) + now.Unix()%3600,
			Load1:         round2(wobble(0.42, 0.18, 90)),
			Load5:         round2(wobble(0.55, 0.12, 240)),
			Load15:        0.61,
			MemTotalBytes: 32 * gib,
			MemAvailBytes: int64(wobble(11.5, 0.9, 150) * float64(gib)),
			SwapTotal:     8 * gib,
			SwapFree:      8 * gib,
			ARCSizeBytes:  int64(wobble(9.8, 0.4, 300) * float64(gib)),
			ARCMaxBytes:   16 * gib,
			ARCHitRatio:   round2(wobble(97.4, 1.2, 200)),
		},
		Pools: []Pool{
			{
				// Sehat sepenuhnya: mirror, baru di-scrub, masih longgar.
				Name: "pool-cepat", Health: "ONLINE",
				SizeBytes: 1800 * gib, AllocBytes: 640 * gib, FreeBytes: 1160 * gib,
				Fragmentation: 8,
				ScanLine:      "scrub repaired 0B in 01:42:10 with 0 errors on " + ctime(now.Add(-6*24*time.Hour)),
				Mirrored:      true,
				Devices:       []string{"ata-DEMO-DISK-A", "ata-DEMO-DISK-B"},
			},
			{
				// Dua masalah sekaligus: stripe (tanpa redundansi) dan hampir
				// penuh. Justru kombinasi seperti inilah yang perlu terlihat.
				Name: "pool-arsip", Health: "ONLINE",
				SizeBytes: 18500 * gib, AllocBytes: 17100 * gib, FreeBytes: 1400 * gib,
				Fragmentation: 31,
				ScanLine:      "scrub repaired 0B in 09:12:44 with 0 errors on " + ctime(now.Add(-52*24*time.Hour)),
				Mirrored:      false,
				Devices:       []string{"ata-DEMO-DISK-C", "ata-DEMO-DISK-D"},
			},
			{
				// Belum pernah di-scrub, dan sudah mencatat error checksum.
				Name: "pool-uji", Health: "DEGRADED",
				SizeBytes: 500 * gib, AllocBytes: 120 * gib, FreeBytes: 380 * gib,
				Fragmentation: 3,
				ScanLine:      "none requested",
				Mirrored:      true,
				Devices:       []string{"ata-DEMO-DISK-E", "ata-DEMO-DISK-F"},
				CksumErr:      4,
			},
		},
		Datasets: []Dataset{
			{Name: "pool-cepat/app", UsedBytes: 210 * gib, AvailBytes: 1160 * gib, Mountpoint: "/srv/app", SnapshotCount: 148},
			{Name: "pool-cepat/basis-data", UsedBytes: 96 * gib, AvailBytes: 1160 * gib, Mountpoint: "/srv/db", SnapshotCount: 312},
			{Name: "pool-arsip/rekaman", UsedBytes: 16800 * gib, AvailBytes: 1400 * gib, Mountpoint: "/srv/rekaman", SnapshotCount: 0},
			{Name: "pool-uji/coba", UsedBytes: 118 * gib, AvailBytes: 380 * gib, Mountpoint: "/srv/coba", SnapshotCount: 2},
		},
		Containers: []Container{
			{Name: "web", Image: "demo/web:1.4.2", State: "running", Status: "Up 6 days",
				Health: "healthy", Networks: []string{"jaring-depan"},
				PublishedPorts: []string{"127.0.0.1:8080->8080/tcp"}},
			{Name: "basis-data", Image: "demo/postgres:16", State: "running", Status: "Up 6 days",
				Networks: []string{"jaring-belakang"},
				// Port database terbuka ke seluruh jaringan — nyaris selalu
				// tidak disengaja, dan tidak pernah muncul sendiri di layar.
				PublishedPorts: []string{"0.0.0.0:5432->5432/tcp"}},
			{Name: "pekerja", Image: "demo/worker:0.9", State: "running", Status: "Up 2 hours",
				// Jebakan mahal: statusnya Up, jadi semuanya terlihat normal.
				Networks: nil},
			{Name: "cache", Image: "demo/redis:7", State: "running", Status: "Up 6 days",
				Health: "unhealthy", Networks: []string{"jaring-belakang"}},
			{Name: "pencadang", Image: "demo/backup:2.1", State: "exited", Status: "Exited (1) 3 hours ago",
				Networks: []string{"jaring-belakang"}},
		},
		Smart: SmartReport{
			// Sengaja segar: supaya tampilan "cache basi" bisa diuji terpisah
			// dengan menyetel ini ke waktu lampau.
			WrittenAt: now.Add(-2 * time.Hour),
			Stale:     false,
			Disks: []SmartDisk{
				{Device: "/dev/sda", Model: "DEMO SSD 2TB", Serial: "DEMO-0001", Passed: true,
					Temperature: 34, PowerOnHrs: 14820, LastSelfTest: "Completed without error"},
				{Device: "/dev/sdb", Model: "DEMO SSD 2TB", Serial: "DEMO-0002", Passed: true,
					Temperature: 36, PowerOnHrs: 14818, LastSelfTest: "Completed without error"},
				// Disk tua di pool stripe: reallocated sudah banyak. Scrub bisa
				// memberi tahu, tapi tanpa redundansi tidak bisa memperbaiki.
				{Device: "/dev/sdc", Model: "DEMO HDD 10TB", Serial: "DEMO-0003", Passed: true,
					Temperature: 41, PowerOnHrs: 41230, Reallocated: 88,
					LastSelfTest: "Completed without error"},
				{Device: "/dev/sdd", Model: "DEMO HDD 10TB", Serial: "DEMO-0004", Passed: true,
					Temperature: 52, PowerOnHrs: 41190, Reallocated: 2, PendingSect: 8,
					LastSelfTest: "Completed: read failure"},
				// Disk yang sedang tidur: suhunya memang tidak terbaca, dan itu
				// bukan temuan — membangunkannya justru yang dihindari.
				{Device: "/dev/sde", Model: "DEMO HDD 4TB", Serial: "DEMO-0005", Passed: true,
					Standby: true, PowerOnHrs: 9110},
			},
		},
		Errors: []string{"mode demo aktif — seluruh data di halaman ini palsu"},
	}
}

// ctime meniru format tanggal yang dipakai `zpool status` pada baris scan:,
// supaya parser scrub yang sesungguhnya ikut teruji di mode demo.
func ctime(t time.Time) string { return t.Format("Mon Jan _2 15:04:05 2006") }

func round2(f float64) float64 { return math.Round(f*100) / 100 }
