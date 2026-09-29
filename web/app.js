/* issboard — dashboard kesehatan host.
 *
 * Vanilla, tanpa build step, tanpa aset pihak ketiga: halaman ini disajikan
 * dengan Content-Security-Policy default-src 'self' dan harus tetap berfungsi
 * di host tanpa akses internet.
 *
 * Vonis TIDAK dihitung di sini. Aturannya ada di internal/health (Go), supaya
 * issboard-agent memakai aturan yang persis sama saat mengirim notifikasi.
 * Berkas ini hanya menggambar apa yang sudah diputuskan server.
 */
'use strict';

const REFRESH_MS = 15000;
// Riwayat ditulis agent tiap menit, jadi menariknya tiap 15 detik cuma
// membaca berkas yang sama tiga kali sia-sia.
const HISTORY_MS = 60000;

// Dikembalikan sebagai [angka, satuan] supaya satuannya bisa dicetak kecil
// seperti kartu lain — dan supaya "37h" tidak terbaca sebagai 37 jam.
function uptimeParts(sec) {
  const d = Math.floor(sec / 86400);
  if (d > 0) return [d, d === 1 ? 'hari' : 'hari'];
  const h = Math.floor(sec / 3600);
  if (h > 0) return [h, 'jam'];
  return [Math.floor(sec / 60), 'menit'];
}

/* ---------- pastel ----------
   Pastel di sini adalah IDENTITAS data, bukan status: warnanya menandai
   "pool yang mana", bukan "sehat atau tidak". Status memakai warna pekat,
   dan hanya saat bermasalah. Dipetakan dari nama supaya satu pool memegang
   warna yang sama di tiap kartu — dan, nanti, di tiap sparkline. */
// Tidak ada kuning/oranye/merah di sini — lihat alasannya di nothing.css:
// warna itu sudah dipakai status, dan identitas data tidak boleh meminjamnya.
const PASTELS = ['mint', 'sky', 'lav', 'teal'];

function pastelFor(name) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  const p = PASTELS[h % PASTELS.length];
  return `--fill: var(--p-${p}-fill); --line: var(--p-${p}-line);`;
}

/* ---------- sparkline ----------
   Digambar tangan sebagai satu <path> SVG. TIDAK ADA pustaka grafik: deret
   angka jadi garis itu belasan baris, dan menariknya akan melanggar janji
   "tanpa aset pihak ketiga" yang membuat halaman ini bisa disajikan dengan
   default-src 'self' di host tanpa internet.

   Warnanya memakai pastel yang sama dengan bar di kartu yang sama — pastel
   di sini identitas ("pool yang mana"), bukan status. */

let HIST = null;   // isi /api/v1/history
let LAST = null;   // snapshot /api/v1/status terakhir, untuk digambar ulang
let RANGE = localStorage.getItem('issboard-range') === 'coarse' ? 'coarse' : 'fine';

/* Titik live dari polling /status, disimpan di memori browser saja.
   Agent menulis riwayat tiap menit; menyambung titik yang baru saja dibaca ke
   ekor grafik membuat halaman terasa hidup tanpa daemon di server. Hilang
   saat halaman ditutup, dan itu memang tidak masalah — yang permanen ada di
   berkas milik agent. */
const LIVE = new Map();
const LIVE_MAX = 240;

function liveKey(spec) { return spec.k + (spec.name ? ':' + spec.name : ''); }

function pushLive(spec, t, v) {
  const k = liveKey(spec);
  const arr = LIVE.get(k) || [];
  const last = arr[arr.length - 1];
  if (last && t - last.t < 5) return;   // hindari dobel saat refresh manual
  arr.push({ t, v });
  if (arr.length > LIVE_MAX) arr.splice(0, arr.length - LIVE_MAX);
  LIVE.set(k, arr);
}

// Nilai satu metrik dari satu titik riwayat. null berarti TIDAK TERBACA —
// dibedakan dari nol, dan digambar sebagai putus, bukan sebagai jurang.
function pointValue(p, spec) {
  switch (spec.k) {
    case 'cpu':   return p.cpu < 0 ? null : p.cpu;   // -1 = belum terhitung
    case 'load1': return p.load1;
    case 'mem':   return p.mem_used;
    case 'swap':  return p.swap_used;
    case 'arc':   return p.arc;
    case 'pool':  return p.pools ? valOrNull(p.pools[spec.name]) : null;
    case 'disk':  return p.disks ? valOrNull(p.disks[spec.name]) : null;
  }
  return null;
}

function valOrNull(v) { return (v === undefined || v === null) ? null : v; }

// Nilai yang sama, tapi dari snapshot /status — supaya ekor grafiknya
// menyambung ke angka yang sedang tertulis besar di kartu.
function liveValue(d, spec) {
  const h = d.host || {};
  switch (spec.k) {
    case 'cpu':   return h.cpu_percent < 0 ? null : round1(h.cpu_percent);
    case 'load1': return h.load1;
    case 'mem':   return h.mem_total_bytes - h.mem_available_bytes;
    case 'swap':  return h.swap_total_bytes - h.swap_free_bytes;
    case 'arc':   return h.arc_size_bytes;
    case 'pool': {
      const p = (d.pools || []).find((x) => x.name === spec.name);
      return p && p.size_bytes > 0 ? round1((p.alloc_bytes / p.size_bytes) * 100) : null;
    }
    case 'disk': {
      const k = ((d.smart || {}).disks || []).find((x) => x.device === spec.name);
      // Disk tidur tidak melaporkan suhu; itu bukan 0 °C.
      return k && !k.standby && k.temperature_c ? k.temperature_c : null;
    }
  }
  return null;
}

