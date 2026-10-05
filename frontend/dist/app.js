"use strict";
// Thin UI: every call goes to the Go side, which runs the mndl CLI.
const api = () => window.go.main.App;
const $ = (s) => document.querySelector(s);
const PALETTE = ["#6ea8ff", "#ff9a52", "#3ccf91", "#c58bff", "#ffd166", "#ff6f91", "#4dd4e6", "#a3e05a"];
const KIND = { wifi: "Wi-Fi", ethernet: "유선", usb: "USB/테더링", vpn: "VPN", virtual: "가상", other: "기타" };
const STATE = { starting: "시작 중", probing: "주소 확인 중", downloading: "받는 중", pausing: "멈추는 중", paused: "일시정지", done: "완료", error: "실패", canceled: "취소됨", queued: "대기" };
const ICON = {
  pause: '<path d="M9 5v14M15 5v14"/>',
  play: '<path d="M7 5l12 7-12 7z"/>',
  retry: '<path d="M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6"/>',
  stop: '<path d="M6 6l12 12M18 6L6 18"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  term: '<path d="M4 6l6 6-6 6M12 18h8"/>',
};

let nets = [];
let cfg = { dir: "", networks: [], conns: 8 };
let tests = {};
let items = [];
let showVirtual = localStorage.getItem("showVirtual") === "1";
const rows = new Map();

function colorFor(id) {
  const i = nets.findIndex((n) => n.id === id);
  if (i >= 0) return PALETTE[i % PALETTE.length];
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}
function bytes(n) {
  if (n == null || n < 0) return "?";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(n >= 100 ? 0 : 1)) + " " + u[i];
}
const speed = (b) => bytes(b) + "/s";
function eta(sec) {
  if (!isFinite(sec) || sec <= 0) return "";
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  if (h) return `${h}시간 ${m}분`;
  if (m) return `${m}분 ${s}초`;
  return `${s}초`;
}
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}
function iconBtn(name, title, onClick) {
  const b = el("button", "icon", `<svg viewBox="0 0 24 24">${ICON[name]}</svg>`);
  b.title = title;
  b.setAttribute("aria-label", title);
  b.addEventListener("click", (e) => { e.stopPropagation(); onClick(); });
  return b;
}
function msg(text) { $("#formMsg").textContent = text || ""; }

// ---------- networks ----------
async function loadNets() {
  try {
    nets = (await api().Networks()) || [];
  } catch (e) {
    nets = [];
    msg(String(e));
  }
  renderNets();
}

function renderNets() {
  const box = $("#nets");
  box.innerHTML = "";
  const sel = new Set(cfg.networks || []);
  const visible = nets.filter((n) => showVirtual || !n.virtual || sel.has(n.id));
  if (!visible.length) {
    box.append(el("div", "nets-empty", "연결된 네트워크가 없음. 와이파이·테더링·랜을 연결하고 다시 찾기를 눌러줘."));
  }
  for (const n of visible) {
    const on = sel.has(n.id);
    const card = el("label", "net" + (on ? " on" : ""));
    card.style.setProperty("--c", colorFor(n.id));
    const t = tests[n.id];
    let test = "";
    if (t === "wait") test = `<div class="net-test wait">확인 중…</div>`;
    else if (t && t.ok) test = `<div class="net-test ok">✓ 인터넷 연결됨 · ${t.ip}${t.loc ? " · " + t.loc : ""} · ${t.ms}ms</div>`;
    else if (t) test = `<div class="net-test bad" title="${esc(t.error || "")}">✗ 이 네트워크로는 인터넷이 안 됨</div>`;
    card.innerHTML = `
      <input type="checkbox" ${on ? "checked" : ""} aria-label="${esc(n.label)} 사용" />
      <span class="tick"><svg viewBox="0 0 24 24"><path d="M5 12l5 5 9-10"/></svg></span>
      <span>
        <span class="net-top"><span class="net-label">${esc(n.label)}</span><span class="kind">${KIND[n.kind] || n.kind}</span></span>
        <div class="net-sub">${esc(n.id)} · ${esc((n.addrs || [])[0] || "")}</div>
        ${test}
      </span>`;
    card.querySelector("input").addEventListener("change", (e) => toggleNet(n.id, e.target.checked));
    box.append(card);
  }
}
function esc(s) { return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])); }

