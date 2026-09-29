/* issboard — panel "kelola": aksi container, scrub, SMART self-test, batas ARC,
 * dan jadwal yang berjalan sendiri.
 *
 * Memakai helper dari app.js (el, $, clear, bytes, relTime, LAST, tick).
 *
 * Keputusan yang membentuk berkas ini:
 *  - Tidak ada aksi tanpa konfirmasi, dan konfirmasinya menyebut AKIBATNYA,
 *    bukan cuma "yakin?". Menghapus container harus mengetik namanya.
 *  - Bagian ber-input (login, ARC) statis di HTML. Daftar lain digambar ulang
 *    tiap 15 detik bersama halaman; kalau form ikut digambar ulang, isian yang
 *    sedang diketik akan hilang di tengah jalan.
 *  - Vonis tetap di server. Panel ini hanya menawarkan tombol yang cocok dengan
 *    keadaan yang dilaporkan server.
 */
'use strict';

let SESSION = { configured: false, authenticated: false };
let MANAGE = null;         // isi /api/v1/manage
const BUSY = new Set();    // aksi yang sedang berjalan, supaya tombolnya tidak ditekan dua kali

const GIB = 1024 ** 3;

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

function panelOpen() { return !$('panel').hidden; }

/* ---------- sesi ---------- */
async function loadSession() {
  try {
    const r = await fetch('api/v1/session', { cache: 'no-store' });
    SESSION = await r.json();
  } catch (e) {
    SESSION = { configured: false, authenticated: false, error: e.message };
  }
  renderAuth();
}

function renderAuth() {
  const s = SESSION;
  const note = $('auth-note');
  $('login-form').hidden = !(s.configured && !s.authenticated);
  $('logout').hidden = !s.authenticated;
  $('who').textContent = s.authenticated ? s.user : '';

  if (s.error) note.textContent = 'Berkas kredensial bermasalah: ' + s.error;
  else if (!s.configured) {
    note.textContent = 'Aksi mati: kata sandi operator belum diatur. Di host, jalankan ' +
      '`sudo issboard -set-password`. Bagian baca di bawah tetap bisa dilihat.';
  } else if (s.authenticated) {
    note.textContent = `Masuk sebagai ${s.user}` +
      (s.expires ? ` sampai ${new Date(s.expires).toLocaleString('id-ID')}` : '') +
      '. Setiap aksi dicatat di journal host.';
  } else {
    note.textContent = s.demo ? 'Mode demo: masuk dengan demo / demo. Aksinya pura-pura.'
      : 'Masuk untuk menjalankan aksi.';
  }
  if (LAST) renderManage(LAST);
}