function round1(v) { return Math.round(v * 10) / 10; }

/* Rentang sumbu Y.

   Autoscale murni membesar-besarkan hal sepele: pool yang bergerak dari 62,0%
   ke 62,4% akan tergambar seperti tebing. Karena itu tiap metrik punya
   rentang MINIMUM — grafiknya baru benar-benar naik kalau perubahannya
   memang berarti. */
const SPAN_MIN = {
  cpu: 20, load1: 0.5, pool: 8, disk: 6,
};

function domain(values, spec) {
  let lo = Math.min(...values), hi = Math.max(...values);
  // Metrik byte tidak punya satuan tetap, jadi minimumnya relatif.
  const min = SPAN_MIN[spec.k] !== undefined ? SPAN_MIN[spec.k] : Math.max(hi * 0.08, 1);
  if (hi - lo < min) {
    const mid = (hi + lo) / 2;
    lo = mid - min / 2;
    hi = mid + min / 2;
  }
  // Persen tidak pernah keluar 0..100; menggambar di luar itu menyesatkan.
  if (spec.k === 'cpu' || spec.k === 'pool') { lo = Math.max(0, lo); hi = Math.min(100, hi); }
  if (spec.k === 'load1') lo = Math.max(0, lo);
  if (hi - lo <= 0) hi = lo + 1;
  return [lo, hi];
}

/* Memecah deret jadi potongan-potongan yang boleh disambung.

   Riwayat yang bolong — agent mati, mesin baru menyala — digambar PUTUS.
   Menyambungnya lurus akan menyembunyikan justru hal yang ingin diketahui:
   ada periode yang tidak terpantau sama sekali. */
function segments(series, step) {
  const out = [];
  let cur = [];
  let prev = null;
  for (const s of series) {
    const bolong = prev !== null && (s.t - prev) > step * 2.5;
    if (s.v === null || bolong) {
      if (cur.length) out.push(cur);
      cur = [];
    }
    if (s.v !== null) cur.push(s);
    prev = s.t;
  }
  if (cur.length) out.push(cur);
  return out;
}

const SPARK_H = 34;

function sparkSVG(series, step, spec) {
  const vals = series.filter((s) => s.v !== null).map((s) => s.v);
  if (vals.length < 2) return null;

  const [lo, hi] = domain(vals, spec);
  const t0 = series[0].t;
  const tspan = Math.max(1, series[series.length - 1].t - t0);
  const pad = 2;
  const x = (t) => ((t - t0) / tspan) * 100;
  const y = (v) => SPARK_H - pad - ((v - lo) / (hi - lo)) * (SPARK_H - pad * 2);

  let d = '';
  for (const seg of segments(series, step)) {
    if (seg.length === 1) {
      // Titik tunggal tidak menghasilkan garis apa pun. Diberi ruas sangat
      // pendek supaya tetap terlihat — data satu titik tetap data.
      d += `M${x(seg[0].t).toFixed(2)} ${y(seg[0].v).toFixed(2)}h.6`;
      continue;
    }
    seg.forEach((s, i) => {
      d += (i ? 'L' : 'M') + x(s.t).toFixed(2) + ' ' + y(s.v).toFixed(2) + ' ';
    });
  }
  if (!d) return null;

  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', `0 0 100 ${SPARK_H}`);
  svg.setAttribute('preserveAspectRatio', 'none');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', d.trim());
  path.setAttribute('fill', 'none');
  path.setAttribute('stroke', 'var(--line, var(--n-ink-soft))');
  path.setAttribute('stroke-width', '1.5');
  path.setAttribute('stroke-linecap', 'round');
  path.setAttribute('stroke-linejoin', 'round');
  // Tanpa ini, garisnya ikut melar mengikuti lebar kartu dan jadi tebal
  // sebelah — akibat preserveAspectRatio="none" yang memang disengaja.
  path.setAttribute('vector-effect', 'non-scaling-stroke');
  svg.append(path);
  return svg;
}

// Ringkasan yang bisa dibaca pembaca layar dan ditampilkan sebagai judul —
// grafik tanpa angka tidak berarti apa-apa kalau tidak bisa dilihat.
function sparkTitle(series, spec) {
  const vals = series.filter((s) => s.v !== null).map((s) => s.v);
  if (!vals.length) return 'belum ada riwayat';
  const fmt = (v) => (spec.k === 'mem' || spec.k === 'arc' || spec.k === 'swap')
    ? bytes(v) : String(round1(v));
  const jam = RANGE === 'coarse' ? '24 jam' : '1 jam';
  return `${jam} terakhir · terendah ${fmt(Math.min(...vals))} · tertinggi ${fmt(Math.max(...vals))}`;
}

