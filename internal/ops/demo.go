package ops

import "time"

// DemoARC dan DemoSchedule melayani mode demo: tidak ada /proc, tidak ada
// systemctl yang disentuh. Bentuknya sengaja memuat kasus yang ingin terlihat
// di UI — ARC yang dibatasi permanen, scrub yang dijadwalkan dua kali.

func DemoARC() ARCInfo {
	mem := int64(32) << 30
	a := ARCInfo{
		Present: true, SizeBytes: 11 << 30, CMax: 12 << 30, CMin: 1 << 30,
		Param: 12 << 30, MemTotal: mem,
		Persisted: []PersistedARC{{File: ARCPersistFile, Value: 12 << 30, Ours: true}},
	}
	a.Lower, a.Upper = ARCBounds(mem, a.CMin)
	return a
}

func DemoSchedule() Schedule {
	now := time.Now()
	at := func(d time.Duration) *time.Time { t := now.Add(d).Truncate(time.Minute); return &t }
	s := Schedule{
		Timers: []Timer{
			{Unit: "issboard-agent.timer", Activates: "issboard-agent.service", Next: at(40 * time.Second), Last: at(-20 * time.Second)},
			{Unit: "issboard-smart.timer", Activates: "issboard-smart.service", Next: at(4 * time.Hour), Last: at(-2 * time.Hour)},
			{Unit: "sanoid.timer", Activates: "sanoid.service", Next: at(9 * time.Minute), Last: at(-6 * time.Minute)},
			{Unit: "zfs-scrub-monthly@kolam.timer", Activates: "zfs-scrub@kolam.service", Next: at(16 * 24 * time.Hour), Last: nil},
		},
		Other: 17,
		Cron: []CronEntry{
			{File: "/etc/cron.d/zfsutils-linux", Schedule: "24 0 8-14 * *", User: "root",
				Command: "if [ $(date +\\%w) -eq 0 ] && [ -x /usr/lib/zfs-linux/scrub ]; then /usr/lib/zfs-linux/scrub; fi"},
			{File: "/etc/cron.d/zfsutils-linux", Schedule: "24 0 1-7 * *", User: "root",
				Command: "if [ $(date +\\%w) -eq 0 ] && [ -x /usr/lib/zfs-linux/trim ]; then /usr/lib/zfs-linux/trim; fi"},
		},
		Smartd: &Smartd{File: "/etc/smartd.conf", Lines: []string{"DEVICESCAN -d removable -n standby -m root"}},
	}
	s.Notes = scheduleNotes(s, true)
	return s
}

// DemoSnapshots memuat campuran buatan sanoid dan buatan tangan, supaya
// perbedaan "ikut dipangkas otomatis atau tidak" terlihat di UI.
func DemoSnapshots(dataset string) []Snapshot {
	now := time.Now().Truncate(time.Minute)
	var s []Snapshot
	for i, d := range []time.Duration{20 * time.Minute, 80 * time.Minute, 26 * time.Hour} {
		s = append(s, Snapshot{
			Name: dataset + "@autosnap_" + now.Add(-d).Format("2006-01-02_15:04:05") + "_hourly",
			Used: int64(i+1) * 37 << 20, Referenced: 20 << 30, Created: now.Add(-d),
		})
	}
	s = append(s, Snapshot{Name: dataset + "@issboard_2026-08-14_10:00:00_migrasi",
		Used: 3 << 30, Referenced: 18 << 30, Created: time.Date(2026, 8, 14, 10, 0, 0, 0, time.Local)})
	return s
}

func DemoProps() []Prop {
	return []Prop{
		{"atime", "off", "inherited from pool-cepat"},
		{"compression", "zstd", "local"},
		{"quota", "none", "default"},
		{"readonly", "off", "default"},
		{"recordsize", "128K", "default"},
		{"refquota", "none", "default"},
		{"refreservation", "none", "default"},
		{"relatime", "on", "default"},
		{"reservation", "none", "default"},
	}
}
