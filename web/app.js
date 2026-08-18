// Vanilla, tanpa build step — sesuai plan bagian 7.
const $ = (id) => document.getElementById(id);

const bytes = (n) => {
  if (!n) return "0";
  const u = ["B", "K", "M", "G", "T", "P"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(n < 10 && i > 0 ? 1 : 0) + u[i];
};

const dur = (s) => {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600);
  return d ? `${d}h ${h}j` : `${h}j`;
};

const card = (k, v, cls = "") =>
  `<div class="card"><div class="k">${k}</div><div class="v ${cls}">${v}</div></div>`;

const table = (cols, rows) =>
  `<thead><tr>${cols.map((c) => `<th>${c}</th>`).join("")}</tr></thead>` +
  `<tbody>${rows.map((r) => `<tr>${r.map((c) => `<td>${c}</td>`).join("")}</tr>`).join("")}</tbody>`;

// alerts mengumpulkan hal yang benar-benar perlu ditindak, bukan sekadar angka.
function alerts(d) {
  const out = [];
  for (const p of d.pools || []) {
    if (p.health !== "ONLINE") out.push(`Pool <b>${p.name}</b> berstatus ${p.health}`);
    if (!p.scan_line) out.push(`Pool <b>${p.name}</b> belum pernah di-scrub`);
    if (!p.mirrored) out.push(`Pool <b>${p.name}</b> tanpa redundansi (stripe) — satu disk mati, pool hilang`);
    const e = p.read_errors + p.write_errors + p.cksum_errors;
    if (e > 0) out.push(`Pool <b>${p.name}</b> punya ${e} error I/O`);
  }
  if (d.smart?.stale) out.push("Cache SMART basi — timer pengumpulnya mungkin mati");
  for (const s of d.smart?.disks || []) {
    if (!s.passed) out.push(`Disk <b>${s.device}</b> GAGAL self-assessment`);
    if (s.reallocated > 0) out.push(`Disk <b>${s.device}</b>: ${s.reallocated} reallocated sector`);
    if (s.pending_sectors > 0) out.push(`Disk <b>${s.device}</b>: ${s.pending_sectors} pending sector`);
  }
  for (const c of d.containers || []) {
    if (c.state === "running" && (!c.networks || !c.networks.length))
      out.push(`Container <b>${c.name}</b> jalan tanpa network`);
    if (c.state !== "running" && c.state !== "exited")
      out.push(`Container <b>${c.name}</b> berstatus ${c.state}`);
  }
  return out;
}

function render(d) {
  $("meta").textContent =
    `${d.host.hostname} · uptime ${dur(d.host.uptime_seconds)} · ` +
    `diperbarui ${new Date(d.collected_at).toLocaleTimeString("id-ID")}`;

  const a = alerts(d);
  $("alerts").hidden = a.length === 0;
  $("alert-list").innerHTML = a.map((x) => `<li>${x}</li>`).join("");

  const h = d.host;
  const memUsed = h.mem_total_bytes - h.mem_available_bytes;
  $("host").innerHTML =
    card("Beban", `${h.load1.toFixed(2)} / ${h.load5.toFixed(2)} / ${h.load15.toFixed(2)}`) +
    card("Memori", `${bytes(memUsed)} / ${bytes(h.mem_total_bytes)}`) +
    card("Swap", `${bytes(h.swap_total_bytes - h.swap_free_bytes)} / ${bytes(h.swap_total_bytes)}`) +
    card("ARC", `${bytes(h.arc_size_bytes)} / ${bytes(h.arc_max_bytes)}`) +
    card("ARC hit", `${(h.arc_hit_ratio * 100).toFixed(1)}%`,
      h.arc_hit_ratio > 0.9 ? "ok" : "warn");

  $("pools").innerHTML = (d.pools || []).map((p) => {
    const pct = p.size_bytes ? (p.alloc_bytes / p.size_bytes) * 100 : 0;
    const cls = p.health === "ONLINE" ? "ok" : "bad";
    return card(
      `${p.name} · ${p.mirrored ? "mirror" : "stripe"}`,
      `${bytes(p.alloc_bytes)} / ${bytes(p.size_bytes)} (${pct.toFixed(0)}%)`,
      cls
    );
  }).join("");

  const sm = d.smart || {};
  $("smart-note").textContent = sm.written_at && !sm.written_at.startsWith("0001")
    ? `Cache ditulis ${new Date(sm.written_at).toLocaleString("id-ID")}` +
      (sm.stale ? " — BASI" : "")
    : "Cache SMART belum tersedia.";
  $("smart").innerHTML = table(
    ["Device", "Model", "Suhu", "Jam nyala", "Realloc", "Pending", "Status"],
    (sm.disks || []).map((s) => [
      s.device, s.model, s.standby ? "standby" : `${s.temperature_c}°C`,
      s.power_on_hours,
      s.reallocated > 0 ? `<span class="bad">${s.reallocated}</span>` : "0",
      s.pending_sectors > 0 ? `<span class="bad">${s.pending_sectors}</span>` : "0",
      s.passed ? '<span class="ok">PASSED</span>' : '<span class="bad">FAILED</span>',
    ])
  );

  $("containers").innerHTML = table(
    ["Nama", "Status", "Network", "Port publik"],
    (d.containers || []).map((c) => [
      c.name,
      c.state === "running"
        ? `<span class="ok">${c.status}</span>`
        : `<span class="warn">${c.status}</span>`,
      (c.networks || []).join(", ") ||
        '<span class="bad">tidak ada</span>',
      (c.published_ports || []).join("<br>") || "—",
    ])
  );
}

async function tick() {
  try {
    const r = await fetch("api/v1/status");
    if (!r.ok) throw new Error(r.status);
    render(await r.json());
  } catch (e) {
    $("meta").textContent = `gagal memuat: ${e.message}`;
  }
}

tick();
setInterval(tick, 15000);