async function login(ev) {
  ev.preventDefault();
  const btn = ev.submitter;
  if (btn) btn.disabled = true;
  try {
    const r = await fetch('api/v1/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user: $('login-user').value, password: $('login-pw').value }),
    });
    const b = await r.json();
    $('login-pw').value = '';
    if (!b.ok) { showLog(false, b.error || ('HTTP ' + r.status)); return; }
    showLog(true, 'berhasil masuk');
    await loadSession();
  } catch (e) {
    showLog(false, e.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function logout() {
  await fetch('api/v1/logout', { method: 'POST' }).catch(() => {});
  await loadSession();
  showLog(true, 'keluar');
}

/* ---------- info ---------- */
async function loadManage() {
  try {
    const r = await fetch('api/v1/manage', { cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    MANAGE = await r.json();
  } catch (e) {
    MANAGE = { error: e.message };
  }
  if (LAST) renderManage(LAST);
}

/* ---------- konfirmasi ----------
   <dialog> bawaan, bukan confirm(): confirm() tidak bisa memuat kolom ketik-
   ulang nama, dan di beberapa browser HP tampilannya memotong teks panjang —
   padahal teks itulah yang menjelaskan akibatnya. */
function confirmDialog({ title, text, typeName, okLabel, danger }) {
  return new Promise((resolve) => {
    const d = $('confirm');
    $('confirm-title').textContent = title;
    $('confirm-text').textContent = text;
    const wrap = $('confirm-type-wrap');
    const inp = $('confirm-input');
    const ok = $('confirm-ok');
    ok.textContent = okLabel || 'lanjut';
    ok.className = 'n-btn' + (danger ? ' n-btn-danger' : '');
    wrap.hidden = !typeName;
    inp.value = '';
    $('confirm-name').textContent = typeName || '';
    ok.disabled = !!typeName;
    inp.oninput = () => { ok.disabled = inp.value !== typeName; };
    d.onclose = () => resolve(d.returnValue === 'ok');
    d.returnValue = '';
    d.showModal();
    (typeName ? inp : ok).focus();
  });
}

function showLog(ok, text) {
  const p = $('panel-log');
  p.hidden = false;
  p.className = 'panel-log ' + (ok ? 'ok' : 'err');
  p.textContent = (ok ? '✓ ' : '✕ ') + text;
}

/* Satu jalan untuk semua aksi: konfirmasi → POST ber-CSRF → catat → segarkan. */
async function act(key, path, body, confirmOpts) {
  if (BUSY.has(key)) return false;
  if (confirmOpts && !(await confirmDialog(confirmOpts))) return false;
  BUSY.add(key);
  if (LAST) renderManage(LAST);
  let ok = false;
  try {
    const r = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': SESSION.csrf || '' },
      body: body ? JSON.stringify(body) : '',
    });
    const b = await r.json().catch(() => ({ ok: false, error: 'HTTP ' + r.status }));
    if (r.status === 401) await loadSession();
    const out = (b.output || '').trim();
    ok = !!b.ok;
    showLog(ok, ok ? `${key}: ${out || 'selesai'}` : `${key}: ${b.error}${out ? ' — ' + out : ''}`);
  } catch (e) {
    showLog(false, `${key}: ${e.message}`);
  } finally {
    BUSY.delete(key);
    await Promise.all([tick(), loadManage(), loadDataset()]);
  }
  return ok;
}

// Tombol aksi. `root` = lewat helper, jadi ikut mati kalau helper tidak ada.
function actBtn(label, key, onClick, { danger, root } = {}) {
  let why = '';
  if (!SESSION.authenticated) why = 'masuk dulu';
  else if (root && MANAGE && MANAGE.helper === false) why = 'issboard-helper.socket belum aktif';
  const b = el('button', {
    class: 'n-btn n-btn-sm' + (danger ? ' n-btn-danger' : ''),
    type: 'button',
    disabled: !!why || BUSY.has(key),
    title: why || null,
  }, BUSY.has(key) ? '…' : label);
  b.addEventListener('click', onClick);
  return b;
}

/* ---------- gambar ---------- */
function renderManage(d) {
  if (!panelOpen()) return;
  renderARC();
  renderDatasets(d.datasets || []);
  renderSanoid(d);
  renderMPools(d.pools || []);
  renderMDisks((d.smart && d.smart.disks) || []);
  renderMContainers(d.containers || []);
  renderSchedule();
}

function renderARC() {
  const box = $('arc-info');
  clear(box);
  const a = MANAGE && MANAGE.arc;
  const form = $('arc-form');
  if (!a || !a.present) {
    box.append(el('p', { class: 'note' }, MANAGE ? 'ZFS tidak termuat di host ini.' : 'memuat…'));
    form.hidden = true;
    return;
  }
  form.hidden = false;

  const persisted = a.persisted || [];
  const kv = el('dl', { class: 'kv' },
    el('dt', {}, 'terpakai'), el('dd', {}, `${bytes(a.size_bytes)} dari batas ${bytes(a.c_max)}`),
    el('dt', {}, 'parameter'), el('dd', {}, a.param ? bytes(a.param) : '0 (bawaan ZFS)'),
    el('dt', {}, 'setelah reboot'), el('dd', {}, persisted.length
      ? persisted.map((p) => `${bytes(p.value)} — ${p.file}`).join(' · ')
      : 'bawaan ZFS (tidak diatur di modprobe.d)'),
    el('dt', {}, 'rentang'), el('dd', {}, `${bytes(a.lower)} – ${bytes(a.upper)} (RAM ${bytes(a.mem_total)})`));
  box.append(kv);

  // Parameter yang diminta tapi tidak dipakai ZFS: ZFS diam-diam mengabaikan
  // nilai di luar batasnya, jadi bedanya harus ditulis, bukan ditebak.
  if (a.param && a.param !== a.c_max) {
    box.append(el('p', { class: 'note warn' },
      `zfs_arc_max ${bytes(a.param)} tapi batas yang berlaku ${bytes(a.c_max)} — ZFS tidak memakai nilai itu.`));
  }
  if (persisted.length > 1) {
    box.append(el('p', { class: 'note warn' },
      'zfs_arc_max diatur di lebih dari satu berkas; yang terakhir menurut abjad yang menang.'));
  }
  if (persisted.length && !persisted.some((p) => p.ours)) {
    box.append(el('p', { class: 'note' },
      `Nilai permanen diatur di ${persisted[0].file}. issboard tidak menyunting berkas itu — ` +
      '"simpan permanen" akan ditolak sampai baris di sana dihapus.'));
  }

  const inp = $('arc-gib');
  inp.min = (a.lower / GIB).toFixed(1);
  inp.max = (a.upper / GIB).toFixed(1);
  // Diisi sekali, dan tidak pernah saat sedang diketik.
  if (!inp.value && document.activeElement !== inp) inp.value = ((a.param || a.c_max) / GIB).toFixed(1);

  const authOK = SESSION.authenticated && MANAGE.helper !== false;
  for (const b of form.querySelectorAll('button')) {
    b.disabled = !authOK || BUSY.has('arc');
    b.title = SESSION.authenticated ? (MANAGE.helper === false ? 'issboard-helper.socket belum aktif' : '') : 'masuk dulu';
  }
}

function submitARC(ev, reset) {
  if (ev) ev.preventDefault();
  const persist = $('arc-persist').checked;
  const v = reset ? 0 : Math.round(parseFloat($('arc-gib').value) * GIB);
  if (!reset && !(v > 0)) { showLog(false, 'isi batas ARC dalam GiB'); return; }
  const a = MANAGE.arc;
  const kata = reset ? 'kembali ke bawaan ZFS' : `batas ARC jadi ${bytes(v)}`;
  act('arc', 'api/v1/arc', { max_bytes: v, persist }, {
    title: 'Ubah batas ARC',
    text: `Atur ${kata}. Terpakai sekarang ${bytes(a.size_bytes)}. ` +
      (v && v < a.size_bytes ? 'ARC akan menyusut bertahap, dan bacaan dari disk akan bertambah. ' : '') +
      (persist ? `Nilai juga ditulis ke /etc/modprobe.d supaya berlaku setelah reboot. Kalau root ` +
        `filesystem ada di ZFS, perlu \`update-initramfs -u\` juga.` : 'Hanya berlaku sampai reboot.'),
    okLabel: 'terapkan',
  });
}

function scrubState(line) {
  const s = (line || '').toLowerCase();
  if (s.includes('paused')) return 'paused';
  if (s.includes('in progress') && s.includes('scrub')) return 'running';
  return 'idle';
}

function renderMPools(pools) {
  const box = $('m-pools');
  clear(box);
  if (!pools.length) { box.append(el('p', { class: 'note' }, 'Tidak ada pool.')); return; }
  const rows = el('div', { class: 'mrows' });
  for (const p of pools) {
    const st = scrubState(p.scan_line);
    const k = (op) => `scrub.${op} ${p.name}`;
    const url = (op) => `api/v1/pools/${encodeURIComponent(p.name)}/scrub/${op}`;
    const btns = [];
    if (st === 'running') {
      btns.push(actBtn('jeda', k('pause'), () => act(k('pause'), url('pause'), null), { root: true }));
      btns.push(actBtn('hentikan', k('stop'), () => act(k('stop'), url('stop'), null, {
        title: `Hentikan scrub ${p.name}`,
        text: 'Scrub yang dihentikan TIDAK bisa dilanjutkan — mulai lagi berarti dari awal. Pakai "jeda" kalau hanya ingin menunda.',
        okLabel: 'hentikan',
      }), { root: true }));
    } else {
      btns.push(actBtn(st === 'paused' ? 'lanjutkan' : 'mulai scrub', k('start'), () => act(k('start'), url('start'), null,
        st === 'paused' ? null : {
          title: `Scrub ${p.name}`,
          text: `Scrub membaca seluruh ${bytes(p.alloc_bytes)} data di pool ini dan memverifikasi checksum-nya. ` +
            'Bisa berjam-jam dan membebani disk selama berjalan. ' +
            (p.mirrored ? 'Pool ini redundan: blok yang rusak akan diperbaiki.'
              : 'Pool ini TIDAK redundan: scrub hanya bisa melaporkan kerusakan, tidak memperbaikinya.'),
          okLabel: 'mulai',
        }), { root: true }));
      if (st === 'paused') {
        btns.push(actBtn('hentikan', k('stop'), () => act(k('stop'), url('stop'), null), { root: true }));
      }
    }
    rows.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname' }, p.name),
      el('div', { class: 'mmeta' }, 'scrub: ' + scrubText(p.scan_line)),
      el('div', { class: 'mbtns' }, btns)));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

function renderMDisks(disks) {
  const box = $('m-disks');
  clear(box);
  const head = el('div', { class: 'mbtns mtop' },
    actBtn('segarkan cache smart', 'smart.refresh', () => act('smart.refresh', 'api/v1/smart/refresh', null), { root: true }));
  box.append(el('p', { class: 'note' },
    'Hasil tes baru terlihat setelah tesnya selesai DAN cache SMART diperbarui — tekan "segarkan" ' +
    'setelah kira-kira 2 menit (tes singkat) atau beberapa jam (tes panjang).'), head);
  if (!disks.length) { box.append(el('p', { class: 'note' }, 'Belum ada disk di cache SMART.')); return; }

  const rows = el('div', { class: 'mrows' });
  for (const d of disks) {
    const k = (op) => `smart.${op} ${d.device}`;
    const go = (op, confirmOpts) => act(k(op), `api/v1/smart/${op}`, { device: d.device }, confirmOpts);
    const bangun = d.standby ? 'Disk ini SEDANG TIDUR dan akan dibangunkan. ' : 'Disk yang tidur akan dibangunkan. ';
    rows.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname' }, d.device),
      el('div', { class: 'mmeta' }, `${d.model || 'model tak dikenal'} · ${d.standby ? 'tidur' : 'aktif'} · terakhir: ${d.last_self_test || 'belum pernah'}`),
      el('div', { class: 'mbtns' },
        actBtn('tes singkat', k('short'), () => go('short', {
          title: `Tes singkat ${d.device}`,
          text: bangun + 'Tes singkat butuh sekitar 2 menit dan berjalan di dalam disk; disk tetap bisa dipakai.',
          okLabel: 'mulai tes',
        }), { root: true }),
        actBtn('tes panjang', k('long'), () => go('long', {
          title: `Tes panjang ${d.device}`,
          text: bangun + 'Tes panjang membaca seluruh permukaan disk — berjam-jam, dan disk tidak akan tidur selama itu. ' +
            'Kinerja disk turun selama tes berjalan.',
          okLabel: 'mulai tes',
        }), { root: true }),
        actBtn('batalkan', k('abort'), () => go('abort'), { root: true }))));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

function renderMContainers(cs) {
  const box = $('m-containers');
  clear(box);
  if (!cs.length) { box.append(el('p', { class: 'note' }, 'Tidak ada container terbaca.')); return; }
  const rows = el('div', { class: 'mrows' });
  for (const c of cs) {
    const state = (c.state || '').toLowerCase();
    const running = state === 'running' || state === 'restarting' || state === 'paused';
    const k = (op) => `container.${op} ${c.name}`;
    const url = (op) => `api/v1/containers/${encodeURIComponent(c.name)}/${op}`;
    const btns = [];
    if (running) {
      btns.push(actBtn('restart', k('restart'), () => act(k('restart'), url('restart'), null, {
        title: `Restart ${c.name}`,
        text: 'Container dihentikan (tunggu sampai 10 detik) lalu dinyalakan lagi. Layanan di dalamnya putus sebentar.',
        okLabel: 'restart',
      })));
      btns.push(actBtn('stop', k('stop'), () => act(k('stop'), url('stop'), null, {
        title: `Hentikan ${c.name}`,
        text: 'Layanan di dalamnya berhenti sampai dinyalakan lagi. Kebijakan restart Docker tidak akan menyalakannya sendiri.',
        okLabel: 'stop',
      })));
    } else {
      btns.push(actBtn('start', k('start'), () => act(k('start'), url('start'), null)));
      btns.push(actBtn('hapus', k('remove'), () => act(k('remove'), url('remove'), null, {
        title: `Hapus ${c.name}`,
        text: `Container ${c.name} (${c.image}) dihapus. Volume dan image TIDAK ikut dihapus, ` +
          'tapi apa pun yang ditulis di dalam container di luar volume hilang, dan tidak ada undo. ' +
          'Kalau ia dibuat lewat compose, `docker compose up -d` akan membuatnya lagi.',
        typeName: c.name,
        okLabel: 'hapus',
        danger: true,
      }), { danger: true }));
    }
    rows.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname' }, statusDotFor(state), c.name),
      el('div', { class: 'mmeta' }, c.status || state),
      el('div', { class: 'mbtns' }, btns)));
  }
  box.append(el('div', { class: 'n-card' }, rows));
}

