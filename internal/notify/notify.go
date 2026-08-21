// Package notify mengirim pesan ke ntfy dan/atau Telegram.
//
// Keduanya cuma HTTP POST, jadi janji nol dependensi di luar pustaka standar
// tetap utuh (docs/design.md §3.1) — tidak ada SDK yang perlu ditarik.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config: kanal mana pun boleh kosong. Dua-duanya aktif juga boleh, dan itu
// bukan kasus aneh — ntfy untuk yang lewat di HP, Telegram supaya ada
// jejaknya di percakapan yang bisa dicari lagi nanti.
type Config struct {
	NtfyURL   string // default https://ntfy.sh
	NtfyTopic string
	NtfyToken string

	TelegramToken  string
	TelegramChatID string
}

// Enabled menjawab apakah ada kanal yang benar-benar bisa dipakai.
func (c Config) Enabled() bool { return c.ntfyOK() || c.telegramOK() }

func (c Config) ntfyOK() bool { return c.NtfyTopic != "" }
func (c Config) telegramOK() bool {
	return c.TelegramToken != "" && c.TelegramChatID != ""
}

// Channels menyebut kanal aktif, untuk log agent.
func (c Config) Channels() []string {
	var s []string
	if c.ntfyOK() {
		s = append(s, "ntfy")
	}
	if c.telegramOK() {
		s = append(s, "telegram")
	}
	return s
}

type Message struct {
	Title string
	Body  string
	// Urgent menaikkan prioritas notifikasi. Dipakai hanya untuk temuan
	// kritis: kalau semua pesan penting, tidak ada yang penting.
	Urgent bool
	// Resolved menandai pesan yang isinya hanya kabar baik. Ikon peringatan
	// pada kabar "sudah pulih" membuat orang meraih HP dengan cemas untuk
	// sesuatu yang justru sudah beres.
	Resolved bool
}

// Send mengirim ke SEMUA kanal yang aktif dan menggabungkan errornya.
//
// Kegagalan satu kanal tidak boleh menyembunyikan keberhasilan kanal lain,
// tapi juga tidak boleh ditelan diam-diam: pemanggil memakai error ini untuk
// memutuskan apakah temuan sudah boleh ditandai "sudah dikabari".
func (c Config) Send(ctx context.Context, m Message) error {
	cl := &http.Client{Timeout: 10 * time.Second}
	var errs []error
	if c.ntfyOK() {
		if err := c.sendNtfy(ctx, cl, m); err != nil {
			errs = append(errs, fmt.Errorf("ntfy: %w", err))
		}
	}
	if c.telegramOK() {
		if err := c.sendTelegram(ctx, cl, m); err != nil {
			errs = append(errs, fmt.Errorf("telegram: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (c Config) sendNtfy(ctx context.Context, cl *http.Client, m Message) error {
	base := c.NtfyURL
	if base == "" {
		base = "https://ntfy.sh"
	}
	endpoint := strings.TrimSuffix(base, "/") + "/" + url.PathEscape(c.NtfyTopic)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	// Header HTTP secara resmi hanya ISO-8859-1; judul dibersihkan supaya
	// server tidak menolak atau menampilkan sampah. Teks lengkapnya tetap
	// utuh di badan pesan, yang memang UTF-8.
	req.Header.Set("Title", asciiOnly(m.Title))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	switch {
	case m.Urgent:
		req.Header.Set("Priority", "urgent")
		req.Header.Set("Tags", "rotating_light")
	case m.Resolved:
		req.Header.Set("Priority", "low")
		req.Header.Set("Tags", "white_check_mark")
	default:
		req.Header.Set("Tags", "warning")
	}
	if c.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.NtfyToken)
	}
	return do(cl, req)
}

func (c Config) sendTelegram(ctx context.Context, cl *http.Client, m Message) error {
	endpoint := "https://api.telegram.org/bot" + c.TelegramToken + "/sendMessage"
	// Sengaja tanpa parse_mode: teks temuan memuat nama device dan status yang
	// mudah mengandung karakter Markdown, dan pesan yang gagal terkirim karena
	// escaping adalah cara terburuk kehilangan alert.
	form := url.Values{
		"chat_id":                  {c.TelegramChatID},
		"text":                     {m.Title + "\n\n" + m.Body},
		"disable_web_page_preview": {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(cl, req)
}

func do(cl *http.Client, req *http.Request) error {
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Sedikit badan responsnya ikut dibawa: "401" sendirian tidak cukup
		// untuk tahu apakah tokennya salah atau topiknya yang dilindungi.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

// asciiOnly menjaga judul tetap aman ditaruh di header HTTP.
func asciiOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '—' || r == '–':
			b.WriteByte('-')
		case r == '\u201c' || r == '\u201d':
			b.WriteByte('"')
		case r < 32:
			b.WriteByte(' ')
		case r < 127:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