/* Satu kotak sparkline. Ukurannya sudah dikunci sejak fase 1, jadi mengisinya
   tidak menggeser satu pun kartu. */
function spark(spec) {
  const box = el('div', { class: 'spark' });
  if (spec && spec.name) box.style.cssText = pastelFor(spec.name);
  else if (spec) box.style.cssText = pastelFor(spec.k);

  if (!HIST) {
    box.classList.add('empty');
    box.title = 'riwayat belum dimuat';
    return box;
  }

  const layer = RANGE === 'coarse' ? HIST.coarse : HIST.fine;
  const step = (layer && layer.step_seconds) || 60;
  let series = ((layer && layer.points) || []).map((p) => ({ t: p.t, v: pointValue(p, spec) }));

  // Titik live cuma disambung ke lapis halus. Di grafik 24 jam, satu titik
  // semenit tidak menambah apa pun selain kerja.
  if (RANGE === 'fine') {
    const live = LIVE.get(liveKey(spec)) || [];
    const akhir = series.length ? series[series.length - 1].t : 0;
    for (const s of live) if (s.t > akhir) series.push(s);
  }

  const svg = sparkSVG(series, step, spec);
  if (!svg) {
    box.classList.add('empty');
    box.title = HIST.note || 'belum cukup riwayat untuk digambar';
    return box;
  }
  box.title = sparkTitle(series, spec);
  box.append(svg);
  return box;
}

/* ---------- temuan ---------- */
const RANK = { crit: 2, warn: 1, ok: 0 };

// Tingkat terburuk per subjek, supaya tiap kartu bisa menyalakan titik
// statusnya sendiri tanpa mengulang logika vonis.
function worstBySubject(findings) {
  const m = new Map();
  for (const f of findings || []) {
    if (!f.subject) continue;
    if (RANK[f.level] > RANK[m.get(f.subject) || 'ok']) m.set(f.subject, f.level);
  }
  return m;
}

/* ---------- render ---------- */
function render(d) {
  LAST = d;
  collectLive(d);
  const worst = worstBySubject(d.findings);

  renderHeader(d);
  renderHero(d);
  renderFindings(d.findings);
  renderHost(d.host, d.system);
  renderPools(d.pools, worst);
  renderDisks(d.smart, worst);
  renderContainers(d.containers, worst);
  renderDatasets(d.datasets, d.snap_policy, worst);
  renderErrors(d);
}

// Mencatat nilai yang baru saja dibaca supaya ekor grafik ikut bergerak di
// antara dua tulisan agent. Hanya di memori browser — server tetap tidak
// menyimpan apa pun selama halaman terbuka.
function collectLive(d) {
  const t = Math.floor(new Date(d.collected_at).getTime() / 1000) || Math.floor(Date.now() / 1000);
  const spesifikasi = [{ k: 'cpu' }, { k: 'load1' }, { k: 'mem' }, { k: 'swap' }, { k: 'arc' }];
  for (const p of d.pools || []) spesifikasi.push({ k: 'pool', name: p.name });
  for (const k of ((d.smart || {}).disks || [])) spesifikasi.push({ k: 'disk', name: k.device });
  for (const spec of spesifikasi) {
    const v = liveValue(d, spec);
    if (v !== null && v !== undefined && !Number.isNaN(v)) pushLive(spec, t, v);
  }
}

function renderHeader(d) {
  const hp = $('host-pill');
  hp.textContent = d.host && d.host.hostname ? d.host.hostname : '—';
  hp.hidden = false;
  $('demo-pill').hidden = !d.demo;
  $('updated').textContent = 'diperbarui ' + relTime(d.collected_at);

  // Grafik kosong punya dua arti yang jauh berbeda: mesin baru dipasang, atau
  // timer agent-nya mati. Tanpa catatan ini keduanya terlihat sama — halaman
  // yang tenang. Agent tidak bisa mengabari bahwa dirinya sendiri berhenti.
  const hn = $('hist-note');
  hn.textContent = (HIST && HIST.note) || '';
  hn.hidden = !hn.textContent;
}

function renderHero(d) {
  const v = d.verdict || { level: 'ok', warn: 0, crit: 0 };
  const out = $('verdict');
  clear(out);
  out.className = 'verdict ' + (v.level === 'ok' ? 'ok' : v.level);

  // Angka yang dibuat besar, kata-katanya kecil dan huruf biasa. Versi
  // sebelumnya menulis "6 KRITIS / 8 PERHATIAN" kapital semua, dan teksnya
  // berebut perhatian dengan angkanya sendiri — padahal justru angka itu
  // yang harus tertangkap lebih dulu.
  const baris = (n, teks, level) => el('div', { class: 'vrow' },
    el('span', { class: 'vnum ' + level }, String(n)),
    el('span', { class: 'vtext' }, teks));

  if (v.level === 'ok') {
    // Tidak ada angka untuk disorot, jadi katanya yang jadi tokoh utama —
    // dan kata boleh memakai font dot-matrix.
    out.append(el('p', { class: 'vok n-num' }, 'Sehat'),
      el('p', { class: 'vtext' }, 'tidak ada yang perlu diurus'));
    return;
  }

  if (v.crit) out.append(baris(v.crit, 'perlu ditangani sekarang', 'crit'));
  if (v.warn) out.append(baris(v.warn, 'perlu diperiksa', 'warn'));
}

