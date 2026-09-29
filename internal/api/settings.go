package api

// Pengaturan notifikasi dan aplikasi dari halaman kelola.
//
// Dua berkas, dua aturan:
//   - /etc/issboard/agent.env (0600 root): token & chat id. Ditulis helper,
//     dan nilai rahasianya TIDAK PERNAH kembali ke browser — hanya petunjuk
//     empat karakter terakhir.
//   - /etc/issboard/settings.conf: timpaan pengaturan aplikasi, supaya
//     /etc/issboard.yaml (conffile paket) tidak pernah disunting mesin.

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"

	"github.com/legendia93/issboard/internal/ops"
)

// authed membungkus GET yang hanya untuk operator yang sudah masuk. Tidak
// butuh CSRF — ia tidak mengubah apa pun — tapi isinya (keadaan kredensial)
// bukan untuk siapa pun yang kebetulan bisa membuka dashboard baca.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := s.session(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "perlu masuk dulu"})
			return
		}
		h(w, r)
	}
}

type appSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Source: berkas asal nilainya, atau kosong = bawaan program.
	Source string `json:"source,omitempty"`
}

type settingsResponse struct {
	App    []appSetting               `json:"app"`
	Notify map[string]ops.NotifyValue `json:"notify,omitempty"`
	// NotifyError: helper tidak bisa membaca agent.env. Halaman tetap bisa
	// menyimpan pengaturan aplikasi walau bagian ini gagal.
	NotifyError string   `json:"notify_error,omitempty"`
	PoolNames   []string `json:"pool_names"`
	Demo        bool     `json:"demo,omitempty"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	c := s.conf()
	vals := map[string]string{
		"notify_min_level": c.NotifyMinLevel,
		"alert_repeat":     c.AlertRepeat.String(),
		"idle_timeout":     c.IdleTimeout.String(),
		"snapshot_exempt":  strings.Join(c.SnapExempt, ", "),
		"pools":            strings.Join(c.Pools, ", "),
	}
	resp := settingsResponse{Demo: c.Demo}
	for _, k := range ops.SettingsKeys() {
		resp.App = append(resp.App, appSetting{Key: k, Value: vals[k], Source: c.Sources[k]})
	}

	if c.Demo {
		resp.Notify = ops.DemoNotify()
		resp.PoolNames = []string{"pool-cepat", "pool-arsip", "pool-uji"}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Semua pool, bukan hanya yang lolos saringan: justru daftar inilah yang
	// dipakai untuk mengubah saringannya.
	if out, err := exec.CommandContext(r.Context(), "zpool", "list", "-H", "-o", "name").Output(); err == nil {
		resp.PoolNames = strings.Fields(string(out))
	}
	res, err := ops.Call(r.Context(), c.HelperSocket, ops.Request{Action: ops.NotifyStatus})
	switch {
	case err != nil:
		resp.NotifyError = err.Error()
	case !res.OK:
		resp.NotifyError = res.Error
	default:
		if err := json.Unmarshal([]byte(res.Output), &resp.Notify); err != nil {
			resp.NotifyError = "jawaban helper tidak terbaca: " + err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) actNotifySet(r *http.Request, actor string) (string, error) {
	var b struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	if len(b.Values) == 0 {
		return "", bad("tidak ada yang diubah")
	}
	for k, v := range b.Values {
		if err := ops.ValidateNotify(k, strings.TrimSpace(v)); err != nil {
			return "", bad("%v", err)
		}
		b.Values[k] = strings.TrimSpace(v)
	}
	if s.conf().Demo {
		return demoOut(ops.NotifySet, strings.Join(keysOf(b.Values), ",")), nil
	}
	return s.helper(r, ops.Request{Action: ops.NotifySet, Props: b.Values, Actor: actor})
}

func (s *Server) actNotifyTest(r *http.Request, actor string) (string, error) {
	if s.conf().Demo {
		return demoOut(ops.NotifyTest, ""), nil
	}
	return s.helper(r, ops.Request{Action: ops.NotifyTest, Actor: actor})
}

func (s *Server) actSettingsSet(r *http.Request, actor string) (string, error) {
	var b struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	if len(b.Values) == 0 {
		return "", bad("tidak ada yang diubah")
	}
	for k, v := range b.Values {
		if err := ops.ValidateSetting(k, strings.TrimSpace(v)); err != nil {
			return "", bad("%v", err)
		}
		b.Values[k] = strings.TrimSpace(v)
	}
	if s.conf().Demo {
		return demoOut(ops.SettingsSet, strings.Join(keysOf(b.Values), ",")), nil
	}
	out, err := s.helper(r, ops.Request{Action: ops.SettingsSet, Props: b.Values, Actor: actor})
	if err != nil {
		return out, err
	}
	// Dashboard ini memakai nilai barunya SEKARANG. Agent membaca berkas yang
	// sama di putaran berikutnya (≤1 menit).
	if err := s.reload(); err != nil {
		return out + "; ⚠ tersimpan, tapi gagal dimuat ulang: " + err.Error(), nil
	}
	return out, nil
}

func keysOf(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}
