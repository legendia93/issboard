package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tulisCache(t *testing.T, isi string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "smart.json")
	if err := os.WriteFile(p, []byte(isi), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadSmartCache(t *testing.T) {
	isi := `{"written_at":"` + time.Now().Format(time.RFC3339) + `","disks":[
	 {"device":"/dev/sda","model":"CONTOH 2TB","passed":true,"temperature_c":34,
	  "reallocated_sectors":0,"pending_sectors":0,"standby":false},
	 {"device":"/dev/sdb","model":"CONTOH 10TB","passed":true,"temperature_c":0,
	  "reallocated_sectors":88,"pending_sectors":0,"standby":true}]}`

	r, err := ReadSmartCache(tulisCache(t, isi))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Disks) != 2 {
		t.Fatalf("mau 2 disk, dapat %d", len(r.Disks))
	}
	if r.Stale {
		t.Error("cache yang baru ditulis tidak boleh dianggap basi")
	}
	if r.Disks[1].Reallocated != 88 || !r.Disks[1].Standby {
		t.Errorf("disk kedua salah dibaca: %+v", r.Disks[1])
	}
}

// 🔴 Timer pengumpul yang mati adalah temuan tersendiri, bukan sekadar
// ketiadaan data: tanpa penanda ini layar terlihat sehat justru karena buta.
func TestReadSmartCacheBasi(t *testing.T) {
	isi := `{"written_at":"` + time.Now().Add(-30*time.Hour).Format(time.RFC3339) + `","disks":[]}`
	r, err := ReadSmartCache(tulisCache(t, isi))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale {
		t.Error("cache berumur 30 jam harus ditandai basi")
	}
}

func TestReadSmartCacheBatasBasi(t *testing.T) {
	// Ambangnya 12 jam; 11 jam masih segar, 13 jam sudah basi.
	for _, c := range []struct {
		umur time.Duration
		basi bool
	}{{11 * time.Hour, false}, {13 * time.Hour, true}} {
		isi := `{"written_at":"` + time.Now().Add(-c.umur).Format(time.RFC3339) + `","disks":[]}`
		r, _ := ReadSmartCache(tulisCache(t, isi))
		if r.Stale != c.basi {
			t.Errorf("umur %s: mau basi=%v, dapat %v", c.umur, c.basi, r.Stale)
		}
	}
}

// Cache yang belum pernah ada BUKAN kegagalan fatal — dashboard tetap berguna
// tanpa SMART — tapi wajib ditandai basi supaya tidak terlihat sehat.
func TestReadSmartCacheBelumAda(t *testing.T) {
	r, err := ReadSmartCache(filepath.Join(t.TempDir(), "tidak-ada.json"))
	if err == nil {
		t.Error("ketiadaan cache harus dilaporkan sebagai error yang terlihat")
	}
	if !r.Stale {
		t.Error("cache yang belum ada harus ditandai basi")
	}
}

func TestReadSmartCacheRusak(t *testing.T) {
	if _, err := ReadSmartCache(tulisCache(t, "{ini bukan json")); err == nil {
		t.Error("cache rusak harus jadi error, bukan laporan kosong")
	}
}

// written_at yang kosong berarti berkasnya belum pernah benar-benar diisi.
func TestReadSmartCacheTanpaStempel(t *testing.T) {
	r, _ := ReadSmartCache(tulisCache(t, `{"disks":[]}`))
	if !r.Stale {
		t.Error("cache tanpa written_at harus dianggap basi")
	}
}