/* Daftar temuan — jawaban atas angka besar di kartu vonis.
   Sampai sekarang `findings[]` cuma dipakai menyalakan titik status di tiap
   kartu, jadi "6 perlu ditangani sekarang" tidak pernah bisa dijawab tanpa
   menyisir seluruh halaman sendiri. Di layar lebar daftar ini menempel di
   rail, jadi jawabannya ikut saat Anda menggulung. */
const FINDINGS_MAX = 8;

function renderFindings(fs) {
  const kartu = $('findings-card');
  const daftar = $('findings');
  clear(daftar);

  const semua = fs || [];
  kartu.hidden = semua.length === 0;
  // Jumlahnya ditulis di ringkasan, jadi kartu yang dilipat tetap memberi
  // tahu ada berapa — melipat menyembunyikan rinciannya, bukan kabarnya.
  $('findings-count').textContent = semua.length ? `(${semua.length})` : '';
  if (!semua.length) return;

  for (const f of semua.slice(0, FINDINGS_MAX)) {
    daftar.append(el('li', {},
      statusDot(f.level),
      el('div', { class: 'fbody' },
        el('b', {}, f.title),
        el('span', {}, f.detail))));
  }
  // Sisanya disebut jumlahnya, tidak dibuang diam-diam: daftar yang dipotong
  // tanpa keterangan membuat orang mengira itu semuanya.
  if (semua.length > FINDINGS_MAX) {
    daftar.append(el('li', { class: 'more' },
      `+ ${semua.length - FINDINGS_MAX} temuan lagi — ada di kartunya masing-masing`));
  }
}

// Satu blok metrik di dalam kartu host. `sp` adalah spesifikasi sparkline
// ({k: 'cpu'} dan seterusnya); ruangnya sudah berukuran tetap sejak fase 1,
// jadi grafiknya mengisi tanpa menggeser apa pun. Metrik yang memang tidak
// punya riwayat — uptime — tidak memesan ruang sama sekali.
//
// Penjelasannya TIDAK ditaruh di sini. Kolom metrik lebarnya cuma ~132px, dan
// paragraf sepanjang penjelasan ARC di dalamnya berubah jadi pita teks sempit
// yang menarik seluruh kartu host memanjang ke bawah. `daftar` mendaftarkannya
// ke satu pita selebar grid — lihat penjelas() di bawah.
function metric(label, value, unit, sub, extra, sp, help, daftar) {
  // Label bisa diketuk untuk membuka penjelasan. Sengaja BUKAN atribut title:
  // tooltip hover tidak ada di layar sentuh, dan halaman ini paling sering
  // dibuka dari HP. Nama indikator seperti "ARC" tidak menjelaskan dirinya
  // sendiri, dan menebak-nebak arti angka lebih buruk daripada tidak melihat.
  const head = help
    ? el('button', { class: 'n-label metric-label', type: 'button', 'aria-expanded': 'false' }, label)
    : el('span', { class: 'n-label' }, label);

  if (help && daftar) daftar(head, label, help);

  return el('div', { class: 'metric' },
    head,
    el('div', { class: 'stat' },
      value, unit ? el('span', { class: 'unit' }, unit) : null),
    extra || null,
    sub ? el('div', { class: 'sub' }, sub) : null,
    sp ? spark(sp) : null);
}

/* Penjelasan metrik yang sedang terbuka, disimpan lintas render.
   Halaman menggambar ulang tiap 15 detik; tanpa ini, penjelasan yang sedang
   dibaca akan tertutup sendiri di tengah kalimat. */
let HINT_AKTIF = '';

/* penjelas() membuat SATU pita selebar seluruh grid metrik, plus fungsi untuk
   mendaftarkan tiap label ke situ.
   Hanya satu penjelasan terbuka pada satu waktu: membuka yang kedua menutup
   yang pertama, jadi kartunya tidak pernah tumbuh dua kali. */
function penjelas() {
  const judul = el('h3', {});
  const isi = el('p', {});
  const pita = el('div', { class: 'hint', hidden: 'hidden' }, judul, isi);
  const tombol = new Map();

  const tutupSemua = () => {
    for (const [, t] of tombol) t.btn.setAttribute('aria-expanded', 'false');
  };

  const buka = (label) => {
    const t = tombol.get(label);
    if (!t) return;
    tutupSemua();
    judul.textContent = label;
    isi.textContent = t.help;
    pita.hidden = false;
    t.btn.setAttribute('aria-expanded', 'true');
    HINT_AKTIF = label;
  };

  const tutup = () => {
    tutupSemua();
    pita.hidden = true;
    HINT_AKTIF = '';
  };

  return {
    pita,
    daftar(btn, label, help) {
      tombol.set(label, { btn, help });
      btn.addEventListener('click', () => (HINT_AKTIF === label ? tutup() : buka(label)));
    },
    // Dipanggil setelah grid selesai dibangun, untuk memulihkan penjelasan
    // yang sedang dibuka sebelum halaman digambar ulang.
    pulihkan() {
      if (HINT_AKTIF) buka(HINT_AKTIF);
    },
  };
}

