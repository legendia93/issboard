/* issboard — helper bersama dashboard (index.html) dan halaman kelola
 * (admin.html). Dimuat SEBELUM app.js / admin.js.
 *
 * Vanilla, tanpa build step, tanpa aset pihak ketiga: kedua halaman disajikan
 * dengan Content-Security-Policy default-src 'self'.
 */
'use strict';

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

// Waktu ke depan ("dalam 5 mnt") — jadwal timer. Waktu lampau jatuh ke relTime.
function relWhen(iso) {
  const t = new Date(iso).getTime();
  if (!t) return '—';
  const s = Math.round((t - Date.now()) / 1000);
  if (s <= 0) return relTime(iso);
  if (s < 60) return `dalam ${s} dtk`;
  if (s < 3600) return `dalam ${Math.round(s / 60)} mnt`;
  if (s < 86400) return `dalam ${Math.round(s / 3600)} jam`;
  return `dalam ${Math.round(s / 86400)} hari`;
}

function statusDot(level) {
  return el('span', { class: 'n-dot n-dot-' + (level || 'ok') });
}

// zpool status sudah menulis "scrub repaired ..." pada baris scan:, jadi
// label "scrub:" di kartu akan menghasilkan "scrub: scrub repaired ...".
function scrubText(line) {
  if (!line) return 'belum pernah';
  if (line === 'none requested') return 'belum pernah';
  return line.replace(/^scrub\s+/, '');
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