async function toggleNet(id, on) {
  const set = new Set(cfg.networks || []);
  on ? set.add(id) : set.delete(id);
  // keep the order of the network list
  cfg.networks = nets.map((n) => n.id).filter((x) => set.has(x)).concat([...set].filter((x) => !nets.some((n) => n.id === x)));
  renderNets();
  await saveCfg();
  if (on) testNets([id]);
}

async function testNets(ids) {
  ids = ids || (cfg.networks || []);
  if (!ids.length) { msg("확인할 네트워크를 먼저 골라줘"); return; }
  ids.forEach((id) => (tests[id] = "wait"));
  renderNets();
  try {
    const res = (await api().TestNetworks(ids)) || [];
    res.forEach((r) => (tests[r.id] = r));
  } catch (e) {
    ids.forEach((id) => (tests[id] = { ok: false, error: String(e) }));
  }
  renderNets();
}

// ---------- config ----------
let saveTimer;
async function saveCfg() {
  try {
    cfg = await api().SetConfig(cfg);
  } catch (e) { msg(String(e)); }
  renderCfg();
}
function renderCfg() {
  $("#bindNote").textContent = cfg.note || "";
  $("#dirPath").textContent = cfg.dir;
  $("#dirPath").title = cfg.dir;
  $("#conns").value = cfg.conns;
  $("#connsOut").textContent = cfg.conns;
  connsWarn();
}
function connsWarn() {
  $("#connsWarn").textContent = cfg.conns > 16 ? "16개보다 많으면 서버가 연결을 막거나(429) 차단할 수 있음. 막히면 앱이 알아서 연결을 줄임." : "";
}

// ---------- downloads ----------
function render(list) {
  items = list || [];
  const box = $("#list");
  const seen = new Set();
  items.forEach((it, idx) => {
    seen.add(it.id);
    let r = rows.get(it.id);
    if (!r) { r = makeRow(it); rows.set(it.id, r); }
    updateRow(r, it);
    if (box.children[idx] !== r.root) box.insertBefore(r.root, box.children[idx] || null);
  });
  for (const [id, r] of rows) if (!seen.has(id)) { r.root.remove(); rows.delete(id); }
  $("#empty").style.display = items.length ? "none" : "";
  $("#count").textContent = items.length ? items.length : "";
  const active = items.filter((i) => ["downloading", "probing", "starting"].includes(i.state));
  const total = active.reduce((s, i) => s + (i.speed || 0), 0);
  $("#total").innerHTML = active.length ? `받는 중 ${active.length}개 · <b>${speed(total)}</b>` : "";
}

function makeRow(it) {
  const root = el("article", "item");
  const top = el("div", "item-top");
  const name = el("div", "item-name");
  const pill = el("span", "pill");
  const actions = el("div", "actions");
  top.append(name, pill, actions);
  const url = el("div", "url");
  const bar = el("div", "bar");
  const stats = el("div", "stats");
  const chips = el("div", "chips");
  const warn = el("div", "warn");
  const err = el("div", "err");
  const cmd = el("div", "cmd");
  root.append(top, url, bar, stats, chips, warn, err, cmd);
  return { root, name, pill, actions, url, bar, stats, chips, warn, err, cmd, sig: "" };
}

