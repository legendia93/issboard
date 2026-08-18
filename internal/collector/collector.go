// Package collector mengumpulkan data kesehatan host.
//
// Aturan keras dari plan bagian 5: TIDAK ADA collector yang boleh memanggil
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
}

type cached[T any] struct {
	val T
	at  time.Time
	ttl time.Duration
}

func (c *cached[T]) fresh() bool { return !c.at.IsZero() && time.Since(c.at) < c.ttl }

func (c *cached[T]) set(v T) {
	c.val = v
	c.at = time.Now()
}

func NewCache() *Cache {
	c := &Cache{}
	c.pools.ttl = 60 * time.Second
	c.datasets.ttl = 60 * time.Second
	c.containers.ttl = 30 * time.Second
	c.host.ttl = 15 * time.Second
	c.smart.ttl = 5 * time.Minute // TTL pembacaan FILE cache, bukan smartctl
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
		note(err)
		c.host.set(h)
	}
	s.Host = c.host.val

	if !c.pools.fresh() {
		p, err := CollectPools(ctx, o.Pools)
		note(err)
		c.pools.set(p)
	}
	s.Pools = c.pools.val

	if !c.datasets.fresh() {
		d, err := CollectDatasets(ctx)
		note(err)
		c.datasets.set(d)
	}
	s.Datasets = c.datasets.val

	if !c.containers.fresh() {
		ct, err := CollectContainers(ctx, o.DockerSocket)
		note(err)
		c.containers.set(ct)
	}
	s.Containers = c.containers.val

	if !c.smart.fresh() {
		sm, err := ReadSmartCache(o.SmartCache)
		note(err)
		c.smart.set(sm)
	}
	s.Smart = c.smart.val

	s.Errors = errs
	return s
}
