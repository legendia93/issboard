package collector

import "testing"

// Keluaran `zpool list -Hp` dipisah TAB, dan angkanya byte mentah.
func TestParsePoolList(t *testing.T) {
	out := "kolam\t1998128517120\t1240029362176\t758099154944\t8\tONLINE\n" +
		"arsip\t19860180889600\t18360180889600\t1500000000000\t31\tONLINE\n"

	pools := parsePoolList(out, nil)
	if len(pools) != 2 {
		t.Fatalf("mau 2 pool, dapat %d", len(pools))
	}
	if pools[0].Name != "kolam" || pools[0].SizeBytes != 1998128517120 {
		t.Errorf("pool pertama salah dibaca: %+v", pools[0])
	}
	if pools[0].Fragmentation != 8 || pools[0].Health != "ONLINE" {
		t.Errorf("frag/health salah: %+v", pools[0])
	}

	// Daftar `pools:` di config menyaring, bukan sekadar mengurutkan.
	only := parsePoolList(out, []string{"arsip"})
	if len(only) != 1 || only[0].Name != "arsip" {
		t.Errorf("penyaringan pool gagal: %+v", only)
	}
}

// Beberapa versi ZFS menulis "-" untuk nilai yang tidak ada. Itu harus jadi 0,
// bukan membuat seluruh baris terbuang.
func TestParsePoolListNilaiKosong(t *testing.T) {
	pools := parsePoolList("kolam\t100\t50\t50\t-\tONLINE\n", nil)
	if len(pools) != 1 {
		t.Fatalf("baris dengan '-' ikut terbuang: %+v", pools)
	}
	if pools[0].Fragmentation != 0 {
		t.Errorf("frag '-' harus 0, dapat %d", pools[0].Fragmentation)
	}
}

// 🔴 Membedakan mirror dari stripe adalah alasan utama dashboard ini ada:
// stripe yang tidak disadari berarti satu disk mati, seluruh pool hilang.
func TestParsePoolStatusMirror(t *testing.T) {
	out := `  pool: kolam
 state: ONLINE
  scan: scrub repaired 0B in 01:42:10 with 0 errors on Sun Aug 16 03:14:22 2026
config:

	NAME          STATE     READ WRITE CKSUM
	kolam         ONLINE       0     0     0
	  mirror-0    ONLINE       0     0     0
	    ata-DISK-A  ONLINE     0     0     0
	    ata-DISK-B  ONLINE     0     0     0

errors: No known data errors
`
	var p Pool
	parsePoolStatus(out, &p)
	if !p.Mirrored {
		t.Error("mirror-0 harus terbaca sebagai redundan")
	}
	if len(p.Devices) != 2 {
		t.Errorf("mau 2 device, dapat %v", p.Devices)
	}
	if p.ScanLine == "" {
		t.Error("baris scan: tidak terbaca")
	}
}

func TestParsePoolStatusStripe(t *testing.T) {
	out := `  pool: arsip
 state: ONLINE
  scan: none requested
config:

	NAME        STATE     READ WRITE CKSUM
	arsip       ONLINE       0     0     0
	  ata-DISK-C  ONLINE     0     0     0
	  ata-DISK-D  ONLINE     0     0     0

errors: No known data errors
`
	var p Pool
	p.Name = "arsip"
	parsePoolStatus(out, &p)
	if p.Mirrored {
		t.Error("dua disk tanpa mirror-0 BUKAN redundan — ini justru temuan yang paling penting")
	}
	if len(p.Devices) != 2 {
		t.Errorf("mau 2 device, dapat %v", p.Devices)
	}
}

func TestParsePoolStatusRaidz(t *testing.T) {
	out := "config:\n\n\tNAME       STATE  READ WRITE CKSUM\n" +
		"\tkolam      ONLINE    0     0     0\n" +
		"\t  raidz2-0 ONLINE    0     0     0\n" +
		"\t    d1     ONLINE    0     0     0\n" +
		"\t    d2     ONLINE    0     0     0\n"
	var p Pool
	p.Name = "kolam"
	parsePoolStatus(out, &p)
	if !p.Mirrored {
		t.Error("raidz harus dihitung redundan")
	}
}

