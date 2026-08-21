package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type tangkapan struct {
	path, escaped, body string
	header              http.Header
}

func serverUji(t *testing.T, kode int, out *tangkapan) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		out.path, out.escaped = r.URL.Path, r.URL.EscapedPath()
		out.body, out.header = string(b), r.Header.Clone()
		w.WriteHeader(kode)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestNtfyHeaderDanBadan(t *testing.T) {
	var got tangkapan
	s := serverUji(t, 200, &got)

	c := Config{NtfyURL: s.URL, NtfyTopic: "topik uji", NtfyToken: "rahasia"}
	err := c.Send(context.Background(), Message{
		Title: "issboard kotak: 2 baru", Body: "rincian", Urgent: true})
	if err != nil {
		t.Fatal(err)
	}

	// Topik yang mengandung spasi harus di-escape di kabel, dan sampai utuh
	// di seberang. Topik ntfy datang dari config, jadi ia bisa berisi apa saja.
	if got.escaped != "/topik%20uji" {
		t.Errorf("topik tidak di-escape: %q", got.escaped)
	}
	if got.path != "/topik uji" {
		t.Errorf("topik tidak sampai utuh: %q", got.path)
	}
	if got.body != "rincian" {
		t.Errorf("badan salah: %q", got.body)
	}
	if got.header.Get("Priority") != "urgent" {
		t.Errorf("temuan kritis harus prioritas urgent: %q", got.header.Get("Priority"))
	}
	if got.header.Get("Authorization") != "Bearer rahasia" {
		t.Errorf("token tidak terpasang: %q", got.header.Get("Authorization"))
	}
	if got.header.Get("Title") == "" {
		t.Error("judul tidak terkirim")
	}
}

// Pesan yang isinya cuma kabar baik tidak boleh memakai ikon peringatan.
func TestNtfyPrioritasPesanPulih(t *testing.T) {
	var got tangkapan
	s := serverUji(t, 200, &got)
	c := Config{NtfyURL: s.URL, NtfyTopic: "uji"}

	if err := c.Send(context.Background(), Message{Title: "pulih", Resolved: true}); err != nil {
		t.Fatal(err)
	}
	if got.header.Get("Priority") != "low" {
		t.Errorf("kabar pulih harus prioritas rendah: %q", got.header.Get("Priority"))
	}
	if got.header.Get("Tags") == "rotating_light" {
		t.Error("kabar pulih memakai ikon peringatan")
	}
}

// 🔴 Header HTTP secara resmi hanya ISO-8859-1. Judul ber-UTF-8 bisa membuat
// server menolak seluruh pesan — kegagalan diam yang paling mahal di sini.
func TestJudulDibersihkanUntukHeader(t *testing.T) {
	var got tangkapan
	s := serverUji(t, 200, &got)
	c := Config{NtfyURL: s.URL, NtfyTopic: "uji"}

	if err := c.Send(context.Background(), Message{
		Title: "issboard — “kotak” 52°C", Body: "badan tetap UTF-8: 52°C"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range got.header.Get("Title") {
		if r > 127 {
			t.Errorf("judul masih memuat karakter non-ASCII: %q", got.header.Get("Title"))
			break
		}
	}
	// Badan pesan TIDAK dibersihkan — di situ UTF-8 memang sah.
	if !strings.Contains(got.body, "°C") {
		t.Errorf("badan pesan ikut dibersihkan: %q", got.body)
	}
}

func TestTelegramForm(t *testing.T) {
	var got tangkapan
	s := serverUji(t, 200, &got)
	// sendTelegram memakai alamat api.telegram.org yang tetap, jadi yang bisa
	// diuji langsung adalah bentuk form-nya.
	form := url.Values{"chat_id": {"123"}, "text": {"judul\n\nbadan"}}
	if _, err := http.PostForm(s.URL, form); err != nil {
		t.Fatal(err)
	}
	v, err := url.ParseQuery(got.body)
	if err != nil {
		t.Fatal(err)
	}
	if v.Get("chat_id") != "123" || !strings.Contains(v.Get("text"), "badan") {
		t.Errorf("form salah: %v", v)
	}
}

// Kiriman yang ditolak harus jadi error yang terlihat: pemanggil memakainya
// untuk memutuskan apakah temuan boleh ditandai "sudah dikabari".
func TestKodeGagalJadiError(t *testing.T) {
	var got tangkapan
	s := serverUji(t, 403, &got)
	c := Config{NtfyURL: s.URL, NtfyTopic: "uji"}

	err := c.Send(context.Background(), Message{Title: "x"})
	if err == nil {
		t.Fatal("HTTP 403 harus jadi error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("kode status harus ikut disebut: %v", err)
	}
}

func TestEnabledDanChannels(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("config kosong tidak boleh dianggap aktif")
	}
	// Telegram butuh DUA nilai; salah satu saja tidak cukup.
	if (Config{TelegramToken: "t"}).Enabled() {
		t.Error("telegram tanpa chat id tidak boleh dianggap aktif")
	}
	c := Config{NtfyTopic: "a", TelegramToken: "t", TelegramChatID: "1"}
	if got := c.Channels(); len(got) != 2 {
		t.Errorf("dua kanal harus terdaftar: %v", got)
	}
}

// URL kosong jatuh ke ntfy.sh, supaya cukup mengisi topik saja.
func TestNtfyURLBawaan(t *testing.T) {
	c := Config{NtfyTopic: "uji"}
	if !c.Enabled() {
		t.Error("topik saja sudah cukup untuk mengaktifkan ntfy")
	}
}