function updateRow(r, it) {
  r.root.className = "item " + it.state + (r.root.classList.contains("show-cmd") ? " show-cmd" : "");
  r.name.textContent = it.fileName || shortName(it.url);
  r.name.title = it.path || it.fileName || it.url;
  r.pill.className = "pill " + it.state;
  r.pill.textContent = STATE[it.state] || it.state;
  r.url.textContent = it.url;
  r.url.title = it.url;
  r.cmd.textContent = it.command || "";

  // stacked bar: each network's share of the file
  const size = it.size > 0 ? it.size : 0;
  const stats = it.netStats || [];
  const segs = size ? stats.map((n) => ({ c: colorFor(n.id), w: Math.max(0, Math.min(100, (n.bytes / size) * 100)) })) : [];
  if (r.bar.children.length !== segs.length) {
    r.bar.innerHTML = "";
    segs.forEach(() => r.bar.append(el("i")));
  }
  segs.forEach((s, i) => { const x = r.bar.children[i]; x.style.setProperty("--c", s.c); x.style.width = s.w + "%"; });
  if (!size && it.state === "done") { r.bar.innerHTML = '<i style="width:100%;--c:var(--ok)"></i>'; }

  const pct = size ? ((it.done / size) * 100).toFixed(1) + "%" : "";
  const parts = [];
  if (it.state === "done") {
    const sec = (it.finishedAt - it.createdAt) / 1000;
    parts.push(`<b>${bytes(it.size)}</b>`);
    if (stats.length > 1 || it.netStats) parts.push(`네트워크 ${stats.length}개`);
  } else {
    if (pct) parts.push(`<b>${pct}</b>`);
    parts.push(`${bytes(it.done)} / ${bytes(it.size)}`);
    if (it.state === "downloading") {
      parts.push(`<b>${speed(it.speed || 0)}</b>`);
      if (size && it.speed > 0) parts.push("남은 시간 " + eta((size - it.done) / it.speed));
    }
    if (it.state === "downloading" && !it.rangeOK) parts.push("서버가 나눠받기를 안 받아줘서 한 줄로 받는 중");
  }
  if (it.throttled > 0) parts.push(`서버 제한으로 연결 ${it.throttled}개 줄임`);
  r.stats.innerHTML = parts.join("<span>·</span>");

  r.chips.innerHTML = "";
  for (const n of stats) {
    const share = size ? Math.round((n.bytes / size) * 100) : 0;
    const chip = el("span", "chip" + (n.failed ? " failed" : ""));
    chip.style.setProperty("--c", colorFor(n.id));
    let v;
    if (n.failed) v = "끊김";
    else if (it.state === "downloading") v = `${speed(n.speed)} ×${n.active}`;
    else v = `${bytes(n.bytes)}${size ? " · " + share + "%" : ""}`;
    chip.innerHTML = `<span class="dot"></span>${esc(n.label)} <span class="v">${v}</span>`;
    if (n.lastError) chip.title = n.lastError;
    r.chips.append(chip);
  }
  r.warn.textContent = (it.warnings || []).join(" / ");
  r.err.textContent = it.state === "error" ? it.error || "알 수 없는 오류" : "";

  const sig = it.state;
  if (r.sig !== sig) {
    r.sig = sig;
    r.actions.innerHTML = "";
    const id = it.id;
    const call = (fn) => async () => { try { await fn(); } catch (e) { msg(String(e)); } refresh(); };
    if (["downloading", "probing", "starting"].includes(it.state)) {
      r.actions.append(iconBtn("pause", "일시정지", call(() => api().Pause(id))));
      r.actions.append(iconBtn("stop", "취소(받던 파일 삭제)", call(() => api().Cancel(id))));
    } else if (it.state === "paused") {
      r.actions.append(iconBtn("play", "이어받기 (지금 고른 네트워크로)", call(() => api().Resume(id))));
      r.actions.append(iconBtn("stop", "취소(받던 파일 삭제)", call(() => api().Cancel(id))));
    } else if (it.state === "error") {
      r.actions.append(iconBtn("retry", "다시 시도 (받은 데까지 이어서)", call(() => api().Resume(id))));
    } else if (it.state === "canceled") {
      r.actions.append(iconBtn("retry", "처음부터 다시 받기", call(() => api().Resume(id))));
    }
    r.actions.append(iconBtn("folder", it.state === "done" ? "Finder/탐색기에서 보기" : "저장 폴더 열기", call(() => api().Reveal(id))));
    r.actions.append(iconBtn("term", "실행한 CLI 명령 보기", () => r.root.classList.toggle("show-cmd")));
    if (!["downloading", "probing", "starting", "pausing"].includes(it.state)) {
      r.actions.append(iconBtn("trash", it.state === "done" ? "목록에서 지우기 (파일은 남음)" : "목록에서 지우기 (임시 파일 삭제)", call(() => api().Remove(id))));
    }
  }
}
function shortName(u) {
  try { const p = new URL(u).pathname.split("/").filter(Boolean); return decodeURIComponent(p[p.length - 1] || u); } catch { return u; }
}
async function refresh() { try { render(await api().List()); } catch (e) {} }

