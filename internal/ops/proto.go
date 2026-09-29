// Package ops adalah jalur aksi yang butuh root: scrub, SMART self-test, dan
// batas ARC.
//
// 🔴 issboard sendiri TIDAK PERNAH jadi root dan tidak memakai sudo.
// issboard.service berjalan dengan NoNewPrivileges=yes, yang membuat sudo
// mustahil — dan itu disengaja. Aksi root dikerjakan issboard-helper: proses
// kecil milik root yang dinyalakan systemd per koneksi lewat socket unix
// (issboard-helper.socket, Accept=yes), mengerjakan SATU permintaan, lalu
// keluar. Sama seperti issboard: tidak ada yang menyala saat tidak dipakai.
//
// Helper tidak mempercayai issboard. Setiap target — nama pool, perangkat
// disk — dicocokkan lagi dengan daftar yang diambil helper sendiri dari
// sistem, persis (design.md §8: daftar-putih, bukan regex). issboard yang
// dibobol hanya bisa meminta aksi yang memang ada di daftar di bawah ini,
// terhadap target yang memang ada.
package ops

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Aksi yang dikenal helper. Tidak ada aksi "jalankan perintah ini": daftar ini
// adalah seluruh kemampuan root yang bisa dijangkau dari dashboard.
const (
	ScrubStart   = "scrub.start"
	ScrubStop    = "scrub.stop"
	ScrubPause   = "scrub.pause"
	SmartShort   = "smart.short"
	SmartLong    = "smart.long"
	SmartAbort   = "smart.abort"
	SmartRefresh = "smart.refresh"
	ARCSet       = "arc.set"
)

type Request struct {
	Action string `json:"action"`
	// Target: nama pool atau perangkat disk, tergantung aksinya.
	Target string `json:"target,omitempty"`
	// Value: batas ARC dalam byte untuk arc.set. 0 = kembali ke bawaan ZFS.
	Value int64 `json:"value,omitempty"`
	// Persist: arc.set juga menulis /etc/modprobe.d supaya selamat reboot.
	Persist bool `json:"persist,omitempty"`
	// Actor hanya untuk log audit helper. Helper tidak memakainya untuk
	// memutuskan apa pun — yang membatasi siapa boleh bicara dengannya adalah
	// izin socket-nya (root:issboard 0660).
	Actor string `json:"actor,omitempty"`
}

type Response struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ErrUnavailable: socket helper tidak ada — belum dipasang, atau unit
// socket-nya tidak aktif. Dibedakan supaya UI bisa bilang "helper belum
// terpasang" alih-alih error koneksi yang membingungkan.
var ErrUnavailable = errors.New("issboard-helper tidak terjangkau")

// Call mengirim satu permintaan ke helper dan menunggu jawabannya.
//
// Batas waktunya longgar karena `smartctl -t` pada disk yang tidur harus
// menunggu disk itu berputar dulu.
func Call(ctx context.Context, socket string, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, fmt.Errorf("%w (%s): %v", ErrUnavailable, socket, err)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}

	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		return Response{}, fmt.Errorf("kirim ke helper: %w", err)
	}

	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, fmt.Errorf("jawaban helper: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("jawaban helper tidak terbaca: %w", err)
	}
	return resp, nil
}
