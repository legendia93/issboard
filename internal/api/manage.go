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

// ---------- dataset & snapshot (fase 8 bagian 2) ----------

// knownDataset: daftar-putih dari zfs sendiri, dan tunduk pada saringan
// `pools:` — dataset di pool yang tidak ditampilkan juga tidak bisa disentuh.
func (s *Server) knownDataset(r *http.Request, name string) error {
	var names []string
	if s.cfg.Demo {
		for _, d := range collector.DemoSnapshot().Datasets {
			names = append(names, d.Name)
		}
	} else {
		var err error
		if names, err = ops.DatasetNames(r.Context()); err != nil {
			return err
		}
	}
	found := false
	for _, n := range names {
		found = found || n == name
	}
	if !found {
		return bad("dataset %q tidak ada", name)
	}
	if len(s.cfg.Pools) > 0 {
		pool, _, _ := strings.Cut(name, "/")
		ok := false
		for _, p := range s.cfg.Pools {
			ok = ok || p == pool
		}
		if !ok {
			return bad("pool %q tidak ditampilkan (pools: di config)", pool)
		}
	}
	return nil
}

type datasetResponse struct {
	Name      string         `json:"name"`
	Props     []ops.Prop     `json:"props"`
	Specs     []ops.PropSpec `json:"specs"`
	Snapshots []ops.Snapshot `json:"snapshots"`
	Errors    []string       `json:"errors,omitempty"`
}

// handleDataset (GET, baca saja): properti yang bisa diubah dan snapshot satu
// dataset. Dipisah dari /status karena mahal di dataset dengan ribuan
// snapshot, dan hanya dibutuhkan saat panel kelola dibuka.
func (s *Server) handleDataset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if err := s.knownDataset(r, name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	resp := datasetResponse{Name: name, Specs: ops.PropSpecs()}
	if s.cfg.Demo {
		resp.Props, resp.Snapshots = ops.DemoProps(), ops.DemoSnapshots(name)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	var err error
	if resp.Props, err = ops.GetProps(r.Context(), name); err != nil {
		resp.Errors = append(resp.Errors, err.Error())
	}
	if resp.Snapshots, err = ops.ListSnapshots(r.Context(), name); err != nil {
		resp.Errors = append(resp.Errors, err.Error())
	}
	writeJSON(w, http.StatusOK, resp)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return bad("permintaan tidak terbaca")
	}
	return nil
}

func (s *Server) actSnapCreate(r *http.Request, actor string) (string, error) {
	var b struct {
		Dataset   string `json:"dataset"`
		Tag       string `json:"tag"`
		Recursive bool   `json:"recursive"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	if err := s.knownDataset(r, b.Dataset); err != nil {
		return "", err
	}
	if err := ops.ValidTag(b.Tag); err != nil {
		return "", bad("%v", err)
	}
	if s.cfg.Demo {
		return demoOut(ops.SnapCreate, b.Dataset), nil
	}
	return s.helper(r, ops.Request{Action: ops.SnapCreate, Target: b.Dataset, Name: b.Tag, Recursive: b.Recursive, Actor: actor})
}

func (s *Server) actSnapDestroy(r *http.Request, actor string) (string, error) {
	var b struct {
		Snapshot string `json:"snapshot"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	ds, _, ok := strings.Cut(b.Snapshot, "@")
	if !ok {
		return "", bad("%q bukan nama snapshot", b.Snapshot)
	}
	if err := s.knownDataset(r, ds); err != nil {
		return "", err
	}
	if s.cfg.Demo {
		return demoOut(ops.SnapDestroy, b.Snapshot), nil
	}
	snaps, err := ops.ListSnapshots(r.Context(), ds)
	if err != nil {
		return "", err
	}
	found := false
	for _, sn := range snaps {
		found = found || sn.Name == b.Snapshot
	}
	if !found {
		return "", bad("snapshot %q tidak ada", b.Snapshot)
	}
	return s.helper(r, ops.Request{Action: ops.SnapDestroy, Target: b.Snapshot, Actor: actor})
}

func (s *Server) actDSCreate(r *http.Request, actor string) (string, error) {
	var b struct {
		Parent string            `json:"parent"`
		Name   string            `json:"name"`
		Props  map[string]string `json:"props"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	if err := s.knownDataset(r, b.Parent); err != nil {
		return "", err
	}
	if err := ops.ValidName(b.Name); err != nil {
		return "", bad("%v", err)
	}
	for k, v := range b.Props {
		if v == "" || v == "inherit" {
			continue
		}
		if err := ops.ValidateProp(k, v); err != nil {
			return "", bad("%v", err)
		}
	}
	if s.cfg.Demo {
		return demoOut(ops.DSCreate, b.Parent+"/"+b.Name), nil
	}
	return s.helper(r, ops.Request{Action: ops.DSCreate, Target: b.Parent, Name: b.Name, Props: b.Props, Actor: actor})
}

func (s *Server) actDSSet(r *http.Request, actor string) (string, error) {
	var b struct {
		Dataset string `json:"dataset"`
		Prop    string `json:"prop"`
		Value   string `json:"value"`
	}
	if err := decode(r, &b); err != nil {
		return "", err
	}
	if err := s.knownDataset(r, b.Dataset); err != nil {
		return "", err
	}
	if err := ops.ValidateProp(b.Prop, b.Value); err != nil {
		return "", bad("%v", err)
	}
	if s.cfg.Demo {
		return demoOut(ops.DSSet, b.Dataset+" "+b.Prop+"="+b.Value), nil
	}
	return s.helper(r, ops.Request{Action: ops.DSSet, Target: b.Dataset, Prop: b.Prop, PropValue: b.Value, Actor: actor})
}

func (s *Server) actSanoid(r *http.Request, actor string) (string, error) {
	if s.cfg.Demo {
		return demoOut(ops.SanoidRun, ""), nil
	}
	return s.helper(r, ops.Request{Action: ops.SanoidRun, Actor: actor})
}