function statusDotFor(state) {
  const cls = state === 'running' ? 'n-dot-ok' : state === 'exited' || state === 'dead' ? 'n-dot-off' : 'n-dot-warn';
  return el('span', { class: 'n-dot ' + cls });
}

function renderSchedule() {
  const box = $('m-schedule');
  clear(box);
  if (!MANAGE) { box.append(el('p', { class: 'note' }, 'memuat…')); return; }
  if (MANAGE.error) { box.append(el('p', { class: 'note' }, 'gagal memuat: ' + MANAGE.error)); return; }
  const s = MANAGE.schedule || {};

  for (const n of s.notes || []) box.append(el('p', { class: 'note warn' }, n));
  for (const e of s.errors || []) box.append(el('p', { class: 'note' }, e));

  const card = el('div', { class: 'n-card' });
  const timers = s.timers || [];
  card.append(el('h4', { class: 'n-label' }, 'timer systemd'));
  if (!timers.length) card.append(el('p', { class: 'note' }, 'Tidak ada timer terkait penyimpanan.'));
  const tr = el('div', { class: 'mrows' });
  for (const t of timers) {
    tr.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname' }, t.unit),
      el('div', { class: 'mmeta' },
        `berikut ${t.next ? relWhen(t.next) : '—'} · terakhir ${t.last ? relTime(t.last) : 'belum pernah'}`)));
  }
  card.append(tr);
  if (s.other_timers) card.append(el('p', { class: 'note' }, `+ ${s.other_timers} timer lain yang tidak menyangkut penyimpanan.`));

  card.append(el('h4', { class: 'n-label' }, 'cron'));
  const cron = s.cron || [];
  if (!cron.length) card.append(el('p', { class: 'note' }, 'Tidak ada baris cron terkait ZFS/SMART.'));
  const cr = el('div', { class: 'mrows' });
  for (const c of cron) {
    cr.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname mono' }, c.schedule),
      el('div', { class: 'mmeta mono' }, c.command),
      el('div', { class: 'mmeta' }, `${c.file}${c.user ? ' · ' + c.user : ''}`)));
  }
  card.append(cr);

  card.append(el('h4', { class: 'n-label' }, 'smartd'));
  if (!s.smartd) card.append(el('p', { class: 'note' }, 'smartd.conf tidak ditemukan.'));
  else {
    card.append(el('p', { class: 'note' }, s.smartd.file +
      (s.smartd.self_tests ? ' — menjadwalkan self-test sendiri (-s)' : '')));
    for (const l of s.smartd.lines || []) card.append(el('div', { class: 'mmeta mono' }, l));
  }
  box.append(card);
}

