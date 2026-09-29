// issboard-agent — menulis riwayat dan mengirim notifikasi.
//
// 🔴 Ini SENGAJA program terpisah, bukan mode baru di dalam issboard.
//
// issboard memakai socket activation: kalau halamannya tidak dibuka, prosesnya
// tidak hidup, jadi ia secara desain tidak bisa jadi sumber alert
// (docs/design.md §3.2). Godaannya adalah menambal itu dengan menjadikannya
// daemon — dan itu menukar keuntungan terbesarnya (RAM idle 0 MB, tidak ada
// permukaan serang saat tidak ada yang melihat) demi sesuatu yang bisa
// dikerjakan proses berumur beberapa milidetik tiap menit.
//
// Program ini hidup sebentar, menulis dua berkas, mengirim paling banyak satu
// pesan, lalu keluar. RSS kembali nol. Riwayatnya ada di berkas, jadi ia juga
// selamat dari reboot — sesuatu yang daemon in-memory justru tidak punya.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/legendia93/issboard/internal/alert"
	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/config"
	"github.com/legendia93/issboard/internal/health"
	"github.com/legendia93/issboard/internal/history"
	"github.com/legendia93/issboard/internal/notify"
)

func main() {
	log.SetFlags(0) // journald sudah memberi stempel waktu sendiri
	os.Exit(run())
}

func run() int {
	cfgPath := flag.String("config", "/etc/issboard.yaml", "berkas konfigurasi")
	demo := flag.Bool("demo", false, "data palsu; tidak menyentuh sistem dan tidak menulis berkas apa pun")
	dry := flag.Bool("dry-run", false, "cetak pesannya, jangan kirim, jangan tandai sudah dikabari")
	test := flag.Bool("test", false, "kirim SATU pesan uji ke semua kanal lalu keluar; tidak mengumpulkan, tidak menulis apa pun")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Printf("agent: config %s: %v", *cfgPath, err)
		return 1
	}
	if *demo {
		cfg.Demo = true
	}

	ch := notify.Config{
		NtfyURL:        cfg.NtfyURL,
		NtfyTopic:      cfg.NtfyTopic,
		NtfyToken:      cfg.NtfyToken,
		TelegramToken:  cfg.TelegramToken,
		TelegramChatID: cfg.TelegramChatID,
	}

	if *test {
		return sendTest(ch, cfg)
	}

	// Batas waktu keseluruhan. Agent ini dipanggil tiap menit: satu jalannya
	// yang menggantung selamanya (socket Docker yang tidak menjawab, ntfy yang
	// diam) akan menumpuk proses sampai mesinnya sendiri yang jadi masalah.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	now := time.Now()
	snap := collect(ctx, cfg)

	// Riwayat ditulis lebih dulu: kalau kanal notifikasi bermasalah, sparkline
	// tidak ikut kehilangan datanya.
	if !cfg.Demo {
		writeHistory(cfg.HistoryFile, &snap)
	}

	fs := health.Evaluate(snap)

	// Siklus dengan error pengumpulan tidak boleh dibaca sebagai "semuanya
	// beres". Kalau `zpool` gagal dipanggil, seluruh temuan pool ikut hilang
	// dari daftar — dan mengabarkan "pool sudah pulih" persis saat kita
	// kehilangan kemampuan melihat pool adalah kebohongan yang paling mahal
	// di program ini.
	// Data demo memang menaruh satu "error" bikinan supaya dashboard bisa
	// mengaku datanya palsu; itu bukan kegagalan pengumpulan, dan tidak boleh
	// ikut muncul di log sebagai kegagalan.
	settled := cfg.Demo || len(snap.Errors) == 0
	if !cfg.Demo {
		for _, e := range snap.Errors {
			log.Printf("agent: pengumpulan gagal sebagian: %s", e)
		}
	}

	opts := alert.Options{
		Now:      now,
		MinLevel: alert.ParseLevel(cfg.NotifyMinLevel),
		Repeat:   cfg.AlertRepeat,
		Settled:  settled,
	}

	// Mode demo tidak menyentuh state di disk sama sekali: tiap kali dijalankan
	// ia jadi "siklus pertama", yang memang bentuk pesan yang paling ingin
	// dilihat saat mencoba kanal notifikasi.
	st := alert.State{Active: map[string]alert.Entry{}}
	if !cfg.Demo {
		if st, err = alert.Load(cfg.AlertState); err != nil {
			log.Printf("agent: state alert %s: %v (dianggap mesin baru)", cfg.AlertState, err)
		}
	}

	d := st.Plan(fs, opts)
	code := 0

	if d.Any() {
		title, body := alert.Compose(d, snap.Host.Hostname, now)
		switch {
		case *dry:
			fmt.Printf("%s\n\n%s\n", title, body)
		case !ch.Enabled():
			// Sengaja TIDAK ditandai sudah dikabari: begitu kanalnya diisi,
			// kondisi yang sedang berlangsung langsung terkirim sekali,
			// bukan menunggu sesuatu memburuk dulu.
			log.Printf("agent: %s — tidak ada kanal notifikasi yang dikonfigurasi", title)
		default:
			msg := notify.Message{
				Title: title, Body: body, Urgent: d.Urgent(),
				// Hanya kabar pulih: tidak perlu berbunyi seperti alarm.
				Resolved: len(d.New)+len(d.Worse)+len(d.Reminder) == 0,
			}
			if err := ch.Send(ctx, msg); err != nil {
				// Keluar dengan kode error supaya unitnya terlihat gagal di
				// `systemctl status`. State sengaja tidak ditandai, jadi
				// siklus berikutnya mencoba lagi — alert yang hilang karena
				// jaringan sedang putus adalah kegagalan yang paling ingin
				// dihindari proyek ini.
				log.Printf("agent: gagal mengirim: %v", err)
				code = 1
			} else {
				log.Printf("agent: terkirim lewat %v — %s", ch.Channels(), title)
				st.MarkSent(d, now)
			}
		}
	}

	// Observe berjalan terkirim atau tidak: kehadiran temuan dicatat supaya
	// umurnya benar, sedangkan yang menandai "sudah dikabari" hanya MarkSent.
	st.Observe(fs, opts)
	if !cfg.Demo && !*dry {
		if err := alert.Save(cfg.AlertState, st); err != nil {
			log.Printf("agent: menulis state alert: %v", err)
			code = 1
		}
	}

	v := health.Summarize(fs)
	log.Printf("agent: %d temuan (%d kritis, %d perhatian), %d sedang diingat",
		len(fs), v.Crit, v.Warn, len(st.Active))
	return code
}

