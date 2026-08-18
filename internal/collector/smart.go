package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// SmartDisk adalah ringkasan satu disk, sengaja kecil: dashboard butuh
// "memburuk atau tidak", bukan seluruh keluaran smartctl.
type SmartDisk struct {
	Device      string `json:"device"`
	Model       string `json:"model"`
	Serial      string `json:"serial"`
	Passed      bool   `json:"passed"`
	Temperature int    `json:"temperature_c"`
	PowerOnHrs  int64  `json:"power_on_hours"`
	// Reallocated adalah angka yang paling layak diawasi pada disk tua.
	Reallocated int64 `json:"reallocated_sectors"`
	PendingSect int64 `json:"pending_sectors"`
	// LastSelfTest kosong berarti belum pernah ada long test yang selesai.
	LastSelfTest string `json:"last_self_test"`
	Standby      bool   `json:"standby"`
}

type SmartReport struct {
	// WrittenAt berasal dari file cache, bukan dari saat pembacaan. Kalau ini
	// basi, artinya timer root-nya mati — itu sendiri sebuah temuan.
	WrittenAt time.Time   `json:"written_at"`
	Stale     bool        `json:"stale"`
	Disks     []SmartDisk `json:"disks"`
}

// ReadSmartCache membaca file JSON yang ditulis unit systemd milik root.
//
// 🔴 issboard TIDAK PERNAH memanggil smartctl sendiri. Satu panggilan
// membangunkan HDD yang sedang standby; dengan socket activation, satu kali
// buka halaman dari HP akan membangunkan seluruh disk. Lihat plan bagian 5.
func ReadSmartCache(path string) (SmartReport, error) {
	var r SmartReport
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Bukan error fatal: dashboard tetap berguna tanpa SMART.
			return SmartReport{Stale: true}, fmt.Errorf(
				"cache SMART belum ada di %s — apakah issboard-smart.timer aktif?", path)
		}
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("cache SMART %s rusak: %w", path, err)
	}
	// Timer menulis tiap 6 jam; lebih dari 12 jam berarti ada yang salah.
	r.Stale = time.Since(r.WrittenAt) > 12*time.Hour
	return r, nil
}