/* ---------- dataset & snapshot ----------
   Detailnya (properti + snapshot) diambil saat dataset dipilih dan sesudah
   aksi, BUKAN tiap 15 detik: dataset dengan ribuan snapshot membuat daftar itu
   mahal, dan ia tidak berubah sendiri secepat itu. */
let DS = null;             // isi /api/v1/dataset untuk dataset terpilih
const SNAP_MAX = 30;

function selectedDataset() { return $('ds-select').value; }

async function loadDataset() {
  const name = selectedDataset();
  if (!name) { DS = null; return; }
  try {
    const r = await fetch('api/v1/dataset?name=' + encodeURIComponent(name), { cache: 'no-store' });
    DS = await r.json();
    if (!r.ok) DS = { name, error: DS.error || ('HTTP ' + r.status) };
  } catch (e) {
    DS = { name, error: e.message };
  }
  fillPropForm();
  if (LAST) renderManage(LAST);
}

// Tombol di form statis ikut aturan yang sama dengan actBtn.
function gateForm(form, key) {
  let why = '';
  if (!SESSION.authenticated) why = 'masuk dulu';
  else if (MANAGE && MANAGE.helper === false) why = 'issboard-helper.socket belum aktif';
  for (const b of form.querySelectorAll('button')) {
    b.disabled = !!why || BUSY.has(key);
    b.title = why;
  }
}