func collect(ctx context.Context, cfg config.Config) collector.Snapshot {
	if cfg.Demo {
		return collector.DemoSnapshot()
	}
	// Cache baru tiap kali: proses ini tidak hidup cukup lama untuk mendapat
	// manfaat TTL. Yang dipakai ulang adalah kodenya — termasuk cara error
	// per-bagian dikumpulkan, supaya agent melihat snapshot yang bentuknya
	// persis sama dengan yang dilihat dashboard.
	return collector.NewCache().Collect(ctx, collector.Options{
		SmartCache:     cfg.SmartCache,
		DockerSocket:   cfg.DockerSocket,
		Pools:          cfg.Pools,
		SnapPolicyFile: cfg.SnapPolicyFile,
		SnapExempt:     cfg.SnapExempt,
	})
}

// writeHistory menambahkan satu titik ke ring buffer dan menghitung pemakaian
// CPU dari cuplikan siklus sebelumnya.
//
// Kegagalannya sengaja tidak fatal: riwayat untuk sparkline adalah kenyamanan,
// sedangkan notifikasi adalah alasan program ini ada. Berkas riwayat yang tidak
// bisa ditulis tidak boleh ikut membungkam alert.
func writeHistory(path string, snap *collector.Snapshot) {
	if path == "" {
		return
	}
	h, err := history.Load(path)
	if err != nil {
		log.Printf("agent: riwayat %s: %v (memulai riwayat baru)", path, err)
	}

	// Pemakaian CPU butuh dua cuplikan /proc/stat, dan proses ini hidup
	// beberapa milidetik. Cuplikan siklus sebelumnya disimpan di berkas
	// riwayat, jadi angkanya justru jadi rata-rata satu menit penuh — bukan
	// potret sesaat hasil tidur beberapa ratus milidetik.
	cur := collector.ReadCPUTimes()
	if h.CPUMark != nil {
		prev := collector.CPUTimes{Total: h.CPUMark.Total, Idle: h.CPUMark.Idle}
		if p := collector.CPUPercent(prev, cur); p >= 0 {
			snap.Host.CPUPercent = p
		}
	}
	if cur.Valid() {
		h.CPUMark = &history.CPUMark{At: time.Now(), Total: cur.Total, Idle: cur.Idle}
	}

	h.Append(history.PointFrom(*snap))
	if err := history.Save(path, h); err != nil {
		log.Printf("agent: menulis riwayat %s: %v", path, err)
	}
}

// sendTest dipakai tombol "kirim tes" di halaman kelola (lewat helper dan
// systemd-run, dengan user dan agent.env yang sama dengan timernya). Ia tidak
// menyentuh riwayat maupun ingatan "sudah dikabari": tes yang diam-diam
// menandai temuan sebagai sudah dikabari akan menelan notifikasi sungguhan.
func sendTest(ch notify.Config, cfg config.Config) int {
	if !ch.Enabled() {
		fmt.Println("tidak ada kanal notifikasi yang terisi — isi token & chat id Telegram, atau topik ntfy")
		return 1
	}
	host, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := ch.Send(ctx, notify.Message{
		Title:    "issboard: pesan uji dari " + host,
		Body:     "Kalau pesan ini sampai, kanal notifikasi bekerja. Dikirim dari halaman kelola; tidak ada temuan yang berubah.",
		Resolved: true,
	})
	if err != nil {
		fmt.Printf("gagal: %v\n", err)
		return 1
	}
	fmt.Printf("terkirim lewat %s (level minimum %s)\n", strings.Join(ch.Channels(), ", "), cfg.NotifyMinLevel)
	return 0
}
