package api

// Aksi dan info pengelolaan (fase 8).
//
// Dua aturan dari design.md §8 yang ditagih di berkas ini:
//  1. Tidak ada sudo. Aksi root lewat issboard-helper (internal/ops).
//  2. Daftar-putih dari sistem, dicocokkan persis. Di sini DAN sekali lagi di
//     helper, yang tidak mempercayai issboard.
//
// Semua handler aksi didaftarkan lewat s.mutate (session.go).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/legendia93/issboard/internal/collector"
	"github.com/legendia93/issboard/internal/ops"
)

// ---------- aksi ----------

func (s *Server) actContainer(r *http.Request, _ string) (string, error) {
	name, act := r.PathValue("name"), r.PathValue("action")
	if !collector.ValidContainerAction(act) {
		return "", bad("aksi container tidak dikenal: %q", act)
	}
	if s.cfg.Demo {
		return demoOut(act, name), nil
	}
	c, err := collector.FindContainer(r.Context(), s.cfg.DockerSocket, name)
	if err != nil {
		return "", bad("%v", err)
	}
	if act == "remove" {
		switch strings.ToLower(c.State) {
		case "running", "restarting", "paused":
			return "", bad("container %q masih %s — hentikan dulu sebelum dihapus", name, c.State)
		}
	}
	return collector.ContainerAction(r.Context(), s.cfg.DockerSocket, c.ID, act)
}

var scrubOps = map[string]string{"start": ops.ScrubStart, "stop": ops.ScrubStop, "pause": ops.ScrubPause}

func (s *Server) actScrub(r *http.Request, actor string) (string, error) {
	name := r.PathValue("name")
	action, ok := scrubOps[r.PathValue("op")]
	if !ok {
		return "", bad("operasi scrub tidak dikenal: %q", r.PathValue("op"))
	}
	if s.cfg.Demo {
		return demoOut(action, name), nil
	}
	// Daftar yang sama dengan yang tampil di dashboard — termasuk saringan
	// `pools:` di config. Pool yang tidak ditampilkan juga tidak bisa disentuh.
	pools, err := collector.CollectPools(r.Context(), s.cfg.Pools)
	if err != nil {
		return "", err
	}
	found := false
	for _, p := range pools {
		found = found || p.Name == name
	}
	if !found {
		return "", bad("pool %q tidak ada", name)
	}
	return s.helper(r, ops.Request{Action: action, Target: name, Actor: actor})
}

var smartOps = map[string]string{
	"short": ops.SmartShort, "long": ops.SmartLong, "abort": ops.SmartAbort, "refresh": ops.SmartRefresh,
}

func (s *Server) actSmart(r *http.Request, actor string) (string, error) {
	action, ok := smartOps[r.PathValue("op")]
	if !ok {
		return "", bad("operasi SMART tidak dikenal: %q", r.PathValue("op"))
	}
	var body struct {
		Device string `json:"device"`
	}
	if action != ops.SmartRefresh {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", bad("permintaan tidak terbaca")
		}
	}
	if s.cfg.Demo {
		return demoOut(action, body.Device), nil
	}
	if action != ops.SmartRefresh {
		rep, _ := collector.ReadSmartCache(s.cfg.SmartCache)
		found := false
		for _, d := range rep.Disks {
			found = found || d.Device == body.Device
		}
		if !found {
			return "", bad("perangkat %q tidak ada di cache SMART", body.Device)
		}
	}
	return s.helper(r, ops.Request{Action: action, Target: body.Device, Actor: actor})
}

func (s *Server) actARC(r *http.Request, actor string) (string, error) {
	var body struct {
		MaxBytes int64 `json:"max_bytes"`
		Persist  bool  `json:"persist"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", bad("permintaan tidak terbaca")
	}
	a := ops.ReadARCInfo()
	if s.cfg.Demo {
		a = ops.DemoARC()
	}
	if !a.Present {
		return "", bad("ZFS tidak termuat di host ini")
	}
	if err := ops.ValidateARC(body.MaxBytes, a.MemTotal, a.CMin); err != nil {
		return "", bad("%v", err)
	}
	if s.cfg.Demo {
		return demoOut(ops.ARCSet, fmt.Sprint(body.MaxBytes)), nil
	}
	return s.helper(r, ops.Request{Action: ops.ARCSet, Value: body.MaxBytes, Persist: body.Persist, Actor: actor})
}

func (s *Server) helper(r *http.Request, req ops.Request) (string, error) {
	resp, err := ops.Call(r.Context(), s.cfg.HelperSocket, req)
	if errors.Is(err, ops.ErrUnavailable) {
		return "", unavailable{err}
	}
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return resp.Output, errors.New(resp.Error)
	}
	return resp.Output, nil
}

func demoOut(action, target string) string {
	return fmt.Sprintf("(demo) %s %s — tidak ada yang benar-benar dijalankan", action, target)
}

// ---------- info pengelolaan (baca saja) ----------

type manageResponse struct {
	ARC      ops.ARCInfo  `json:"arc"`
	Schedule ops.Schedule `json:"schedule"`
	// Helper: socket issboard-helper ada. False berarti tombol aksi root
	// pasti gagal, dan UI bisa mengatakannya sebelum ada yang menekan.
	Helper bool `json:"helper"`
	Demo   bool `json:"demo,omitempty"`
}

func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo {
		writeJSON(w, http.StatusOK, manageResponse{ARC: ops.DemoARC(), Schedule: ops.DemoSchedule(), Helper: true, Demo: true})
		return
	}
	st, err := os.Stat(s.cfg.HelperSocket)
	writeJSON(w, http.StatusOK, manageResponse{
		ARC:      ops.ReadARCInfo(),
		Schedule: ops.ReadSchedule(r.Context()),
		Helper:   err == nil && st.Mode()&os.ModeSocket != 0,
	})
}