function bar(fraction, level, style) {
  const w = Math.max(0, Math.min(100, fraction));
  return el('div', { class: 'bar ' + (level && level !== 'ok' ? level : ''), style: style || '' },
    el('i', { style: `width:${w}%` }));
}

// Penjelasan tiap indikator: apa yang diukur, dari mana dibaca, dan angka
// seperti apa yang sebenarnya bermasalah. Ditulis di satu tempat supaya
// gampang dipoles tanpa mengubah tata letak.
const HELP = {
  cpu: 'Persen waktu CPU yang benar-benar bekerja, dihitung dari SELISIH dua pembacaan /proc/stat — bukan rata-rata sejak boot. Waktu menunggu disk (iowait) dihitung sebagai menganggur, karena CPU-nya memang tidak bekerja.',

  mem: 'RAM terpakai = MemTotal − MemAvailable dari /proc/meminfo. MemAvailable sudah memperhitungkan cache yang bisa dilepas kapan saja, jadi angkanya lebih jujur daripada sekadar "free". Di mesin ZFS, ARC tidak ikut dihitung sebagai memori bebas walau sebenarnya bisa dilepas.',

  arc: 'ARC = Adaptive Replacement Cache, cache baca milik ZFS yang tinggal di RAM. Data yang sering dibaca disimpan di sini supaya ZFS tidak perlu menyentuh disk sama sekali. Angka besarnya = ukuran cache saat ini; "dari batas" = dibanding c_max, batas atas yang diizinkan; "hit" = berapa persen pembacaan yang terlayani dari RAM, bukan dari disk — makin tinggi makin baik, di atas 90% itu sehat. ARC yang hampir penuh adalah hal NORMAL dan justru diinginkan: ZFS sengaja memakai RAM yang sedang menganggur, dan akan melepaskannya sendiri saat aplikasi butuh. Karena itu bar ini tidak pernah berwarna peringatan. Dibaca dari /proc/spl/kstat/zfs/arcstats.',

  swap: 'Bagian RAM yang dipindahkan ke disk karena RAM penuh. Dari SwapTotal dan SwapFree di /proc/meminfo. Swap yang terpakai banyak sementara RAM terlihat lega adalah tanda mesin pernah kehabisan memori — dan disk ribuan kali lebih lambat daripada RAM.',

  load: 'Load average BUKAN persen: rata-rata banyaknya proses yang sedang jalan atau menunggu giliran, selama 1, 5, dan 15 menit terakhir. Bandingkan dengan jumlah core — 4,0 di mesin 8 core berarti setengah beban, sedangkan 4,0 di mesin 2 core berarti kewalahan. Membandingkan ketiga angkanya menunjukkan arah: 1m jauh di atas 15m berarti beban baru saja naik. Dari /proc/loadavg.',

  uptime: 'Lama mesin menyala sejak boot terakhir, dari /proc/uptime. Ini uptime MESIN, bukan uptime issboard — prosesnya sendiri memang mati-hidup mengikuti socket activation.',
};

function renderHost(h, sys) {
  const box = $('host');
  clear(box);
  if (!h) return;

  const memUsed = h.mem_total_bytes - h.mem_available_bytes;
  const memPct = pct(memUsed, h.mem_total_bytes);
  const arcPct = pct(h.arc_size_bytes, h.arc_max_bytes);
  const swapUsed = h.swap_total_bytes - h.swap_free_bytes;
  const up = uptimeParts(h.uptime_seconds);

  const jelas = penjelas();
  const grid = el('div', { class: 'metrics' },

    // -1 berarti belum ada dua cuplikan /proc/stat untuk dibandingkan, bukan
    // 0% terpakai. Ditulis "—" supaya tidak terbaca sebagai mesin menganggur.
    h.cpu_percent < 0
      ? metric('cpu', '—', '', 'butuh dua pembacaan untuk dihitung', null, { k: 'cpu' }, HELP.cpu, jelas.daftar)
      : metric('cpu', Math.round(h.cpu_percent), '%',
        sys && sys.cpu_cores ? `${sys.cpu_cores} core` : null,
        bar(h.cpu_percent, h.cpu_percent >= 90 ? 'crit' : 'ok', pastelFor('cpu')), { k: 'cpu' }, HELP.cpu, jelas.daftar),

    metric('memori', memPct, '%',
      `${bytes(memUsed)} dari ${bytes(h.mem_total_bytes)}`,
      bar(memPct, memPct >= 90 ? 'crit' : memPct >= 80 ? 'warn' : 'ok', pastelFor('mem')), { k: 'mem' }, HELP.mem, jelas.daftar),

    // ARC penuh itu normal dan justru diinginkan — ZFS memang memakai RAM
    // yang menganggur sebagai cache. Bar ini tidak pernah jadi warna pekat:
    // ia informasi, bukan peringatan.
    metric('arc zfs', bytes(h.arc_size_bytes), '',
      `${arcPct}% dari batas · hit ${h.arc_hit_ratio.toFixed(1)}%`,
      bar(arcPct, 'ok', pastelFor('arc')), { k: 'arc' }, HELP.arc, jelas.daftar),

    // Swap yang terpakai penting justru saat RAM terlihat lega: mesin itu
    // sedang menukar kecepatan dengan diam-diam.
    h.swap_total_bytes > 0
      ? metric('swap', bytes(swapUsed), '',
        `dari ${bytes(h.swap_total_bytes)}`,
        bar(pct(swapUsed, h.swap_total_bytes),
          pct(swapUsed, h.swap_total_bytes) >= 50 ? 'warn' : 'ok', pastelFor('swap')), { k: 'swap' }, HELP.swap, jelas.daftar)
      : metric('swap', '—', '', 'tidak ada swap', null, false, HELP.swap),

    // Load average BUKAN persen: 4,0 di mesin 8 core berarti setengah beban.
    // Karena itu tidak diberi bar — bar menyiratkan skala 0–100 yang keliru.
    metric('load 1m', h.load1.toFixed(2), '',
      `5m ${h.load5.toFixed(2)} · 15m ${h.load15.toFixed(2)}`
      + (sys && sys.cpu_cores ? ` · dari ${sys.cpu_cores} core` : ''), null, { k: 'load1' }, HELP.load, jelas.daftar),

    metric('uptime', up[0], up[1], 'sejak boot terakhir', null, null, HELP.uptime, jelas.daftar),

    // Pita penjelasan menutup grid: selebar seluruh kolom, jadi kartunya
    // tidak pernah memanjang karena satu kolom sempit kebanjiran teks.
    jelas.pita);

  box.append(el('div', { class: 'n-card' }, grid, systemDetails(sys, h)));
  jelas.pulihkan();
}

