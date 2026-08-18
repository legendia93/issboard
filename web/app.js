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

/* ---------- helper DOM ----------
   Sengaja membangun node, bukan merangkai innerHTML: nama container, image,
   dan mountpoint datang dari sistem dan tidak boleh pernah diperlakukan
   sebagai markup. */
function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') n.className = v;
    else if (k === 'style') n.style.cssText = v;
    else n.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    n.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return n;
}

const $ = (id) => document.getElementById(id);
const clear = (node) => { while (node.firstChild) node.removeChild(node.firstChild); };

/* ---------- format ---------- */
function bytes(n) {
  if (!n || n < 0) return '0';
  const u = ['B', 'K', 'M', 'G', 'T', 'P'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)) + u[i];
}

// Dikembalikan sebagai [angka, satuan] supaya satuannya bisa dicetak kecil
// seperti kartu lain — dan supaya "37h" tidak terbaca sebagai 37 jam.
function uptimeParts(sec) {
  const d = Math.floor(sec / 86400);
  if (d > 0) return [d, d === 1 ? 'hari' : 'hari'];
  const h = Math.floor(sec / 3600);
  if (h > 0) return [h, 'jam'];
  return [Math.floor(sec / 60), 'menit'];
}

function pct(a, b) { return b > 0 ? Math.round((a / b) * 100) : 0; }

function relTime(iso) {
  const t = new Date(iso).getTime();
  if (!t) return '—';
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 60) return `${s} dtk lalu`;
  if (s < 3600) return `${Math.round(s / 60)} mnt lalu`;
  if (s < 86400) return `${Math.round(s / 3600)} jam lalu`;
  return `${Math.round(s / 86400)} hari lalu`;
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

function statusDot(level) {
  return el('span', { class: 'n-dot n-dot-' + (level || 'ok') });
}

/* ---------- render ---------- */
function render(d) {
  const worst = worstBySubject(d.findings);

  renderHeader(d);
  renderHero(d);
  renderHost(d.host);
  renderPools(d.pools, worst);
  renderDisks(d.smart, worst);
  renderContainers(d.containers, worst);
  renderDatasets(d.datasets);
  renderErrors(d);
}

function renderHeader(d) {
  const hp = $('host-pill');
  hp.textContent = d.host && d.host.hostname ? d.host.hostname : '—';
  hp.hidden = false;
  $('demo-pill').hidden = !d.demo;
  $('updated').textContent = 'diperbarui ' + relTime(d.collected_at);
}

function renderHero(d) {
  const v = d.verdict || { level: 'ok', warn: 0, crit: 0 };
  const out = $('verdict');
  out.className = 'n-num verdict ' + (v.level === 'ok' ? '' : v.level);

  if (v.level === 'ok') {
    out.textContent = 'SEHAT';
  } else {
    const bits = [];
    if (v.crit) bits.push(`${v.crit} KRITIS`);
    if (v.warn) bits.push(`${v.warn} PERHATIAN`);
    out.textContent = bits.join(' / ');
  }

  const list = $('findings');
  clear(list);

  // Daftar dipotong: kartu hero menjawab "ada apa", bukan menampung semuanya.
  // Sisanya tetap terlihat sebagai penanda di kartu masing-masing.
  const show = (d.findings || []).slice(0, 6);
  for (const f of show) {
    list.append(el('li', {},
      statusDot(f.level),
      el('div', {}, el('b', {}, f.title), ' — ', el('span', {}, f.detail))));
  }
  const sisa = (d.findings || []).length - show.length;
  if (sisa > 0) list.append(el('li', { class: 'more' }, `+ ${sisa} temuan lain, lihat kartu di bawah`));
}

function statCard(label, value, unit, sub, extra) {
  return el('div', { class: 'n-card' },
    el('span', { class: 'n-label' }, label),
    el('div', { class: 'stat', style: 'margin-top:10px' },
      value, unit ? el('span', { class: 'unit' }, unit) : null),
    extra || null,
    sub ? el('div', { class: 'sub' }, sub) : null,
    el('div', { class: 'spark', title: 'riwayat menyusul' }));
}

