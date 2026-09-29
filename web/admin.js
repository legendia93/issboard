/* issboard — halaman kelola (admin.html).
 *
 * Halaman sendiri, bukan laci di atas dashboard. Dashboard tetap halaman baca
 * yang aman dibuka sambil lewat; semua tombol yang mengubah host tinggal di
 * sini, di balik layar masuk.
 *
 * Memakai helper dari common.js (el, $, clear, bytes, relTime, relWhen,
 * scrubText, statusDot, initTheme).
 *
 * Keputusan yang membentuk berkas ini:
 *  - Tidak ada aksi tanpa konfirmasi, dan konfirmasinya menyebut AKIBATNYA,
 *    bukan cuma "yakin?". Yang tak bisa dibatalkan harus mengetik namanya.
 *  - Bagian ber-input (form) statis di HTML. Daftar lain digambar ulang tiap
 *    15 detik; form yang ikut digambar ulang akan menghapus isian yang sedang
 *    diketik.
 *  - Vonis tetap di server. Halaman ini hanya menawarkan tombol yang cocok
 *    dengan keadaan yang dilaporkan server.
 */
'use strict';

const REFRESH_MS = 15000;
const GIB = 1024 ** 3;
const SNAP_MAX = 50;

let LAST = null;           // /api/v1/status
let SESSION = { configured: false, authenticated: false };
let MANAGE = null;         // /api/v1/manage
let DS = null;             // /api/v1/dataset untuk dataset terpilih
const BUSY = new Set();    // aksi yang sedang berjalan
const HISTORY = [];        // hasil aksi sesi ini, terbaru dulu

