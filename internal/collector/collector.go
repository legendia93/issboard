// Package collector mengumpulkan data kesehatan host.
//
// Aturan keras dari docs/design.md §3.3: TIDAK ADA collector yang boleh memanggil
// smartctl di jalur request. SMART hanya dibaca dari cache JSON yang ditulis
// unit systemd milik root, karena smartctl membangunkan HDD yang sedang tidur.
package collector

import (
	"context"
	"sync"
	"time"
)

// Snapshot adalah seluruh data yang dilayani API pada satu titik waktu.
type Snapshot struct {
	CollectedAt time.Time   `json:"collected_at"`
	System      System      `json:"system"`
	Host        Host        `json:"host"`
	Pools       []Pool      `json:"pools"`
	Datasets    []Dataset   `json:"datasets"`
	Containers  []Container `json:"containers"`
	Smart       SmartReport `json:"smart"`
	Errors      []string    `json:"errors,omitempty"`
}

// Cache menyimpan hasil per-bagian dengan TTL masing-masing. Interval berbeda
// karena biayanya berbeda: ZFS murah (dari ARC), container murah, SMART mahal
// (membangunkan disk) sehingga tidak pernah diambil sendiri di sini.
type Cache struct {
	mu sync.Mutex

	pools      cached[[]Pool]
	datasets   cached[[]Dataset]
	containers cached[[]Container]
	host       cached[Host]
	smart      cached[SmartReport]
	system     cached[System]

	// prevCPU adalah cuplikan /proc/stat sebelumnya. Pemakaian CPU tidak bisa
	// dibaca sekali jalan — hanya selisih dua cuplikan yang berarti.
	prevCPU CPUTimes
}

type cached[T any] struct {
	val T
	// err disimpan bersama nilainya, bukan cuma dilaporkan sekali saat
	// pengambilan gagal. Tanpa ini, error hanya muncul di satu permintaan
	// lalu hilang selama TTL — dan karena halaman polling tiap 15 detik,
	// host tanpa ZFS akan hampir selalu terlihat baik-baik saja. Layar yang
	// tampak sehat karena buta lebih berbahaya daripada layar yang mengaku
	// tidak tahu, jadi errornya ikut sesegar-basi nilainya.
	err error
	at  time.Time
	ttl time.Duration
}

func (c *cached[T]) fresh() bool { return !c.at.IsZero() && time.Since(c.at) < c.ttl }

func (c *cached[T]) set(v T, err error) {
	c.val = v
	c.err = err
	c.at = time.Now()
}

func NewCache() *Cache {
	c := &Cache{}
	c.pools.ttl = 60 * time.Second
	c.datasets.ttl = 60 * time.Second
	c.containers.ttl = 30 * time.Second
	c.host.ttl = 15 * time.Second
	c.smart.ttl = 5 * time.Minute // TTL pembacaan FILE cache, bukan smartctl
	// Distro, kernel, dan model CPU tidak berubah selama proses hidup.
	// Suhu berubah, tapi ikut menumpang siklus yang sama supaya tidak ada
	// pembacaan /sys tambahan tiap 15 detik.
	c.system.ttl = 60 * time.Second
	return c
}

// Options yang dibutuhkan collector dari konfigurasi.
type Options struct {
	SmartCache   string
	DockerSocket string
	Pools        []string
}

// Collect mengembalikan snapshot, memakai cache di mana masih segar.
func (c *Cache) Collect(ctx context.Context, o Options) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := Snapshot{CollectedAt: time.Now()}
	var errs []string
	note := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	if !c.host.fresh() {
		h, err := CollectHost(ctx)
		if cur := ReadCPUTimes(); cur.Valid() {
			h.CPUPercent = CPUPercent(c.prevCPU, cur)
			c.prevCPU = cur
		}
		c.host.set(h, err)
	}
	s.Host = c.host.val
	note(c.host.err)

	if !c.system.fresh() {
		c.system.set(CollectSystem(), nil)
	}
	s.System = c.system.val

	if !c.pools.fresh() {
		c.pools.set(CollectPools(ctx, o.Pools))
	}
	s.Pools = c.pools.val
	note(c.pools.err)

	if !c.datasets.fresh() {
		c.datasets.set(CollectDatasets(ctx))
	}
	s.Datasets = c.datasets.val
	note(c.datasets.err)

	if !c.containers.fresh() {
		c.containers.set(CollectContainers(ctx, o.DockerSocket))
	}
	s.Containers = c.containers.val
	note(c.containers.err)

	if !c.smart.fresh() {
		c.smart.set(ReadSmartCache(o.SmartCache))
	}
	s.Smart = c.smart.val
	note(c.smart.err)

	s.Errors = errs
	return s
}