// Identitas mesin, dilipat di dalam kartu host. Jarang berubah, jadi tidak
// perlu memakan ruang tetap — tapi distro dan kernel adalah pertanyaan pertama
// saat sesuatu berperilaku aneh, dan issboard dipakai di luar Debian juga.
function systemDetails(sys, h) {
  if (!sys) return null;

  const kv = el('dl', { class: 'kv' });
  const add = (k, v) => { kv.append(el('dt', {}, k), el('dd', {}, v || '—')); };

  add('distro', sys.distro);
  add('kernel', sys.kernel);
  add('arsitektur', sys.arch);
  add('prosesor', sys.cpu_model);
  add('core', sys.cpu_cores ? String(sys.cpu_cores) : '');
  // 0 = sensor tidak terbaca (biasa di VM dan sebagian board), bukan 0 °C.
  add('suhu cpu', sys.cpu_temp_c > 0 ? `${sys.cpu_temp_c.toFixed(1)} °C` : '— sensor tidak terbaca');
  add('hostname', h ? h.hostname : '');
  add('runtime', sys.go_version);

  const d = el('details', { class: 'sysinfo' },
    el('summary', { class: 'n-label' }, 'informasi sistem'), kv);
  return d;
}

function renderPools(pools, worst) {
  const box = $('pools');
  clear(box);
  if (!pools || !pools.length) {
    box.append(el('p', { class: 'note' }, 'Tidak ada pool ZFS terbaca di host ini.'));
    return;
  }

  for (const p of pools) {
    const used = pct(p.alloc_bytes, p.size_bytes);
    const lvl = worst.get(p.name) || 'ok';
    const online = (p.health || '').toUpperCase() === 'ONLINE';

    box.append(el('div', { class: 'n-card' },
      el('div', { class: 'card-head' },
        el('div', { class: 'card-title' }, statusDot(lvl), el('span', { class: 'name' }, p.name)),
        el('span', { class: 'n-pill ' + (online ? '' : 'n-pill-crit') }, p.health || '?')),

      el('div', { class: 'stat' }, used, el('span', { class: 'unit' }, '% terpakai')),
      bar(used, used >= 90 ? 'crit' : used >= 80 ? 'warn' : 'ok', pastelFor(p.name)),

      el('div', { class: 'sub' },
        `${bytes(p.alloc_bytes)} / ${bytes(p.size_bytes)} · frag ${p.fragmentation_pct}%`,
        el('br'),
        // Stripe vs mirror ditulis apa adanya di tiap kartu, bukan cuma saat
        // jadi temuan: ini sifat pool yang menentukan apakah scrub bisa
        // memperbaiki atau cuma bisa memberi tahu.
        p.mirrored ? 'redundan (mirror/raidz)'
          : (p.devices && p.devices.length > 1 ? 'STRIPE — tanpa redundansi' : 'disk tunggal'),
        el('br'),
        'scrub: ' + scrubText(p.scan_line)),

      spark({ k: 'pool', name: p.name })));
  }
}