function renderDatasets(ds) {
  const sel = $('ds-select');
  const names = ds.map((d) => d.name);
  // Opsi hanya dibangun ulang kalau daftarnya berubah: membangun ulang tiap
  // 15 detik akan menutup pilihan yang sedang dibuka di HP.
  if (sel.dataset.names !== names.join('\n')) {
    const cur = sel.value;
    clear(sel);
    for (const n of names) sel.append(el('option', { value: n }, n));
    sel.dataset.names = names.join('\n');
    if (names.includes(cur)) sel.value = cur;
    if (sel.value !== cur || !DS) loadDataset();
  }
  for (const f of ['snap-form', 'prop-form', 'child-form']) gateForm($(f), f);

  const box = $('ds-detail');
  clear(box);
  if (!ds.length) { box.append(el('p', { class: 'note' }, 'Tidak ada dataset.')); return; }
  if (!DS || DS.name !== sel.value) { box.append(el('p', { class: 'note' }, 'memuat…')); return; }
  if (DS.error) { box.append(el('p', { class: 'note' }, 'gagal memuat: ' + DS.error)); return; }
  for (const e of DS.errors || []) box.append(el('p', { class: 'note' }, e));

  const kv = el('dl', { class: 'kv' });
  for (const p of DS.props || []) {
    const warisan = p.source && p.source !== 'local';
    kv.append(el('dt', {}, p.name), el('dd', {}, p.value,
      warisan ? el('span', { class: 'mmeta' }, ` · ${p.source.replace('inherited from', 'warisan')}`) : null));
  }
  box.append(el('details', { class: 'sysinfo' }, el('summary', { class: 'n-label' }, 'properti'), kv));

  const snaps = DS.snapshots || [];
  box.append(el('h4', { class: 'n-label' }, `snapshot (${snaps.length})`));
  if (!snaps.length) box.append(el('p', { class: 'note' }, 'Belum ada snapshot.'));
  const rows = el('div', { class: 'mrows' });
  for (const sn of snaps.slice(0, SNAP_MAX)) {
    const label = sn.name.slice(sn.name.indexOf('@') + 1);
    const sanoid = label.startsWith('autosnap_');
    const manual = label.startsWith('issboard_');
    const k = 'snapshot.destroy ' + sn.name;
    rows.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname mono' }, label,
        el('span', { class: 'n-pill' }, sanoid ? 'sanoid' : manual ? 'manual' : 'lain')),
      el('div', { class: 'mmeta' }, `${relTime(sn.created)} · unik ${bytes(sn.used_bytes)} · merujuk ${bytes(sn.referenced_bytes)}`),
      el('div', { class: 'mbtns' }, actBtn('hapus', k, () => act(k, 'api/v1/snapshots/destroy', { snapshot: sn.name }, {
        title: 'Hapus snapshot',
        text: `${sn.name} dihapus, membebaskan sekitar ${bytes(sn.used_bytes)}. Titik pulih ini hilang dan tidak ada undo. ` +
          (sanoid ? 'Ini buatan sanoid — sanoid akan membuat yang baru sesuai jadwal, tapi yang ini tidak kembali.'
            : 'Snapshot yang bukan buatan sanoid TIDAK dipangkas otomatis; menghapusnya memang harus manual.'),
        typeName: label,
        okLabel: 'hapus',
        danger: true,
      }), { danger: true, root: true }))));
  }
  box.append(rows);
  if (snaps.length > SNAP_MAX) {
    box.append(el('p', { class: 'note' }, `+ ${snaps.length - SNAP_MAX} snapshot lebih lama tidak ditampilkan.`));
  }
}

