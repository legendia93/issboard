package history

import (
	"math"
	"time"
)

// DemoFile membuat riwayat palsu untuk mode demo.
//
// Alasannya sama dengan mode demo di fase 1, dan sama pentingnya: sparkline
// tidak bisa digarap kalau harus menunggu agent mengumpulkan data 24 jam
// lebih dulu. Efek sampingnya juga sama — screenshot jadi aman, karena berkas
// riwayat sungguhan memuat nama pool dan nama device milik mesin nyata.
//
// Angkanya sengaja berayun mengikuti waktu, bukan acak, supaya tampilannya
// bisa diulang saat merekam layar.
func DemoFile() File {
	now := time.Now()
	f := File{
		WrittenAt: now,
		Fine:      Series{StepSeconds: int(FineStep.Seconds())},
		Coarse:    Series{StepSeconds: int(CoarseStep.Seconds())},
	}

	const gib = float64(1 << 30)

	// Beban mesin sungguhan punya DUA skala waktu, dan grafik ini punya dua
	// lapis yang masing-masing memperlihatkan satu di antaranya:
	//
	//   lambat (~7,5 jam) — bentuk harian; inilah yang terlihat di grafik 24 jam
	//   cepat  (menitan)  — ayunan sesaat; inilah yang terlihat di grafik 1 jam
	//
	// Versi pertama cuma punya skala cepat dengan periode pendek (90-300 detik,
	// disalin dari DemoSnapshot), dan hasilnya salah dua kali: lapis halus
	// mencuplik tiap 60 detik sehingga grafiknya beraliasing jadi gigi gergaji,
	// dan lapis kasar jadi bergerigi rapi seperti generator sinyal. Data demo
	// yang bentuknya tidak mungkin muncul di mesin nyata membuat tampilan
	// digarap untuk kasus yang tidak pernah ada.
	const lambat = 27000.0
	wob := func(t int64, base, amp, cepat float64) float64 {
		f := float64(t)
		return base + amp*(0.7*math.Sin(f/lambat+0.6)+0.3*math.Sin(f/cepat+1.3))
	}

	titik := func(t int64) Point {
		p := Point{
			T:        t,
			Load1:    round1(wob(t, 0.42, 0.18, 1800)),
			CPU:      round1(wob(t, 18, 9, 1500)),
			MemUsed:  int64(wob(t, 20.5, 0.9, 2400) * gib),
			SwapUsed: 0,
			ARC:      int64(wob(t, 9.8, 0.4, 3000) * gib),
			Pools: map[string]float64{
				"pool-cepat": round1(wob(t, 35.5, 0.4, 3600)),
				// Naik pelan-pelan sepanjang jendela: pool yang merangkak
				// penuh adalah bentuk masalah yang justru paling butuh grafik,
				// karena satu angka saja tidak menunjukkan arahnya.
				"pool-arsip": round1(92.4 - float64(now.Unix()-t)/float64(24*3600)*1.6),
				"pool-uji":   round1(wob(t, 24, 0.2, 4200)),
			},
			Disks: map[string]float64{
				"/dev/sda": round1(wob(t, 34, 1.5, 2600)),
				"/dev/sdb": round1(wob(t, 36, 1.5, 3100)),
				"/dev/sdc": round1(wob(t, 41, 2, 3800)),
				"/dev/sdd": round1(wob(t, 50, 2.5, 3400)),
			},
		}
		// /dev/sde tidak pernah muncul: di data demo ia sedang tidur, dan disk
		// tidur memang TIDAK dicatat. Kartunya jadi contoh nyata bagaimana
		// "tidak ada riwayat" harus terlihat.
		return p
	}

	base := now.Unix()
	for i := FineMax - 1; i >= 0; i-- {
		t := base - int64(i)*int64(FineStep.Seconds())
		// Satu lubang yang disengaja: agent mati sekitar 20 menit lalu selama
		// beberapa menit. Sparkline harus menggambarnya PUTUS, bukan menyambung
		// lurus — garis lurus palsu menyembunyikan justru hal yang ingin
		// diketahui, yaitu ada periode yang tidak terpantau.
		if i >= 18 && i <= 22 {
			continue
		}
		f.Fine.Points = append(f.Fine.Points, titik(t))
	}
	// Lapis kasar dibangun dengan MERATA-RATAKAN jendela 30 menit, memakai
	// mean() yang sama dengan ring buffer sungguhan — bukan mencuplik satu
	// titik tiap 30 menit.
	//
	// Bedanya kelihatan: mencuplik tiap 1800 detik membuat metrik yang
	// berayun cepat beraliasing jadi gigi gergaji, dan grafik 24 jam demo
	// jadi berbohong tentang bentuk data yang sebenarnya akan muncul.
	for i := CoarseMax - 1; i >= 0; i-- {
		akhir := base - int64(i)*int64(CoarseStep.Seconds())
		var jendela []Point
		for d := int64(CoarseStep.Seconds()) - 60; d >= 0; d -= 60 {
			jendela = append(jendela, titik(akhir-d))
		}
		f.Coarse.Points = append(f.Coarse.Points, mean(jendela))
	}
	return f
}