function renderDisks(smart, worst) {
  const box = $('disks');
  clear(box);
  const note = $('smart-note');

  if (!smart || !smart.disks || !smart.disks.length) {
    note.textContent = 'Belum ada data SMART. issboard tidak pernah memanggil smartctl sendiri — datanya ditulis issboard-smart.timer.';
    return;
  }

  note.textContent = smart.stale
    ? `Cache SMART basi (ditulis ${relTime(smart.written_at)}). issboard hanya membaca; periksa issboard-smart.timer.`
    : `Dibaca dari cache yang ditulis ${relTime(smart.written_at)}. smartctl tidak pernah dipanggil saat halaman dibuka, supaya disk yang tidur tetap tidur.`;

  for (const d of smart.disks) {
    const lvl = worst.get(d.device) || 'ok';

    box.append(el('div', { class: 'n-card' },
      el('div', { class: 'card-head' },
        el('div', { class: 'card-title' }, statusDot(lvl), el('span', { class: 'name' }, d.device)),
        d.standby ? el('span', { class: 'n-pill' }, 'tidur')
          : el('span', { class: 'n-pill ' + (d.passed ? '' : 'n-pill-crit') }, d.passed ? 'passed' : 'gagal')),

      // Disk standby memang tidak melaporkan suhu. Ditulis "—", bukan 0°C:
      // angka nol akan terbaca sebagai dingin, padahal artinya tak terbaca.
      el('div', { class: 'stat' },
        d.standby || !d.temperature_c ? '—' : d.temperature_c,
        el('span', { class: 'unit' }, d.standby ? 'sedang tidur' : '°C')),

      el('div', { class: 'sub' },
        d.model || 'model tak dikenal',
        el('br'),
        `nyala ${Math.round(d.power_on_hours / 24)} hari · realloc ${d.reallocated_sectors} · pending ${d.pending_sectors}`,
        el('br'),
        'self-test: ' + (d.last_self_test || 'belum pernah selesai')),

      // Disk yang sedang tidur memang tidak punya riwayat suhu: agent TIDAK
      // mencatat disk standby, karena 0 °C akan terbaca sebagai dingin.
      // Kotaknya dibiarkan kosong, bukan diisi garis nol yang mengarang.
      spark({ k: 'disk', name: d.device })));
  }
}