// ---------- wiring ----------
async function submit(e) {
  e && e.preventDefault();
  msg("");
  const urls = $("#urls").value.split(/\s+/).map((s) => s.trim()).filter(Boolean);
  const headers = $("#headers").value.split("\n").map((s) => s.trim()).filter(Boolean);
  if (!urls.length) { msg("다운로드 주소를 넣어줘"); $("#urls").focus(); return; }
  $("#goBtn").disabled = true;
  try {
    await api().Add({ urls, fileName: $("#fileName").value.trim(), headers });
    $("#urls").value = "";
    $("#fileName").value = "";
    autosize();
  } catch (err) {
    msg(String(err));
  } finally {
    $("#goBtn").disabled = false;
    refresh();
  }
}
function autosize() {
  const t = $("#urls");
  t.style.height = "auto";
  t.style.height = Math.min(180, Math.max(58, t.scrollHeight + 2)) + "px";
  const multi = t.value.trim().split(/\s+/).filter(Boolean).length > 1;
  $("#fileName").disabled = multi;
  $("#fileName").placeholder = multi ? "주소가 여러 개면 서버가 주는 이름으로 저장" : "파일 이름 (비우면 서버가 주는 이름)";
}

async function init() {
  const isMac = navigator.userAgent.includes("Mac");
  if (isMac) document.body.classList.add("mac");
  $("#kbd").textContent = isMac ? "⌘↵" : "Ctrl+↵";
  $("#showVirtual").checked = showVirtual;

  $("#composer").addEventListener("submit", submit);
  $("#urls").addEventListener("input", autosize);
  $("#composer").addEventListener("keydown", (e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit(e); });
  $("#urls").addEventListener("keydown", (e) => {
    // Enter alone submits when there is a single line; Shift+Enter adds a line
    if (e.key === "Enter" && !e.shiftKey && !e.metaKey && !e.ctrlKey && !$("#urls").value.includes("\n")) submit(e);
  });
  $("#hdrToggle").addEventListener("click", () => {
    const h = $("#headers");
    h.hidden = !h.hidden;
    $("#hdrToggle").setAttribute("aria-expanded", String(!h.hidden));
    if (!h.hidden) h.focus();
  });
  $("#refreshBtn").addEventListener("click", async () => { tests = {}; await loadNets(); });
  $("#testBtn").addEventListener("click", () => testNets());
  $("#showVirtual").addEventListener("change", (e) => { showVirtual = e.target.checked; localStorage.setItem("showVirtual", showVirtual ? "1" : "0"); renderNets(); });
  $("#conns").addEventListener("input", (e) => { cfg.conns = +e.target.value; $("#connsOut").textContent = cfg.conns; connsWarn(); clearTimeout(saveTimer); saveTimer = setTimeout(saveCfg, 300); });
  $("#dirBtn").addEventListener("click", async () => {
    try { const d = await api().PickDir(cfg.dir); if (d) { cfg.dir = d; await saveCfg(); } } catch (e) { msg(String(e)); }
  });
  $("#dirOpen").addEventListener("click", () => api().OpenDir(cfg.dir).catch((e) => msg(String(e))));
  $("#clearBtn").addEventListener("click", async () => { await api().ClearFinished(); refresh(); });

  const info = await api().Info();
  const cli = $("#cliInfo");
  if (info.cliError) { cli.textContent = info.cliError; cli.classList.add("bad"); }
  else cli.textContent = `CLI ${info.cliVersion || ""} · ${info.cli}`;
  cli.title = "이 앱은 mndl CLI를 실행해서 동작함. 터미널에서도 같은 설정으로 쓸 수 있음.";

  try { cfg = await api().GetConfig(); } catch (e) { msg(String(e)); }
  await loadNets();
  // first run: preselect physical networks that are up
  if (!(cfg.networks || []).length) {
    const phys = nets.filter((n) => !n.virtual).map((n) => n.id);
    if (phys.length) { cfg.networks = phys; await saveCfg(); }
  }
  renderCfg();
  renderNets();
  if ((cfg.networks || []).length) testNets();

  // drop a link from the browser onto the window
  document.addEventListener("dragover", (e) => e.preventDefault());
  document.addEventListener("drop", (e) => {
    const t = e.dataTransfer && (e.dataTransfer.getData("text/uri-list") || e.dataTransfer.getData("text/plain"));
    if (!t || !/:\/\//.test(t)) return;
    e.preventDefault();
    const urls = t.split(/\s+/).filter((x) => /^[a-z]+:\/\//i.test(x) && !x.startsWith("#"));
    const box = $("#urls");
    box.value = (box.value.trim() ? box.value.trim() + "\n" : "") + urls.join("\n");
    autosize();
    box.focus();
  });

  window.runtime.EventsOn("downloads", render);
  refresh();
  $("#urls").focus();
}
window.addEventListener("DOMContentLoaded", init);