function fillPropForm() {
  const specs = (DS && DS.specs) || [];
  const sel = $('prop-name');
  if (!sel.options.length && specs.length) {
    for (const sp of specs) sel.append(el('option', { value: sp.name }, sp.name));
    const comp = specs.find((x) => x.name === 'compression');
    for (const v of (comp ? comp.values : [])) $('child-comp').append(el('option', { value: v }, v));
  }
  const sp = specs.find((x) => x.name === sel.value);
  if (!sp) return;
  $('prop-help').textContent = sp.help;
  $('prop-size-wrap').hidden = !sp.size;
  $('prop-choice-wrap').hidden = !!sp.size;
  const cur = ((DS.props || []).find((p) => p.name === sp.name) || {}).value;
  if (sp.size) {
    $('prop-size').value = cur || '';
  } else {
    const ch = $('prop-choice');
    clear(ch);
    for (const v of [...(sp.inherit ? ['inherit'] : []), ...sp.values]) {
      ch.append(el('option', { value: v }, v === cur ? v + ' (sekarang)' : v));
    }
    if (cur) ch.value = cur;
  }
}

function submitSnap(ev) {
  ev.preventDefault();
  const ds = selectedDataset();
  const tag = $('snap-tag').value.trim();
  const rec = $('snap-rec').checked;
  act('snap-form', 'api/v1/snapshots', { dataset: ds, tag, recursive: rec }, {
    title: `Snapshot ${ds}`,
    text: `Membuat snapshot ${ds}${rec ? ' beserta semua anaknya' : ''}. Awalnya tidak memakan ruang; ` +
      'ruangnya tumbuh seiring data berubah. Snapshot manual TIDAK dipangkas sanoid — hapus sendiri kalau sudah tidak perlu.',
    okLabel: 'buat',
  }).then((ok) => { if (ok) $('snap-tag').value = ''; });
}

