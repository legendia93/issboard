package alert

import (
	"fmt"
	"strings"
	"time"

	"github.com/legendia93/issboard/internal/health"
)

// maxBody membatasi panjang pesan.
//
// ntfy dan Telegram sama-sama menolak badan pesan yang terlalu panjang, dan
// host yang baru dipasangi issboard bisa punya belasan temuan sekaligus.
// Alert yang gagal terkirim justru karena isinya terlalu banyak adalah
// kegagalan diam — persis jenis yang proyek ini ada untuk mencegahnya.
const maxBody = 3500

// Compose menyusun SATU pesan untuk seluruh siklus.
//
// Satu pesan, bukan satu per temuan: mengirim belasan notifikasi sekaligus
// membuat orang mematikan kanalnya, dan kanal yang dimatikan sama saja dengan
// tidak punya alert.
func Compose(d Decision, host string, now time.Time) (title, body string) {
	var b strings.Builder

	newHead := "BARU"
	if d.FirstRun {
		// Pemasangan pertama: temuannya boleh sudah berbulan-bulan umurnya,
		// jadi jangan menyebutnya "baru saja terjadi".
		newHead = "KONDISI SAAT INI"
	}

	var skipped int
	for _, s := range []struct {
		head  string
		items []Item
	}{
		{newHead, d.New},
		{"MEMBURUK", d.Worse},
		{"MASIH BERLANGSUNG", d.Reminder},
		{"PULIH", d.Recovered},
	} {
		skipped += section(&b, s.head, s.items, now, s.head == "PULIH")
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "\n(%d temuan lagi tidak dimuat — buka dashboard untuk daftar penuh)\n", skipped)
	}

	return composeTitle(d, host), strings.TrimRight(b.String(), "\n")
}

func composeTitle(d Decision, host string) string {
	if host == "" {
		host = "host"
	}
	if d.FirstRun && len(d.New) > 0 {
		return fmt.Sprintf("issboard %s: ringkasan awal, %d temuan", host, len(d.New))
	}

	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(len(d.New), "baru")
	add(len(d.Worse), "memburuk")
	add(len(d.Reminder), "masih berlangsung")
	add(len(d.Recovered), "pulih")
	if len(parts) == 0 {
		return "issboard " + host
	}
	return "issboard " + host + ": " + strings.Join(parts, ", ")
}

// section menulis satu bagian dan mengembalikan jumlah temuan yang tidak muat.
// Judul bagian ditulis belakangan supaya bagian yang seluruhnya tidak muat
// tidak meninggalkan judul kosong.
func section(b *strings.Builder, head string, items []Item, now time.Time, recovered bool) (skipped int) {
	written := false
	for i, it := range items {
		block := itemBlock(it, now, recovered)
		lead := ""
		if !written {
			lead = head + "\n"
			if b.Len() > 0 {
				lead = "\n" + lead
			}
		}
		if b.Len()+len(lead)+len(block) > maxBody {
			return len(items) - i
		}
		b.WriteString(lead)
		b.WriteString(block)
		written = true
	}
	return 0
}

func itemBlock(it Item, now time.Time, recovered bool) string {
	var b strings.Builder

	// Temuan yang sudah pulih tetap membawa tingkat LAMANYA, dan menuliskannya
	// apa adanya menghasilkan "[KRITIS] Pool hampir penuh" di bawah judul
	// PULIH — persis kebalikan dari kabar yang sedang disampaikan. Detailnya
	// juga diberi awalan "sebelumnya", karena "terpakai 92%" itu keadaan yang
	// sudah lewat, bukan keadaan sekarang.
	if recovered {
		fmt.Fprintf(&b, "[PULIH] %s\n", it.Title)
		if it.Detail != "" {
			fmt.Fprintf(&b, "  sebelumnya: %s\n", it.Detail)
		}
		return b.String()
	}

	fmt.Fprintf(&b, "%s %s\n", tag(it.Level), it.Title)
	if it.Detail != "" {
		fmt.Fprintf(&b, "  %s\n", it.Detail)
	}
	// Umur hanya disebut kalau memang menambah sesuatu. Temuan yang muncul
	// siklus ini sudah jelas baru dari judul bagiannya, dan "sejak kurang dari
	// sejam lalu" di tiap baris cuma menggandakan panjang pesan.
	if !it.Since.IsZero() && now.Sub(it.Since) >= time.Hour {
		fmt.Fprintf(&b, "  (sejak %s)\n", sejak(it.Since, now))
	}
	return b.String()
}

// tag memakai kata, bukan simbol atau warna: pesan ini dibaca di notifikasi
// ponsel yang tidak punya keduanya.
func tag(l health.Level) string {
	switch l {
	case health.Crit:
		return "[KRITIS]"
	case health.Warn:
		return "[PERHATIAN]"
	default:
		return "[OK]"
	}
}

// sejak menyebut umur secara kasar. Menit yang persis tidak menambah apa pun
// untuk kondisi yang memang bertahan lama.
func sejak(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 24*time.Hour:
		return fmt.Sprintf("%d jam lalu", int(d.Hours()))
	default:
		return fmt.Sprintf("%d hari lalu", int(d.Hours()/24))
	}
}