/* ---------- muat ---------- */
async function tick() {
  if (document.hidden) return;
  try {
    const r = await fetch('api/v1/status', { cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    LAST = await r.json();
    $('host-pill').textContent = (LAST.host && LAST.host.hostname) || '—';
    $('host-pill').hidden = false;
    $('demo-pill').hidden = !LAST.demo;
    $('updated').textContent = 'diperbarui ' + relTime(LAST.collected_at);
    render();
  } catch (e) {
    $('updated').textContent = 'gagal memuat: ' + e.message;
  }
}

async function loadManage() {
  try {
    const r = await fetch('api/v1/manage', { cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    MANAGE = await r.json();
  } catch (e) {
    MANAGE = { error: e.message };
  }
  render();
}

async function loadSession() {
  try {
    const r = await fetch('api/v1/session', { cache: 'no-store' });
    SESSION = await r.json();
  } catch (e) {
    SESSION = { configured: false, authenticated: false, error: e.message };
  }
  renderGate();
}

/* ---------- layar masuk ---------- */
function renderGate() {
  const s = SESSION;
  const in_ = !!s.authenticated;
  $('gate').hidden = in_;
  $('app').hidden = !in_;
  $('who').hidden = !in_;
  $('logout').hidden = !in_;
  $('who').textContent = in_ ? s.user : '';
  $('login-form').hidden = !(s.configured && !in_);

  const note = $('auth-note');
  if (s.error) note.textContent = 'Berkas kredensial bermasalah: ' + s.error;
  else if (!s.configured) {
    note.textContent = 'Kata sandi operator belum diatur. Di host, jalankan `sudo issboard -set-password`, ' +
      'lalu muat ulang halaman ini. Dashboard baca tetap bisa dibuka seperti biasa.';
  } else if (s.demo) note.textContent = 'Mode demo: masuk dengan demo / demo. Aksinya pura-pura.';
  else note.textContent = 'Setiap aksi di halaman ini dicatat di journal host, beserta nama dan alamat Anda.';

  if (in_) {
    $('who').title = s.expires ? 'sesi berlaku sampai ' + new Date(s.expires).toLocaleString('id-ID') : '';
    render();
  }
}

async function login(ev) {
  ev.preventDefault();
  const btn = ev.submitter;
  if (btn) btn.disabled = true;
  const log = $('gate-log');
  log.hidden = true;
  try {
    const r = await fetch('api/v1/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user: $('login-user').value, password: $('login-pw').value }),
    });
    const b = await r.json();
    $('login-pw').value = '';
    if (!b.ok) { log.textContent = b.error || ('HTTP ' + r.status); log.hidden = false; return; }
    await loadSession();
    await Promise.all([tick(), loadManage()]);
  } catch (e) {
    log.textContent = e.message;
    log.hidden = false;
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function logout() {
  await fetch('api/v1/logout', { method: 'POST' }).catch(() => {});
  await loadSession();
}

/* ---------- navigasi ----------
   Satu bagian terlihat pada satu waktu, dipilih lewat hash — jadi tiap bagian
   bisa di-bookmark (admin.html#dataset), dan tombol kembali browser bekerja. */
const SECTIONS = ['container', 'pool', 'dataset', 'disk', 'arc', 'sanoid', 'jadwal', 'riwayat'];

function currentSection() {
  const h = location.hash.slice(1);
  return SECTIONS.includes(h) ? h : 'container';
}

function showSection() {
  const cur = currentSection();
  for (const s of document.querySelectorAll('.sec')) s.hidden = s.dataset.sec !== cur;
  for (const a of document.querySelectorAll('.side a')) {
    a.classList.toggle('on', a.dataset.sec === cur);
    if (a.dataset.sec === cur) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
  if (cur === 'dataset' && !DS) loadDataset();
  window.scrollTo(0, 0);
}

/* ---------- konfirmasi ---------- */
function confirmDialog({ title, text, typeName, okLabel, danger }) {
  return new Promise((resolve) => {
    const d = $('confirm');
    $('confirm-title').textContent = title;
    $('confirm-text').textContent = text;
    const inp = $('confirm-input');
    const ok = $('confirm-ok');
    ok.textContent = okLabel || 'lanjut';
    ok.className = 'n-btn' + (danger ? ' n-btn-danger' : '');
    $('confirm-type-wrap').hidden = !typeName;
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

/* ---------- hasil aksi ---------- */
function note(ok, key, text) {
  HISTORY.unshift({ ok, key, text, at: new Date() });
  if (HISTORY.length > 100) HISTORY.pop();

  const t = el('div', { class: 'toast ' + (ok ? 'ok' : 'err') },
    el('b', {}, (ok ? '✓ ' : '✕ ') + key), el('span', {}, text));
  const box = $('toasts');
  box.prepend(t);
  while (box.children.length > 3) box.lastChild.remove();
  // Kegagalan tinggal lebih lama: pesannya yang menjelaskan apa yang harus
  // dilakukan, dan itu butuh waktu untuk dibaca.
  setTimeout(() => t.remove(), ok ? 6000 : 20000);
  t.addEventListener('click', () => t.remove());
  renderHistory();
}

function renderHistory() {
  const box = $('m-history');
  clear(box);
  if (!HISTORY.length) { box.append(el('p', { class: 'note' }, 'Belum ada aksi di sesi ini.')); return; }
  const rows = el('div', { class: 'mrows' });
  for (const h of HISTORY) {
    rows.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname' }, el('span', { class: 'n-dot ' + (h.ok ? 'n-dot-ok' : 'n-dot-crit') }), h.key),
      el('div', { class: 'mmeta mono' }, h.text),
      el('div', { class: 'mmeta' }, h.at.toLocaleTimeString('id-ID'))));
  }
  box.append(rows);
}

/* Satu jalan untuk semua aksi: konfirmasi → POST ber-CSRF → catat → segarkan. */
async function act(key, path, body, confirmOpts) {
  if (BUSY.has(key)) return false;
  if (confirmOpts && !(await confirmDialog(confirmOpts))) return false;
  BUSY.add(key);
  render();
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
    note(ok, key, ok ? (out || 'selesai') : `${b.error}${out ? ' — ' + out : ''}`);
  } catch (e) {
    note(false, key, e.message);
  } finally {
    BUSY.delete(key);
    await Promise.all([tick(), loadManage(), currentSection() === 'dataset' ? loadDataset() : null]);
  }
  return ok;
}

function whyDisabled(root) {
  if (!SESSION.authenticated) return 'masuk dulu';
  if (root && MANAGE && MANAGE.helper === false) return 'issboard-helper.socket belum aktif';
  return '';
}

// Tombol aksi. `root` = lewat helper, jadi ikut mati kalau helper tidak ada.
function actBtn(label, key, onClick, { danger, root } = {}) {
  const why = whyDisabled(root);
  const b = el('button', {
    class: 'n-btn n-btn-sm' + (danger ? ' n-btn-danger' : ''),
    type: 'button',
    disabled: !!why || BUSY.has(key),
    title: why || null,
  }, BUSY.has(key) ? '…' : label);
  b.addEventListener('click', onClick);
  return b;
}

// Tombol di form statis ikut aturan yang sama dengan actBtn.
function gateForm(form, key) {
  const why = whyDisabled(true);
  for (const b of form.querySelectorAll('button')) {
    b.disabled = !!why || BUSY.has(key);
    b.title = why;
  }
}

/* ---------- gambar ---------- */
function render() {
  if (!SESSION.authenticated || !LAST) return;
  $('helper-note').hidden = !(MANAGE && MANAGE.helper === false);
  renderContainers(LAST.containers || []);
  renderPools(LAST.pools || []);
  renderDatasets(LAST.datasets || []);
  renderDisks((LAST.smart && LAST.smart.disks) || []);
  renderARC();
  renderSanoid(LAST);
  renderSchedule();
}

/* ---------- container ---------- */
function renderContainers(cs) {
  const box = $('m-containers');
  clear(box);
  if (!cs.length) { box.append(el('p', { class: 'note' }, 'Tidak ada container terbaca — apakah socket-nya terjangkau?')); return; }

  const jalan = cs.filter((c) => c.state === 'running').length;
  box.append(el('p', { class: 'note' }, `${cs.length} container · ${jalan} jalan · ${cs.length - jalan} berhenti`));

  const tbl = el('div', { class: 'tbl tbl-ctr' },
    el('div', { class: 'th' }, el('span', {}, 'nama'), el('span', {}, 'image'), el('span', {}, 'status'), el('span', {}, 'port'), el('span', {}, '')));
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
    const cls = state === 'running' ? 'n-dot-ok' : state === 'exited' || state === 'dead' ? 'n-dot-off' : 'n-dot-warn';
    tbl.append(el('div', { class: 'tr' },
      el('span', { class: 'td-name' }, el('span', { class: 'n-dot ' + cls }), c.name),
      el('span', { class: 'td-meta mono' }, c.image),
      el('span', { class: 'td-meta' }, c.status || state),
      el('span', { class: 'td-meta mono' }, (c.published_ports || []).join(' ') || '—'),
      el('span', { class: 'mbtns td-act' }, btns)));
  }
  box.append(el('div', { class: 'n-card' }, tbl));
}

/* ---------- pool ---------- */
function scrubState(line) {
  const s = (line || '').toLowerCase();
  if (s.includes('paused')) return 'paused';
  if (s.includes('in progress') && s.includes('scrub')) return 'running';
  return 'idle';
}

function renderPools(pools) {
  const box = $('m-pools');
  clear(box);
  if (!pools.length) { box.append(el('p', { class: 'note' }, 'Tidak ada pool ZFS.')); return; }
  const grid = el('div', { class: 'cards' });
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
      if (st === 'paused') btns.push(actBtn('hentikan', k('stop'), () => act(k('stop'), url('stop'), null), { root: true }));
    }
    const online = (p.health || '').toUpperCase() === 'ONLINE';
    grid.append(el('div', { class: 'n-card' },
      el('div', { class: 'card-head' },
        el('span', { class: 'card-title-lg' }, p.name),
        el('span', { class: 'n-pill' + (online ? '' : ' n-pill-crit') }, p.health || '?')),
      el('dl', { class: 'kv' },
        el('dt', {}, 'terpakai'), el('dd', {}, `${bytes(p.alloc_bytes)} / ${bytes(p.size_bytes)}`),
        el('dt', {}, 'bentuk'), el('dd', {}, p.mirrored ? 'redundan (mirror/raidz)'
          : (p.devices && p.devices.length > 1 ? 'STRIPE — tanpa redundansi' : 'disk tunggal')),
        el('dt', {}, 'error'), el('dd', {}, `baca ${p.read_errors} · tulis ${p.write_errors} · checksum ${p.cksum_errors}`),
        el('dt', {}, 'scrub'), el('dd', {}, scrubText(p.scan_line))),
      el('div', { class: 'mbtns' }, btns)));
  }
  box.append(grid);
}

/* ---------- dataset & snapshot ----------
   Detailnya diambil saat dataset dipilih dan sesudah aksi, BUKAN tiap 15
   detik: dataset dengan ribuan snapshot membuat daftar itu mahal. */
function selectedDataset() { return $('ds-select').value; }

function pickDataset(name) {
  if ($('ds-select').value === name && DS) return;
  $('ds-select').value = name;
  DS = null;
  loadDataset();
}

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
  render();
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
    if ((sel.value !== cur || !DS) && currentSection() === 'dataset') loadDataset();
  }
  for (const f of ['snap-form', 'prop-form', 'child-form']) gateForm($(f), f);

  // Daftar di layar lebar: nama, isi, dan umur snapshot terakhir sekaligus —
  // cukup untuk memilih tanpa membuka satu per satu.
  const list = $('ds-list');
  clear(list);
  for (const d of ds) {
    const b = el('button', { type: 'button', class: 'ds-item' + (d.name === sel.value ? ' on' : '') },
      el('span', { class: 'ds-name' }, d.name),
      el('span', { class: 'mmeta' }, `${bytes(d.used_bytes)} · ${d.snapshot_count} snapshot` +
        (d.last_snapshot ? ` · ${relTime(d.last_snapshot)}` : '')));
    b.addEventListener('click', () => pickDataset(d.name));
    list.append(b);
  }

  $('ds-title').textContent = sel.value || '—';
  const box = $('ds-detail');
  clear(box);
  if (!ds.length) { box.append(el('p', { class: 'note' }, 'Tidak ada dataset.')); return; }
  if (!DS || DS.name !== sel.value) { box.append(el('p', { class: 'note' }, 'memuat…')); return; }
  if (DS.error) { box.append(el('p', { class: 'note' }, 'gagal memuat: ' + DS.error)); return; }
  for (const e of DS.errors || []) box.append(el('p', { class: 'note' }, e));

  const kv = el('dl', { class: 'kv kv-props' });
  for (const p of DS.props || []) {
    const warisan = p.source && p.source !== 'local';
    kv.append(el('dt', {}, p.name), el('dd', {}, p.value,
      warisan ? el('span', { class: 'mmeta' }, ` · ${p.source.replace('inherited from', 'warisan')}`) : null));
  }
  box.append(kv);

  const snaps = DS.snapshots || [];
  box.append(el('h4', { class: 'n-label sub-head' }, `snapshot (${snaps.length})`));
  if (!snaps.length) box.append(el('p', { class: 'note' }, 'Belum ada snapshot.'));
  const tbl = el('div', { class: 'tbl tbl-snap' });
  if (snaps.length) {
    tbl.append(el('div', { class: 'th' }, el('span', {}, 'nama'), el('span', {}, 'dibuat'),
      el('span', {}, 'unik'), el('span', {}, 'merujuk'), el('span', {}, '')));
  }
  for (const sn of snaps.slice(0, SNAP_MAX)) {
    const label = sn.name.slice(sn.name.indexOf('@') + 1);
    const sanoid = label.startsWith('autosnap_');
    const manual = label.startsWith('issboard_');
    const k = 'snapshot.destroy ' + sn.name;
    tbl.append(el('div', { class: 'tr' },
      el('span', { class: 'td-name mono' }, label,
        el('span', { class: 'n-pill' }, sanoid ? 'sanoid' : manual ? 'manual' : 'lain')),
      el('span', { class: 'td-meta' }, relTime(sn.created)),
      el('span', { class: 'td-meta' }, bytes(sn.used_bytes)),
      el('span', { class: 'td-meta' }, bytes(sn.referenced_bytes)),
      el('span', { class: 'mbtns td-act' }, actBtn('hapus', k, () => act(k, 'api/v1/snapshots/destroy', { snapshot: sn.name }, {
        title: 'Hapus snapshot',
        text: `${sn.name} dihapus, membebaskan sekitar ${bytes(sn.used_bytes)}. Titik pulih ini hilang dan tidak ada undo. ` +
          (sanoid ? 'Ini buatan sanoid — sanoid akan membuat yang baru sesuai jadwal, tapi yang ini tidak kembali.'
            : 'Snapshot yang bukan buatan sanoid TIDAK dipangkas otomatis; menghapusnya memang harus manual.'),
        typeName: label,
        okLabel: 'hapus',
        danger: true,
      }), { danger: true, root: true }))));
  }
  box.append(tbl);
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
      '. Dataset baru TIDAK otomatis masuk kebijakan snapshot — lihat bagian sanoid sesudahnya.',
    okLabel: 'buat',
  }).then((ok) => { if (ok) { $('child-name').value = ''; $('child-quota').value = ''; } });
}