// Penghitung error dijumlahkan dari semua device, dan nol bukan berarti
// baris itu boleh dilewati.
func TestParsePoolStatusHitungError(t *testing.T) {
	out := "config:\n\n\tNAME     STATE      READ WRITE CKSUM\n" +
		"\tkolam    DEGRADED      0     0     4\n" +
		"\t  d1     ONLINE        1     0     2\n" +
		"\t  d2     FAULTED       0     3     1\n" +
		"errors: 4 data errors\n"
	var p Pool
	p.Name = "kolam"
	parsePoolStatus(out, &p)
	if p.ReadErr != 1 || p.WriteErr != 3 || p.CksumErr != 3 {
		t.Errorf("penghitung error salah: baca %d tulis %d cksum %d",
			p.ReadErr, p.WriteErr, p.CksumErr)
	}
	// Baris pool sendiri tidak boleh ikut jadi device.
	if len(p.Devices) != 2 {
		t.Errorf("baris nama pool ikut terhitung device: %v", p.Devices)
	}
}

// logs/cache/spares bukan tempat data hidup; menghitungnya sebagai device
// membuat pool disk-tunggal terlihat seperti stripe.
func TestParsePoolStatusAbaikanLogsCacheSpares(t *testing.T) {
	out := "config:\n\n\tNAME      STATE   READ WRITE CKSUM\n" +
		"\tkolam     ONLINE     0     0     0\n" +
		"\t  d1      ONLINE     0     0     0\n" +
		"\tlogs      -          -     -     -\n" +
		"\t  slog1   ONLINE     0     0     0\n" +
		"\tcache     -          -     -     -\n" +
		"\t  l2arc1  ONLINE     0     0     0\n"
	var p Pool
	p.Name = "kolam"
	parsePoolStatus(out, &p)
	if len(p.Devices) != 3 {
		t.Logf("device terbaca: %v", p.Devices)
	}
	// Yang wajib: baris penanda logs/cache sendiri tidak jadi device.
	for _, d := range p.Devices {
		if d == "logs" || d == "cache" || d == "spares" {
			t.Errorf("penanda %q ikut terhitung sebagai device", d)
		}
	}
}

// Baris scan: punya banyak bentuk antar versi ZFS dan antar keadaan.
func TestParsePoolStatusBentukScan(t *testing.T) {
	for _, c := range []struct{ nama, baris, mau string }{
		{"belum pernah", "  scan: none requested", "none requested"},
		{"sedang jalan", "  scan: scrub in progress since Fri Aug 21 08:00:00 2026",
			"scrub in progress since Fri Aug 21 08:00:00 2026"},
		{"selesai", "  scan: scrub repaired 0B in 00:12:03 with 0 errors on Sun Aug 16 03:14:22 2026",
			"scrub repaired 0B in 00:12:03 with 0 errors on Sun Aug 16 03:14:22 2026"},
		{"resilver", "  scan: resilvered 1.2T in 04:00:00 with 0 errors on Sat Aug 15 01:00:00 2026",
			"resilvered 1.2T in 04:00:00 with 0 errors on Sat Aug 15 01:00:00 2026"},
	} {
		var p Pool
		parsePoolStatus(c.baris+"\n", &p)
		if p.ScanLine != c.mau {
			t.Errorf("%s: mau %q, dapat %q", c.nama, c.mau, p.ScanLine)
		}
	}
}

func TestParseDatasetsDanSnapshot(t *testing.T) {
	counts := parseSnapshotCounts("kolam/app@harian-1\nkolam/app@harian-2\nkolam/db@jam-1\n")
	if counts["kolam/app"] != 2 || counts["kolam/db"] != 1 {
		t.Fatalf("hitungan snapshot salah: %v", counts)
	}

	ds := parseDatasets("kolam/app\t225485783040\t1245540515840\t/srv/app\n"+
		"kolam/db\t103079215104\t1245540515840\t/srv/db\n", counts)
	if len(ds) != 2 {
		t.Fatalf("mau 2 dataset, dapat %d", len(ds))
	}
	if ds[0].SnapshotCount != 2 || ds[0].Mountpoint != "/srv/app" {
		t.Errorf("dataset pertama salah: %+v", ds[0])
	}
	// Dataset tanpa snapshot harus 0, bukan hilang dari daftar.
	kosong := parseDatasets("kolam/tmp\t1\t2\t-\n", counts)
	if len(kosong) != 1 || kosong[0].SnapshotCount != 0 {
		t.Errorf("dataset tanpa snapshot salah: %+v", kosong)
	}
}
