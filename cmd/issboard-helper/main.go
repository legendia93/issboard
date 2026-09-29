// issboard-helper — satu-satunya bagian issboard yang berjalan sebagai root.
//
// Dinyalakan systemd per koneksi (issboard-helper.socket, Accept=yes):
// stdin dan stdout adalah koneksinya. Membaca SATU permintaan JSON,
// mengerjakannya, menulis jawabannya, lalu keluar. Tidak ada daemon.
//
// Kemampuannya adalah daftar tertutup di internal/ops, dan setiap target
// dicocokkan dengan daftar dari sistem sebelum apa pun dijalankan. Yang boleh
// menyambung ke socket-nya hanya root dan grup issboard (0660).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"sort"
	"time"

	"github.com/legendia93/issboard/internal/ops"
)

func main() {
	// stderr masuk journald (StandardError=journal). stdout adalah socket —
	// JANGAN pernah menulis log ke sana.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	// Permintaan sah tidak pernah lebih dari beberapa ratus byte.
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 4096)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		log.Printf("issboard-helper: tidak ada permintaan: %v", err)
		os.Exit(1)
	}

	var req ops.Request
	var resp ops.Response
	if err := json.Unmarshal(line, &req); err != nil {
		resp = ops.Response{Error: "permintaan tidak terbaca: " + err.Error()}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		// Dicatat SEBELUM dijalankan, bukan cuma sesudah: kalau prosesnya
		// terbunuh di tengah jalan, jejak bahwa aksinya diminta tetap ada.
		log.Printf("audit: mulai aksi=%s target=%q name=%q prop=%q=%q props=%v value=%d persist=%t rekursif=%t oleh=%q",
			req.Action, req.Target, req.Name, req.Prop, req.PropValue, auditProps(req),
			req.Value, req.Persist, req.Recursive, req.Actor)
		resp = ops.NewExecutor().Handle(ctx, req)
		cancel()
		hasil := "ok"
		if !resp.OK {
			hasil = "gagal: " + resp.Error
		}
		log.Printf("audit: selesai aksi=%s target=%q hasil=%s", req.Action, req.Target, hasil)
	}

	b, _ := json.Marshal(resp)
	_, _ = os.Stdout.Write(append(b, '\n'))
}

// auditProps: nilai pengaturan notifikasi adalah token. Journal boleh dibaca
// grup adm, dan token yang tercatat di sana sama dengan token yang bocor —
// jadi untuk notify.set yang dicatat hanya NAMA kunci dan apakah ia dihapus.
func auditProps(req ops.Request) any {
	if req.Action != ops.NotifySet {
		return req.Props
	}
	var ks []string
	for k, v := range req.Props {
		if v == "" {
			k += "(hapus)"
		}
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