function submitProp(ev) {
  ev.preventDefault();
  const ds = selectedDataset();
  const prop = $('prop-name').value;
  const sp = DS && DS.specs && DS.specs.find((x) => x.name === prop);
  if (!sp) return;
  const value = sp.size ? $('prop-size').value.trim() : $('prop-choice').value;
  act('prop-form', 'api/v1/datasets/props', { dataset: ds, prop, value }, {
    title: `Ubah ${prop}`,
    text: `${ds}: ${prop} = ${value}. ${sp.help}.` +
      (prop.includes('quota') && value !== 'none' ? ' Kalau batasnya di bawah pemakaian sekarang, tulisan baru akan GAGAL.' : '') +
      (prop === 'readonly' && value === 'on' ? ' Container yang menulis ke dataset ini akan mulai error.' : ''),
    okLabel: 'terapkan',
  });
}

function submitChild(ev) {
  ev.preventDefault();
  const parent = selectedDataset();
  const name = $('child-name').value.trim();
  const props = {};
  if ($('child-comp').value) props.compression = $('child-comp').value;
  if ($('child-quota').value.trim()) props.quota = $('child-quota').value.trim();
  act('child-form', 'api/v1/datasets', { parent, name, props }, {
    title: 'Buat dataset',
    text: `Membuat ${parent}/${name}` +
      (Object.keys(props).length ? ` (${Object.entries(props).map(([k, v]) => k + '=' + v).join(', ')})` : '') +
      '. Dataset baru TIDAK otomatis masuk kebijakan snapshot — lihat bagian sanoid di bawah sesudahnya.',
    okLabel: 'buat',
  }).then((ok) => { if (ok) { $('child-name').value = ''; $('child-quota').value = ''; } });
}

/* ---------- sanoid ----------
   issboard tidak menulis sanoid.conf — berkas milik program lain. Yang
   ditawarkan adalah potongan yang siap ditempel untuk dataset yang oleh server
   dinilai tidak tercakup (temuan snap.uncovered.*), jadi keputusannya tetap
   dari internal/health, bukan aturan kedua di sini. */