/* ---------- disk ---------- */
function renderDisks(disks) {
  const box = $('m-disks');
  clear(box);
  box.append(el('div', { class: 'mbtns mtop' },
    actBtn('segarkan cache smart', 'smart.refresh', () => act('smart.refresh', 'api/v1/smart/refresh', null), { root: true })));
  if (!disks.length) { box.append(el('p', { class: 'note' }, 'Belum ada disk di cache SMART.')); return; }

  const grid = el('div', { class: 'cards' });
  for (const d of disks) {
    const k = (op) => `smart.${op} ${d.device}`;
    const go = (op, confirmOpts) => act(k(op), `api/v1/smart/${op}`, { device: d.device }, confirmOpts);
    const bangun = d.standby ? 'Disk ini SEDANG TIDUR dan akan dibangunkan. ' : 'Disk yang tidur akan dibangunkan. ';
    grid.append(el('div', { class: 'n-card' },
      el('div', { class: 'card-head' },
        el('span', { class: 'card-title-lg' }, d.device),
        d.standby ? el('span', { class: 'n-pill' }, 'tidur')
          : el('span', { class: 'n-pill' + (d.passed ? '' : ' n-pill-crit') }, d.passed ? 'passed' : 'gagal')),
      el('dl', { class: 'kv' },
        el('dt', {}, 'model'), el('dd', {}, d.model || '—'),
        el('dt', {}, 'suhu'), el('dd', {}, d.standby || !d.temperature_c ? '—' : d.temperature_c + ' °C'),
        el('dt', {}, 'realloc'), el('dd', {}, `${d.reallocated_sectors} · pending ${d.pending_sectors}`),
        el('dt', {}, 'self-test'), el('dd', {}, d.last_self_test || 'belum pernah selesai')),
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
  box.append(grid);
}

/* ---------- ARC ---------- */
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
  box.append(el('div', { class: 'stat' }, bytes(a.size_bytes), el('span', { class: 'unit' }, `terpakai dari batas ${bytes(a.c_max)}`)),
    el('div', { class: 'bar' }, el('i', { style: `width:${Math.min(100, pct(a.size_bytes, a.c_max))}%` })),
    el('dl', { class: 'kv' },
      el('dt', {}, 'parameter'), el('dd', {}, a.param ? bytes(a.param) : '0 (bawaan ZFS)'),
      el('dt', {}, 'setelah reboot'), el('dd', {}, persisted.length
        ? persisted.map((p) => `${bytes(p.value)} — ${p.file}`).join(' · ')
        : 'bawaan ZFS (tidak diatur di modprobe.d)'),
      el('dt', {}, 'rentang'), el('dd', {}, `${bytes(a.lower)} – ${bytes(a.upper)}`),
      el('dt', {}, 'RAM'), el('dd', {}, bytes(a.mem_total))));

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

  // Berkas milik orang lain yang sudah mengatur nilai permanen: issboard
  // tidak menyuntingnya, dan helper menolak menulis berkas kedua yang akan
  // kalah atau menang diam-diam. Daripada membiarkan orang menekan tombol lalu
  // ditolak, pilihan "permanen" dimatikan di depan, dan jalan keluarnya ditulis.
  const asing = persisted.find((p) => !p.ours);
  const pers = $('arc-persist');
  const pn = $('arc-persist-note');
  pers.disabled = !!asing;
  if (asing) pers.checked = false;
  pn.hidden = !asing;
  if (asing) {
    pn.textContent = `Nilai permanen sudah diatur di ${asing.file}, berkas yang tidak ditulis issboard. ` +
      'Perubahan dari sini hanya berlaku sampai reboot. Untuk mengubahnya permanen, sunting berkas itu di host, ' +
      `atau hapus baris zfs_arc_max di sana supaya issboard boleh mengelolanya.`;
  }

  const inp = $('arc-gib');
  inp.min = (a.lower / GIB).toFixed(1);
  inp.max = (a.upper / GIB).toFixed(1);
  // Diisi sekali, dan tidak pernah saat sedang diketik.
  if (!inp.value && document.activeElement !== inp) inp.value = ((a.param || a.c_max) / GIB).toFixed(1);
  gateForm(form, 'arc');
}

function submitARC(ev, reset) {
  if (ev) ev.preventDefault();
  const persist = $('arc-persist').checked && !$('arc-persist').disabled;
  const v = reset ? 0 : Math.round(parseFloat($('arc-gib').value) * GIB);
  if (!reset && !(v > 0)) { note(false, 'arc', 'isi batas ARC dalam GiB'); return; }
  const a = MANAGE.arc;
  const kata = reset ? 'kembali ke bawaan ZFS' : `batas ARC jadi ${bytes(v)}`;
  act('arc', 'api/v1/arc', { max_bytes: v, persist }, {
    title: 'Ubah batas ARC',
    text: `Atur ${kata}. Terpakai sekarang ${bytes(a.size_bytes)}. ` +
      (v && v < a.size_bytes ? 'ARC akan menyusut bertahap, dan bacaan dari disk akan bertambah. ' : '') +
      (persist ? 'Nilai juga ditulis ke /etc/modprobe.d supaya berlaku setelah reboot. Kalau root ' +
        'filesystem ada di ZFS, perlu `update-initramfs -u` juga.' : 'Hanya berlaku sampai reboot.'),
    okLabel: 'terapkan',
  });
}

/* ---------- sanoid ----------
   Potongan config ditawarkan untuk dataset yang oleh server dinilai tidak
   tercakup (temuan snap.uncovered.*), jadi keputusannya tetap dari
   internal/health, bukan aturan kedua di sini. */
function renderSanoid(d) {
  const box = $('m-sanoid');
  clear(box);
  const pol = d.snap_policy || {};
  const timer = ((MANAGE && MANAGE.schedule && MANAGE.schedule.timers) || []).find((t) => t.unit.startsWith('sanoid'));

  const card = el('div', { class: 'n-card' });
  card.append(el('dl', { class: 'kv' },
    el('dt', {}, 'berkas'), el('dd', {}, pol.present ? pol.source : 'tidak ada — perbandingan cakupan mati'),
    el('dt', {}, 'template'), el('dd', {}, (pol.templates && pol.templates.length) ? pol.templates.join(', ') : '—'),
    el('dt', {}, 'timer'), el('dd', {}, timer
      ? `${timer.unit}: terakhir ${timer.last ? relTime(timer.last) : 'belum pernah'} · berikut ${timer.next ? relWhen(timer.next) : '—'}`
      : 'sanoid.timer tidak terlihat')));
  card.append(el('div', { class: 'mbtns mtop' }, actBtn('jalankan sanoid sekarang', 'sanoid.run',
    () => act('sanoid.run', 'api/v1/sanoid/run', null, {
      title: 'Jalankan sanoid',
      text: 'Menyalakan sanoid.service sekarang — persis yang dijalankan timernya: membuat snapshot yang jatuh tempo ' +
        'dan memangkas yang kedaluwarsa menurut sanoid.conf.',
      okLabel: 'jalankan',
    }), { root: true })));
  box.append(card);

  const uncovered = (d.findings || []).filter((f) => (f.key || '').startsWith('snap.uncovered.')).map((f) => f.subject);
  if (pol.present && uncovered.length) {
    const tpl = (pol.templates && pol.templates[0]) || 'ISI_TEMPLATE';
    const text = uncovered.map((n) => `[${n}]\n\tuse_template = ${tpl}\n`).join('\n');
    const pre = el('pre', { class: 'snippet' }, text);
    const copy = el('button', { class: 'n-btn n-btn-sm', type: 'button' }, 'salin');
    copy.addEventListener('click', () => copyText(pre, copy));
    box.append(el('div', { class: 'n-card' },
      el('h3', { class: 'n-label' }, `tidak tercakup (${uncovered.length})`),
      el('p', { class: 'note' }, `Tempel ke ${pol.source}, ganti template kalau perlu, lalu jalankan sanoid. ` +
        'Kalau memang tidak perlu snapshot, daftarkan di snapshot_exempt di /etc/issboard.yaml.'),
      pre, el('div', { class: 'mbtns' }, copy)));
  } else if (pol.present) {
    box.append(el('p', { class: 'note' }, 'Semua dataset yang berisi tercakup kebijakan.'));
  }
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

/* ---------- jadwal ---------- */
function renderSchedule() {
  const box = $('m-schedule');
  clear(box);
  if (!MANAGE) { box.append(el('p', { class: 'note' }, 'memuat…')); return; }
  if (MANAGE.error) { box.append(el('p', { class: 'note' }, 'gagal memuat: ' + MANAGE.error)); return; }
  const s = MANAGE.schedule || {};

  for (const n of s.notes || []) box.append(el('p', { class: 'note warn' }, n));
  for (const e of s.errors || []) box.append(el('p', { class: 'note' }, e));

  const timers = s.timers || [];
  const tc = el('div', { class: 'n-card' }, el('h3', { class: 'n-label' }, 'timer systemd'));
  if (!timers.length) tc.append(el('p', { class: 'note' }, 'Tidak ada timer terkait penyimpanan.'));
  else {
    const tbl = el('div', { class: 'tbl tbl-timer' },
      el('div', { class: 'th' }, el('span', {}, 'unit'), el('span', {}, 'berikut'), el('span', {}, 'terakhir')));
    for (const t of timers) {
      tbl.append(el('div', { class: 'tr' },
        el('span', { class: 'td-name mono' }, t.unit),
        el('span', { class: 'td-meta' }, t.next ? relWhen(t.next) : '—'),
        el('span', { class: 'td-meta' }, t.last ? relTime(t.last) : 'belum pernah')));
    }
    tc.append(tbl);
  }
  if (s.other_timers) tc.append(el('p', { class: 'note' }, `+ ${s.other_timers} timer lain yang tidak menyangkut penyimpanan.`));
  box.append(tc);

  const cron = s.cron || [];
  const cc = el('div', { class: 'n-card' }, el('h3', { class: 'n-label' }, 'cron'));
  if (!cron.length) cc.append(el('p', { class: 'note' }, 'Tidak ada baris cron terkait ZFS/SMART.'));
  const cr = el('div', { class: 'mrows' });
  for (const c of cron) {
    cr.append(el('div', { class: 'mrow' },
      el('div', { class: 'mname mono' }, c.schedule),
      el('div', { class: 'mmeta mono' }, c.command),
      el('div', { class: 'mmeta' }, `${c.file}${c.user ? ' · ' + c.user : ''}`)));
  }
  cc.append(cr);
  box.append(cc);

  const sc = el('div', { class: 'n-card' }, el('h3', { class: 'n-label' }, 'smartd'));
  if (!s.smartd) sc.append(el('p', { class: 'note' }, 'smartd.conf tidak ditemukan.'));
  else {
    sc.append(el('p', { class: 'note' }, s.smartd.file +
      (s.smartd.self_tests ? ' — menjadwalkan self-test sendiri (-s)' : '')));
    for (const l of s.smartd.lines || []) sc.append(el('div', { class: 'mmeta mono' }, l));
  }
  box.append(sc);
}

/* ---------- mulai ---------- */
function init() {
  initTheme();
  $('login-form').addEventListener('submit', login);
  $('logout').addEventListener('click', logout);
  $('arc-form').addEventListener('submit', (e) => submitARC(e, false));
  $('arc-default').addEventListener('click', () => submitARC(null, true));
  $('ds-select').addEventListener('change', () => { DS = null; loadDataset(); });
  $('prop-name').addEventListener('change', fillPropForm);
  $('snap-form').addEventListener('submit', submitSnap);
  $('prop-form').addEventListener('submit', submitProp);
  $('child-form').addEventListener('submit', submitChild);
  window.addEventListener('hashchange', showSection);
  showSection();
  renderHistory();

  loadSession().then(() => Promise.all([tick(), loadManage()]));
  setInterval(tick, REFRESH_MS);
  // Jadwal dan ARC jarang berubah; setengah kecepatan status sudah cukup.
  setInterval(() => { if (!document.hidden && SESSION.authenticated) loadManage(); }, 2 * REFRESH_MS);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') { tick(); loadSession(); }
  });
}

init();