function bar(fraction, level, style) {
  const w = Math.max(0, Math.min(100, fraction));
  return el('div', { class: 'bar ' + (level && level !== 'ok' ? level : ''), style: style || '' },
    el('i', { style: `width:${w}%` }));
}

function renderHost(h) {
  const box = $('host');
  clear(box);
  if (!h) return;

  const memUsed = h.mem_total_bytes - h.mem_available_bytes;
  const memPct = pct(memUsed, h.mem_total_bytes);
  const arcPct = pct(h.arc_size_bytes, h.arc_max_bytes);
  const up = uptimeParts(h.uptime_seconds);

  box.append(
    statCard('uptime', up[0], up[1], 'sejak boot terakhir'),
    statCard('load 1m', h.load1.toFixed(2), '',
      `5m ${h.load5.toFixed(2)} · 15m ${h.load15.toFixed(2)}`),
    statCard('memori', memPct, '%',
      `${bytes(memUsed)} dari ${bytes(h.mem_total_bytes)}`,
      bar(memPct, memPct >= 90 ? 'crit' : memPct >= 80 ? 'warn' : 'ok', pastelFor('mem'))),
    // ARC penuh itu normal dan justru diinginkan — ZFS memang memakai RAM
    // yang menganggur sebagai cache. Jadi bar ini tidak pernah jadi warna
    // pekat: ia informasi, bukan peringatan.
    statCard('arc zfs', bytes(h.arc_size_bytes), '',
      `${arcPct}% dari batas · hit ${h.arc_hit_ratio.toFixed(1)}%`,
      bar(arcPct, 'ok', pastelFor('arc'))));
}

// zpool status sudah menulis "scrub repaired ..." pada baris scan:, jadi
// label "scrub:" di kartu akan menghasilkan "scrub: scrub repaired ...".
function scrubText(line) {
  if (!line) return 'belum pernah';
  if (line === 'none requested') return 'belum pernah';
  return line.replace(/^scrub\s+/, '');
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

      el('div', { class: 'spark', title: 'riwayat menyusul' })));
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

      el('div', { class: 'spark', title: 'riwayat menyusul' })));
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
    for (const p of c.published_ports || []) {
      const terbuka = p.startsWith('0.0.0.0:') || p.startsWith(':::');
      tail.push(el('span', { class: 'n-pill ' + (terbuka ? 'n-pill-warn' : '') }, p));
    }

    rows.append(el('div', { class: 'row' },
      statusDot(lvl),
      el('div', { class: 'rname' }, c.name),
      el('div', { class: 'rmeta' }, `${c.image} · ${c.status}`),
      el('div', { class: 'rtail' }, tail)));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

function renderDatasets(ds) {
  const box = $('datasets');
  clear(box);
  if (!ds || !ds.length) {
    box.append(el('p', { class: 'note' }, 'Tidak ada dataset terbaca.'));
    return;
  }

  const rows = el('div', { class: 'rows' });
  for (const d of ds) {
    // Dataset tanpa snapshot bukan otomatis salah — ada yang memang tidak
    // perlu. Karena itu ditandai redup, bukan dijadikan temuan.
    const tanpaSnap = d.snapshot_count === 0;
    rows.append(el('div', { class: 'row' },
      el('span', { class: 'n-dot ' + (tanpaSnap ? 'n-dot-off' : 'n-dot-ok') }),
      el('div', { class: 'rname' }, d.name),
      el('div', { class: 'rmeta' }, d.mountpoint || '—'),
      el('div', { class: 'rtail' },
        el('span', { class: 'n-pill' }, bytes(d.used_bytes)),
        el('span', { class: 'n-pill' }, `${d.snapshot_count} snap`))));
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

/* ---------- tema ---------- */
function initTheme() {
  const saved = localStorage.getItem('issboard-theme');
  if (saved) document.documentElement.setAttribute('data-theme', saved);

  $('theme').addEventListener('click', () => {
    const kini = document.documentElement.getAttribute('data-theme');
    const sistemGelap = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const berikut = (kini || (sistemGelap ? 'dark' : 'light')) === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', berikut);
    localStorage.setItem('issboard-theme', berikut);
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

initTheme();
tick();
setInterval(tick, REFRESH_MS);

// Kembali terlihat: segarkan segera, jangan tunggu giliran interval berikutnya.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') tick();
});