function renderSanoid(d) {
  const box = $('m-sanoid');
  clear(box);
  const pol = d.snap_policy || {};
  const timer = ((MANAGE && MANAGE.schedule && MANAGE.schedule.timers) || []).find((t) => t.unit.startsWith('sanoid'));

  const card = el('div', { class: 'n-card' });
  card.append(el('p', { class: 'note' }, pol.present
    ? `Kebijakan dibaca dari ${pol.source}` + (pol.templates && pol.templates.length ? ` · template: ${pol.templates.join(', ')}` : '')
    : 'Tidak ada berkas kebijakan sanoid — perbandingan cakupan mati.'));
  if (timer) {
    card.append(el('p', { class: 'mmeta' },
      `${timer.unit}: terakhir ${timer.last ? relTime(timer.last) : 'belum pernah'} · berikut ${timer.next ? relWhen(timer.next) : '—'}`));
  }
  card.append(el('div', { class: 'mbtns mtop' }, actBtn('jalankan sanoid sekarang', 'sanoid.run',
    () => act('sanoid.run', 'api/v1/sanoid/run', null, {
      title: 'Jalankan sanoid',
      text: 'Menyalakan sanoid.service sekarang — persis yang dijalankan timernya: membuat snapshot yang jatuh tempo ' +
        'dan memangkas yang kedaluwarsa menurut sanoid.conf.',
      okLabel: 'jalankan',
    }), { root: true })));

  const uncovered = (d.findings || []).filter((f) => (f.key || '').startsWith('snap.uncovered.')).map((f) => f.subject);
  if (pol.present && uncovered.length) {
    const tpl = (pol.templates && pol.templates[0]) || 'ISI_TEMPLATE';
    const text = uncovered.map((n) => `[${n}]\n\tuse_template = ${tpl}\n`).join('\n');
    const pre = el('pre', { class: 'snippet' }, text);
    const copy = el('button', { class: 'n-btn n-btn-sm', type: 'button' }, 'salin');
    copy.addEventListener('click', () => copyText(pre, copy));
    card.append(
      el('h4', { class: 'n-label' }, `tidak tercakup (${uncovered.length})`),
      el('p', { class: 'note' }, `Tempel ke ${pol.source}, ganti template kalau perlu, lalu jalankan sanoid. ` +
        'issboard sengaja tidak menulis berkas milik sanoid. Kalau memang tidak perlu snapshot, daftarkan di snapshot_exempt.'),
      pre, el('div', { class: 'mbtns' }, copy));
  }
  box.append(card);
}

// Clipboard API hanya ada di secure context, dan tailnet melayani HTTP polos.
// Cadangannya memilih teksnya, supaya cukup satu ketuk "salin" dari sistem.
function copyText(pre, btn) {
  const done = () => { btn.textContent = 'tersalin'; setTimeout(() => { btn.textContent = 'salin'; }, 1500); };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(pre.textContent).then(done, () => selectText(pre));
  } else {
    selectText(pre);
    btn.textContent = 'teks terpilih — salin manual';
  }
}

function selectText(node) {
  const r = document.createRange();
  r.selectNodeContents(node);
  const s = window.getSelection();
  s.removeAllRanges();
  s.addRange(r);
}

/* ---------- buka/tutup ---------- */
function openPanel() {
  $('panel').hidden = false;
  $('panel-backdrop').hidden = false;
  document.body.classList.add('panel-open');
  loadSession();
  loadManage();
  if (LAST) renderManage(LAST);
}

function closePanel() {
  $('panel').hidden = true;
  $('panel-backdrop').hidden = true;
  document.body.classList.remove('panel-open');
}

function initManage() {
  $('manage').addEventListener('click', () => (panelOpen() ? closePanel() : openPanel()));
  $('panel-close').addEventListener('click', closePanel);
  $('panel-backdrop').addEventListener('click', closePanel);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && panelOpen() && !$('confirm').open) closePanel();
  });
  $('login-form').addEventListener('submit', login);
  $('logout').addEventListener('click', logout);
  $('arc-form').addEventListener('submit', (e) => submitARC(e, false));
  $('arc-default').addEventListener('click', () => submitARC(null, true));
  $('ds-select').addEventListener('change', () => { DS = null; loadDataset(); });
  $('prop-name').addEventListener('change', fillPropForm);
  $('snap-form').addEventListener('submit', submitSnap);
  $('prop-form').addEventListener('submit', submitProp);
  $('child-form').addEventListener('submit', submitChild);
  // Jadwal jarang berubah; cukup disegarkan saat panelnya terbuka.
  setInterval(() => { if (panelOpen() && !document.hidden) loadManage(); }, 30000);
  loadSession();
  // #kelola membuka panel langsung — bisa di-bookmark di HP.
  if (location.hash === '#kelola') openPanel();
}

initManage();