function renderContainers(cs, worst) {
  const box = $('containers');
  clear(box);
  if (!cs || !cs.length) {
    box.append(el('p', { class: 'note' }, 'Tidak ada container terbaca — apakah socket-nya terjangkau?'));
    return;
  }

  const rows = el('div', { class: 'rows' });
  for (const c of cs) {
    const lvl = worst.get(c.name) || 'ok';
    const tail = [];

    if (c.health) {
      tail.push(el('span', {
        class: 'n-pill ' + (c.health.toLowerCase() === 'healthy' ? '' : 'n-pill-crit'),
      }, c.health));
    }
    // Container yang Up tapi tak punya network terlihat normal di `docker ps`.
    // Di sini ia diberi label eksplisit, karena itulah bentuk kegagalan yang
    // paling lama tidak ketahuan.
    if ((c.state || '').toLowerCase() === 'running' && !(c.networks || []).length) {
      tail.push(el('span', { class: 'n-pill n-pill-crit' }, 'tanpa network'));
    }
    // Chip port ditampilkan netral, TIDAK diwarnai peringatan di sini.
    // Sebelumnya JavaScript memutuskan sendiri bahwa 0.0.0.0 berarti bahaya —
    // itu aturan vonis yang bocor ke browser, persis yang dilarang design.md
    // §6. Yang menentukan bahaya sekarang hanya internal/health, dan hasilnya
    // sampai ke sini lewat titik status baris ini.
    for (const p of c.published_ports || []) {
      tail.push(el('span', { class: 'n-pill' }, p));
    }

    rows.append(el('div', { class: 'row' },
      statusDot(lvl),
      el('div', { class: 'rname' }, c.name),
      el('div', { class: 'rmeta' }, `${c.image} · ${c.status}`),
      el('div', { class: 'rtail' }, tail)));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

function renderDatasets(ds, pol, worst) {
  const box = $('datasets');
  clear(box);
  if (!ds || !ds.length) {
    box.append(el('p', { class: 'note' }, 'Tidak ada dataset terbaca.'));
    return;
  }

  const punyaKebijakan = !!(pol && pol.present);

  const rows = el('div', { class: 'rows' });
  for (const d of ds) {
    const p = d.snap_policy;
    const umur = d.last_snapshot ? relTime(d.last_snapshot) : 'belum pernah';

    // Warna baris mengikuti aturan yang sama dengan daftar temuan, dan
    // dihitung dari sumber yang sama — bukan aturan kedua yang ditulis ulang
    // di JavaScript. Dua salinan aturan di dua bahasa akan berbeda pelan-pelan
    // (docs/design.md §9.1); yang di sini cuma menerjemahkan hasilnya.
    const temuan = worst.get(d.name);
    let dot = 'n-dot-off';
    if (temuan === 'crit') dot = 'n-dot-crit';
    else if (temuan === 'warn') dot = 'n-dot-warn';
    else if (d.snapshot_count > 0) dot = 'n-dot-ok';

    // Pil kebijakan menjawab "kenapa dataset ini ikut / tidak ikut" tanpa
    // harus membuka berkas konfigurasi lewat SSH. Ia hanya muncul kalau ada
    // berkas kebijakan untuk dibandingkan — di mesin tanpa snapshot terkelola,
    // "tanpa kebijakan" di tiap baris cuma kebisingan yang selalu benar.
    let kebijakan = null;
    if (punyaKebijakan) {
      if (p) {
        kebijakan = el('span', { class: 'n-pill', title: `bagian [${p.section}]` +
          (p.template ? ` · template ${p.template}` : '') +
          ` · simpan ${p.hourly}/jam ${p.daily}/hari ${p.monthly}/bulan` +
          (p.autosnap ? '' : ' · autosnap mati') }, p.autosnap ? 'auto' : 'auto mati');
      } else if (d.snap_exempt) {
        kebijakan = el('span', { class: 'n-pill', title:
          'dikecualikan lewat snapshot_exempt di config' }, 'dikecualikan');
      } else {
        // Warna alarm HANYA kalau aturannya sendiri menganggapnya temuan.
        // Dataset wadah tidak tercakup apa pun dan memang tidak perlu — pil
        // oranye di baris semacam itu adalah alarm untuk keadaan normal,
        // dan itu cara tercepat membuat orang berhenti membaca warnanya.
        kebijakan = el('span', { class: 'n-pill' + (temuan ? ' n-pill-warn' : ''), title:
          `tidak masuk bagian mana pun di ${pol.source || 'berkas kebijakan'}` }, 'tanpa kebijakan');
      }
    }

    rows.append(el('div', { class: 'row' },
      el('span', { class: 'n-dot ' + dot }),
      el('div', { class: 'rname' }, d.name),
      el('div', { class: 'rmeta' }, d.mountpoint || '—'),
      el('div', { class: 'rtail' },
        el('span', { class: 'n-pill' }, bytes(d.used_bytes)),
        kebijakan,
        el('span', { class: 'n-pill', title: `${d.snapshot_count} snapshot` }, umur))));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

// Kegagalan per-bagian tidak menggagalkan seluruh halaman — tapi juga tidak
// boleh disembunyikan, karena layar yang terlihat sehat karena buta jauh lebih
// berbahaya daripada layar yang mengaku tidak tahu.
function renderErrors(d) {
  const box = $('errors');
  const errs = d.errors || [];
  box.hidden = !errs.length;
  clear(box);
  if (errs.length) box.append('Catatan pengumpulan: ' + errs.join(' · '));
}

/* Lipatan kartu temuan diingat antar kunjungan, sama seperti tema.
   Dipasang sekali di awal; <details> adalah elemen statis di HTML, jadi
   keadaannya selamat dari gambar ulang tiap 15 detik. */
function initFindingsFold() {
  const kartu = $('findings-card');
  if (localStorage.getItem('issboard-findings') === 'tutup') kartu.open = false;
  kartu.addEventListener('toggle', () => {
    localStorage.setItem('issboard-findings', kartu.open ? 'buka' : 'tutup');
  });
}

/* ---------- rentang grafik ---------- */
function initRange() {
  const b = $('range');
  const gambar = () => {
    b.textContent = RANGE === 'coarse' ? '24 jam' : '1 jam';
    b.title = RANGE === 'coarse'
      ? 'grafik memakai lapis kasar: 48 titik jarak 30 menit'
      : 'grafik memakai lapis halus: 60 titik jarak 1 menit';
  };
  gambar();
  b.addEventListener('click', () => {
    RANGE = RANGE === 'coarse' ? 'fine' : 'coarse';
    localStorage.setItem('issboard-range', RANGE);
    gambar();
    if (LAST) render(LAST);
  });
}

/* ---------- muat ---------- */
async function tick() {
  // Tab latar tidak perlu meminta apa pun: polling di sana hanya menahan
  // proses tetap hidup dan menunda idle-exit tanpa ada yang melihat.
  if (document.hidden) return;
  try {
    const r = await fetch('api/v1/status', { cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    render(await r.json());
  } catch (e) {
    $('updated').textContent = 'gagal memuat: ' + e.message;
  }
}

// Riwayat dibaca dari berkas milik agent. Endpoint ini tidak mengumpulkan
// apa pun — persis pola cache SMART.
async function tickHistory() {
  if (document.hidden) return;
  try {
    const r = await fetch('api/v1/history', { cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    HIST = await r.json();
    if (LAST) render(LAST);
  } catch (e) {
    // Riwayat yang gagal dimuat tidak boleh menjatuhkan seluruh halaman:
    // sisanya tetap berguna, dan kotak sparkline kembali ke keadaan kosong.
    HIST = { note: 'riwayat gagal dimuat: ' + e.message };
  }
}

// Riwayat diambil LEBIH DULU, baru snapshot. Kalau dibalik, gambar pertama
// selalu menampilkan kotak sparkline kosong sepersekian detik sebelum terisi
// — kedipan yang tidak perlu, dan di HP terlihat seperti halaman rusak.
async function boot() {
  await tickHistory();
  await tick();
}

// Tautan lama ke panel laci (#kelola) sekarang menuju halaman kelola sendiri.
if (location.hash === '#kelola') location.replace('admin.html');

initTheme();
initRange();
initFindingsFold();
boot();
setInterval(tick, REFRESH_MS);
setInterval(tickHistory, HISTORY_MS);

// Kembali terlihat: segarkan segera, jangan tunggu giliran interval berikutnya.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') { tick(); tickHistory(); }
});
