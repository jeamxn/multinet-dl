"use strict";
// Thin UI over the mndl CLI. Every call goes to Go (app.go), which runs the CLI.
const api = () => window.go.main.App;
const $ = (s, r = document) => r.querySelector(s);

// One hue per network, kept for that network everywhere and across launches.
const HUES = ["#2f7cf6", "#ee8a2f", "#2ea36b", "#9a5bd6", "#e0457b", "#139fae", "#c79a00", "#6b7a8f"];
const KIND = { wifi: "Wi-Fi", ethernet: "유선", usb: "USB 테더링", vpn: "VPN", virtual: "가상 어댑터", other: "기타" };
const STATE = { starting: "시작하는 중", probing: "주소 확인 중", downloading: "받는 중", pausing: "멈추는 중", paused: "일시정지", done: "완료", error: "실패", canceled: "취소됨", queued: "대기" };
const RUNNING = ["starting", "probing", "downloading", "pausing"];
const ICON = {
  pause: '<path d="M5.5 3.5v9M10.5 3.5v9"/>',
  play: '<path d="M5 3.2v9.6L12.5 8z"/>',
  retry: '<path d="M2.5 8a5.5 5.5 0 1 0 1.6-3.9M2.5 2.5v3h3"/>',
  reveal: '<path d="M2 8s2.2-4.5 6-4.5S14 8 14 8s-2.2 4.5-6 4.5S2 8 2 8z"/><circle cx="8" cy="8" r="1.8"/>',
};

const S = {
  nets: [],
  cfg: { dir: "", networks: [], conns: 8, note: "" },
  tests: {},
  items: [],
  sel: null,
  showVirtual: localStorage.getItem("showVirtual") === "1",
  hues: JSON.parse(localStorage.getItem("hues") || "{}"),
  hist: new Map(), // item id -> [{t, done, per:{netId:speed}}]
  os: "",
};

function hue(id) {
  if (!(id in S.hues)) {
    const used = new Set(Object.values(S.hues));
    let i = 0;
    while (used.has(i) && i < HUES.length) i++;
    S.hues[id] = i < HUES.length ? i : Object.keys(S.hues).length % HUES.length;
    localStorage.setItem("hues", JSON.stringify(S.hues));
  }
  return HUES[S.hues[id]];
}
function bytes(n, digits) {
  if (n == null || n < 0) return "–";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) { n /= 1024; i++; }
  const d = digits ?? (i === 0 ? 0 : n >= 100 ? 0 : n >= 10 ? 1 : 2);
  return n.toFixed(d) + " " + u[i];
}
const rate = (b) => (b > 0 ? bytes(b) + "/s" : "–");
function dur(sec) {
  if (!isFinite(sec) || sec < 0) return "–";
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  if (h) return `${h}시간 ${m}분`;
  if (m) return `${m}분 ${s}초`;
  return `${s}초`;
}
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const svg = (name) => `<svg viewBox="0 0 16 16">${ICON[name]}</svg>`;
function msg(t) { $("#formMsg").textContent = t ? String(t).replace(/^Error: /, "") : ""; }
function shortName(u) {
  try { const p = new URL(u).pathname.split("/").filter(Boolean); return decodeURIComponent(p[p.length - 1] || new URL(u).host); } catch { return u; }
}
const nameOf = (it) => it.fileName || shortName(it.url);
const etaOf = (it) => (it.state === "downloading" && it.size > 0 && it.speed > 0 ? (it.size - it.done) / it.speed : NaN);

// ---------------- networks ----------------
async function loadNets() {
  try { S.nets = (await api().Networks()) || []; } catch (e) { S.nets = []; msg(e); }
  S.nets.forEach((n) => hue(n.id));
  renderNets();
}

function liveSpeedByNet() {
  const m = {};
  for (const it of S.items) if (it.state === "downloading") for (const n of it.netStats || []) m[n.id] = (m[n.id] || 0) + (n.speed || 0);
  return m;
}

function renderNets() {
  const ul = $("#nets");
  const sel = new Set(S.cfg.networks || []);
  const hidden = S.nets.filter((n) => n.virtual && !sel.has(n.id));
  const shown = S.nets.filter((n) => !n.virtual || sel.has(n.id) || S.showVirtual);
  const live = liveSpeedByNet();
  ul.innerHTML = "";
  if (!shown.length) {
    ul.innerHTML = `<li class="note quiet">연결된 네트워크가 없어요. Wi-Fi, 테더링, 랜선 중 하나를 연결하고 다시 찾아보세요.</li>`;
  }
  for (const n of shown) {
    const on = sel.has(n.id);
    const t = S.tests[n.id];
    let meta = `${KIND[n.kind] || n.kind}, ${esc((n.addrs || [])[0] || n.id)}`;
    if (t === "wait") meta = "연결 확인 중";
    else if (t && t.ok) meta = `<span class="ok">인터넷 됨, ${esc(t.ip)} ${t.ms}ms</span>`;
    else if (t) meta = `<span class="bad">이 네트워크로는 인터넷이 안 돼요</span>`;
    const li = document.createElement("li");
    li.className = "net" + (on ? "" : " off");
    li.style.setProperty("--c", hue(n.id));
    li.title = `${n.label} (${n.id})\n${(n.addrs || []).join("\n")}${t && t.error ? "\n\n" + t.error : ""}`;
    li.innerHTML = `
      <span class="swatch"></span>
      <span style="min-width:0">
        <div class="net-name">${esc(n.label)}</div>
        <div class="net-meta">${meta}</div>
      </span>
      <span style="display:flex;align-items:center;gap:8px">
        ${live[n.id] ? `<span class="net-live">${rate(live[n.id])}</span>` : ""}
        <label class="switch"><input type="checkbox" ${on ? "checked" : ""} aria-label="${esc(n.label)} 사용" /><i></i></label>
      </span>`;
    li.querySelector("input").addEventListener("change", (e) => toggleNet(n.id, e.target.checked));
    ul.append(li);
  }
  const vb = $("#virtualBtn");
  const vCount = S.nets.filter((n) => n.virtual).length;
  vb.textContent = !vCount ? "" : S.showVirtual ? "VPN과 가상 어댑터 숨기기" : hidden.length ? `VPN과 가상 어댑터 ${hidden.length}개 더 보기` : "";
}

async function toggleNet(id, on) {
  const set = new Set(S.cfg.networks || []);
  on ? set.add(id) : set.delete(id);
  S.cfg.networks = S.nets.map((n) => n.id).filter((x) => set.has(x)).concat([...set].filter((x) => !S.nets.some((n) => n.id === x)));
  renderNets();
  await saveCfg();
  if (on) testNets([id]);
}

async function testNets(ids) {
  ids = ids || S.cfg.networks || [];
  if (!ids.length) return msg("확인할 네트워크를 먼저 켜주세요");
  ids.forEach((id) => (S.tests[id] = "wait"));
  renderNets();
  try { ((await api().TestNetworks(ids)) || []).forEach((r) => (S.tests[r.id] = r)); }
  catch (e) { ids.forEach((id) => (S.tests[id] = { ok: false, error: String(e) })); }
  renderNets();
}

// ---------------- settings ----------------
let saveTimer;
async function saveCfg() {
  try { S.cfg = await api().SetConfig(S.cfg); } catch (e) { msg(e); }
  renderCfg();
}
function renderCfg() {
  const d = S.cfg.dir || "";
  const parts = d.split(/[\\/]/).filter(Boolean);
  $("#dirName").textContent = parts[parts.length - 1] || d;
  $("#dirPath").textContent = d;
  $("#dirBtn").title = d + "\n눌러서 폴더 바꾸기";
  $("#conns").value = S.cfg.conns;
  $("#connsOut").textContent = S.cfg.conns;
  $("#bindNote").textContent = S.cfg.note || "";
  connsHint();
}
function connsHint() {
  const c = S.cfg.conns, n = (S.cfg.networks || []).length || 1;
  $("#connsHint").textContent = c > 16
    ? `한 파일에 최대 ${c * n}개 연결. 16개를 넘기면 막는 서버가 있어서, 막히면 알아서 줄여요.`
    : `한 파일에 최대 ${c * n}개 연결로 나눠 받아요.`;
}

// ---------------- downloads ----------------
function record(list) {
  const now = Date.now();
  for (const it of list) {
    if (it.state !== "downloading") continue;
    let h = S.hist.get(it.id);
    if (!h) S.hist.set(it.id, (h = []));
    const last = h[h.length - 1];
    if (last && now - last.t < 300) continue;
    const per = {};
    for (const n of it.netStats || []) per[n.id] = n.speed || 0;
    h.push({ t: now, done: it.done, per });
    while (h.length && now - h[0].t > 90000) h.shift();
  }
  for (const id of S.hist.keys()) if (!list.some((i) => i.id === id)) S.hist.delete(id);
}

function onItems(list) {
  S.items = list || [];
  record(S.items);
  if (!S.items.some((i) => i.id === S.sel)) {
    const pick = S.items.find((i) => RUNNING.includes(i.state)) || S.items[0];
    S.sel = pick ? pick.id : null;
  }
  renderList();
  renderInspector();
  renderNets();
}

function miniBar(it) {
  if (!(it.size > 0)) return "";
  return (it.netStats || []).map((n) => `<i style="--c:${hue(n.id)};width:${Math.min(100, (n.bytes / it.size) * 100)}%"></i>`).join("");
}

function stateText(it) {
  if (it.state === "downloading" && it.size > 0) return `${((it.done / it.size) * 100).toFixed(it.done / it.size > 0.995 ? 1 : 0)}%`;
  return STATE[it.state] || it.state;
}

function renderList() {
  const box = $("#list");
  const keep = new Map([...box.children].map((r) => [r.dataset.id, r]));
  S.items.forEach((it, i) => {
    let r = keep.get(it.id);
    if (!r) {
      r = document.createElement("div");
      r.dataset.id = it.id;
      r.setAttribute("role", "option");
      r.addEventListener("mousedown", () => select(it.id));
      r.addEventListener("dblclick", () => act(it.id, "reveal"));
    }
    keep.delete(it.id);
    r.className = "row " + it.state;
    r.setAttribute("aria-selected", String(it.id === S.sel));
    const size = it.size > 0 ? (it.state === "done" ? bytes(it.size) : `${bytes(it.done)} / ${bytes(it.size)}`) : it.done > 0 ? bytes(it.done) : "–";
    const acts = [];
    if (["downloading", "probing", "starting"].includes(it.state)) acts.push(["pause", "일시정지"]);
    else if (it.state === "paused") acts.push(["play", "이어받기"]);
    else if (it.state === "error" || it.state === "canceled") acts.push(["retry", "다시 받기"]);
    if (it.state === "done") acts.push(["reveal", S.os === "windows" ? "탐색기에서 보기" : "Finder에서 보기"]);
    const sig = JSON.stringify([nameOf(it), it.state, stateText(it), size, it.speed | 0, etaOf(it) | 0, it.done, acts.length]);
    if (r.dataset.sig !== sig) {
      r.dataset.sig = sig;
      r.innerHTML = `
        <div class="cell-name">
          <div class="row-title"><span class="row-name" title="${esc(it.path || it.url)}">${esc(nameOf(it))}</span><span class="row-state ${it.state}">${esc(stateText(it))}</span></div>
          <div class="mini">${miniBar(it)}</div>
        </div>
        <span class="num">${size}</span>
        <span class="num">${it.state === "downloading" ? rate(it.speed) : ""}</span>
        <span class="num">${it.state === "downloading" ? dur(etaOf(it)) : ""}</span>
        <span class="row-acts">${acts.map(([k, t]) => `<button class="row-act" data-act="${k}" title="${t}" aria-label="${t}">${svg(k)}</button>`).join("")}</span>`;
      r.querySelectorAll("[data-act]").forEach((b) => {
        b.addEventListener("mousedown", (e) => e.stopPropagation());
        b.addEventListener("click", (e) => { e.stopPropagation(); act(it.id, b.dataset.act); });
      });
    }
    if (box.children[i] !== r) box.insertBefore(r, box.children[i] || null);
  });
  keep.forEach((r) => r.remove());

  const active = S.items.filter((i) => i.state === "downloading");
  const total = active.reduce((s, i) => s + (i.speed || 0), 0);
  $("#summary").textContent = active.length ? `${active.length}개 받는 중, 합계 ${rate(total)}` : "";
  $("#clearBtn").hidden = !S.items.some((i) => i.state === "done" || i.state === "canceled");
}

function select(id) {
  if (S.sel === id) return;
  S.sel = id;
  renderList();
  renderInspector(true);
}

async function act(id, what) {
  try {
    if (what === "pause") await api().Pause(id);
    else if (what === "play" || what === "retry") await api().Resume(id);
    else if (what === "cancel") await api().Cancel(id);
    else if (what === "remove") await api().Remove(id);
    else if (what === "reveal") await api().Reveal(id);
  } catch (e) { msg(e); }
  refresh();
}

// ---------------- inspector ----------------
let insFor = null;
function renderInspector(force) {
  const box = $("#inspector");
  const it = S.items.find((i) => i.id === S.sel);
  if (!it) {
    insFor = null;
    box.innerHTML = `<div class="ins-empty">
      <h3>파일 하나를 여러 네트워크로 나눠 받아요</h3>
      <p>왼쪽에서 쓸 네트워크를 켜고, 위에 주소를 붙여넣은 뒤 받기를 누르세요. 받는 동안 여기서 파일의 어느 부분을 어느 네트워크가 받고 있는지 볼 수 있어요.</p>
      <p>내장 Wi-Fi는 한 번에 하나에만 붙어요. 두 번째 네트워크는 휴대폰 USB 테더링이나 USB 랜 어댑터로 더해주세요.</p>
      <p>터미널에서는 <code>mndl get 주소</code>로 같은 설정을 쓸 수 있어요.</p>
    </div>`;
    return;
  }
  if (force || insFor !== it.id || !box.querySelector(".ins-head")) {
    insFor = it.id;
    box.innerHTML = `<div class="ins-head"></div><div class="map-zone"></div><div class="lanes"></div><dl class="facts"></dl><div class="ins-msgs"></div><div class="ins-actions"></div><dl class="where"></dl>`;
  }
  const size = it.size > 0 ? it.size : 0;
  const pct = size ? (it.done / size) * 100 : it.state === "done" ? 100 : 0;
  const pctTxt = pct >= 99.95 && it.state !== "done" ? "99.9" : pct >= 10 ? pct.toFixed(1) : pct.toFixed(1);

  // head
  let sub = STATE[it.state] || it.state;
  if (it.state === "downloading") sub = size ? `${bytes(it.done)} / ${bytes(size)} 받음` : `${bytes(it.done)} 받음`;
  if (it.state === "done") sub = `${bytes(it.size)}, ${dur((it.finishedAt - (it.startedAt || it.createdAt)) / 1000)} 걸림`;
  if (it.state === "paused" && size) sub = `${bytes(it.done)} / ${bytes(size)}에서 멈춤`;
  $(".ins-head", box).innerHTML = `
    <div class="ins-title"><h3 class="selectable" title="${esc(it.path || nameOf(it))}">${esc(nameOf(it))}</h3><div class="ins-sub">${esc(sub)}</div></div>
    <div class="ins-big"><div class="ins-pct">${it.state === "done" ? "100" : pctTxt}<small>%</small></div>
    <div class="ins-speed">${it.state === "downloading" ? `${rate(it.speed)}, ${dur(etaOf(it))} 남음` : "&nbsp;"}</div></div>`;

  renderMap($(".map-zone", box), it);
  renderLanes($(".lanes", box), it);
  renderFacts($(".facts", box), it);

  const msgs = [];
  if (it.state === "error") msgs.push(`<div class="ins-error">${esc(it.error || "알 수 없는 이유로 멈췄어요")}</div>`);
  if ((it.warnings || []).length) msgs.push(`<div class="ins-warn">${esc(it.warnings.join(" / "))}</div>`);
  if (it.throttled > 0) msgs.push(`<div class="ins-warn">서버가 동시 연결을 막아서 연결 ${it.throttled}개를 줄였어요. 연결 수를 낮춰도 속도는 비슷할 거예요.</div>`);
  setHTML($(".ins-msgs", box), msgs.join(""));

  const reveal = S.os === "windows" ? "탐색기에서 보기" : "Finder에서 보기";
  const btns = [];
  if (["downloading", "probing", "starting"].includes(it.state)) btns.push(["pause", "일시정지", ""], ["cancel", "취소하고 받던 파일 지우기", "danger"]);
  else if (it.state === "paused") btns.push(["play", "이어받기", "primary"], ["cancel", "취소하고 받던 파일 지우기", "danger"]);
  else if (it.state === "error") btns.push(["retry", "받은 데까지 이어서 다시 시도", "primary"], ["remove", "목록에서 지우기", "danger"]);
  else if (it.state === "canceled") btns.push(["retry", "처음부터 다시 받기", "primary"], ["remove", "목록에서 지우기", ""]);
  else if (it.state === "done") btns.push(["reveal", reveal, "primary"], ["remove", "목록에서 지우기 (파일은 그대로)", ""]);
  const actSig = JSON.stringify(btns);
  const ab = $(".ins-actions", box);
  if (ab.dataset.sig !== actSig || ab.dataset.id !== it.id) {
    ab.dataset.sig = actSig;
    ab.dataset.id = it.id;
    ab.innerHTML = btns.map(([k, t, c]) => `<button class="btn ${c}" data-act="${k}">${t}</button>`).join("");
    ab.querySelectorAll("button").forEach((b) => b.addEventListener("click", () => act(it.id, b.dataset.act)));
  }

  const rows = [["저장 위치", it.path || it.dir, ""], ["주소", it.url, ""]];
  if (it.finalUrl && it.finalUrl !== it.url) rows.push(["실제로 받는 주소 (리다이렉트됨)", it.finalUrl, ""]);
  if (it.command) rows.push(["실행한 명령", it.command, "cmd"]);
  setHTML($(".where", box), rows.map(([k, v, c]) => `<div><dt>${k}</dt><dd class="${c}">${esc(v)}</dd></div>`).join(""));
}

// only touch the DOM when content changed, so text selection survives updates
function setHTML(el, html) { if (el.dataset.h !== html) { el.dataset.h = html; el.innerHTML = html; } }

function renderMap(zone, it) {
  const map = it.map || [];
  const nets = it.netStats || [];
  if (!map.length) {
    // server without Range support, or not probed yet
    const c = nets[0] ? hue(nets[0].id) : "var(--select)";
    const w = it.size > 0 ? (it.done / it.size) * 100 : it.state === "done" ? 100 : 0;
    let note = "";
    if (["starting", "probing"].includes(it.state)) note = "서버에 파일 크기와 나눠 받기 지원 여부를 물어보는 중이에요.";
    else if (!it.rangeOK && it.state !== "canceled") note = "이 서버는 나눠 받기를 지원하지 않아서 네트워크 하나로 차례대로 받아요.";
    setHTML(zone, `<div class="single-bar"><i style="--c:${c};width:${w}%"></i></div>${note ? `<div class="single-note">${note}</div>` : ""}`);
    return;
  }
  let grid = $(".map", zone);
  const cols = Math.min(48, map.length);
  if (!grid || grid.childElementCount !== map.length) {
    zone.dataset.h = "";
    zone.innerHTML = `<div class="map-wrap"><div class="map" role="img" style="--cols:${cols}"></div></div><div class="map-legend"></div>`;
    grid = $(".map", zone);
    grid.innerHTML = "<i></i>".repeat(map.length);
  }
  const live = new Map();
  if (it.size > 0) for (const c of it.cursors || []) live.set(Math.min(map.length - 1, Math.floor((c.pos / it.size) * map.length)), c.net);
  const cells = grid.children;
  let prev = false;
  for (let i = 0; i < map.length; i++) {
    const [owner, fill] = map[i];
    const el = cells[i];
    const color = owner >= 0 && nets[owner] ? hue(nets[owner].id) : "";
    if (owner === -2) prev = true;
    el.className = owner === -2 ? "prev" : "";
    if (color) el.style.setProperty("--c", color); else if (owner !== -2) el.style.removeProperty("--c");
    el.style.setProperty("--f", String(fill / 100));
    if (live.has(i)) {
      const n = nets[live.get(i)];
      el.classList.add("live");
      el.style.setProperty("--c", n ? hue(n.id) : "var(--ink-2)");
    }
  }
  const cellSize = it.size / map.length;
  grid.setAttribute("aria-label", `파일을 ${map.length}칸으로 나눈 지도, 한 칸에 ${bytes(cellSize)}`);
  const legend = nets.map((n) => `<span style="--c:${hue(n.id)}"><b></b>${esc(n.label)}</span>`);
  if (prev) legend.push(`<span style="--c:var(--prev-cell)"><b></b>전에 받아둔 부분</span>`);
  if (it.state === "downloading") legend.push(`<span class="live-key"><b></b>지금 받는 자리 ${(it.cursors || []).length}곳</span>`);
  legend.push(`<span style="color:var(--ink-3)">한 칸에 ${bytes(cellSize)}</span>`);
  setHTML($(".map-legend", zone), legend.join(""));
}

function spark(points, max) {
  // points: [{t, v}] within the last 60s
  const W = 100, H = 26, now = Date.now();
  if (points.length < 2 || !(max > 0)) return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none"></svg>`;
  const xy = points.map((p) => [W - ((now - p.t) / 60000) * W, H - 1 - (p.v / max) * (H - 3)]);
  const line = xy.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(2)},${y.toFixed(2)}`).join("");
  const area = `${line}L${xy[xy.length - 1][0].toFixed(2)},${H}L${xy[0][0].toFixed(2)},${H}Z`;
  return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" aria-hidden="true"><path class="area" d="${area}"/><path class="line" d="${line}"/></svg>`;
}

function renderLanes(box, it) {
  const nets = it.netStats || [];
  if (!nets.length) { setHTML(box, ""); return; }
  const h = (S.hist.get(it.id) || []).filter((p) => Date.now() - p.t <= 60000);
  let max = 0;
  for (const p of h) for (const v of Object.values(p.per)) max = Math.max(max, v);
  const running = it.state === "downloading";
  const total = it.size > 0 ? it.size : nets.reduce((s, n) => s + n.bytes, 0) || 1;
  const rows = nets.map((n) => {
    const c = hue(n.id);
    const share = (n.bytes / total) * 100;
    const slots = Array.from({ length: Math.min(n.conns || 0, 64) }, (_, i) => `<i class="${i < n.active ? "on" : ""}"></i>`).join("");
    const pts = h.map((p) => ({ t: p.t, v: p.per[n.id] || 0 }));
    const errs = n.failed ? `<div class="lane-err">이 네트워크는 끊겨서 빠졌어요. 나머지는 다른 네트워크가 받아요. ${esc(n.lastError || "")}</div>`
      : n.retries && n.lastError ? `<div class="lane-err" style="color:var(--ink-3)">다시 시도 ${n.retries}번, 마지막 오류: ${esc(n.lastError)}</div>` : "";
    return `<div class="lane${n.failed ? " failed" : ""}" style="--c:${c}">
      <div class="lane-name"><span class="swatch"></span><span>${esc(n.label)}</span><small>${esc(n.id)}</small></div>
      <div>${running || h.length ? spark(pts, max) : ""}</div>
      <div class="v"><b>${running ? rate(n.speed) : "–"}</b></div>
      <div class="v">${rate(n.peak)}</div>
      <div class="share"><div class="share-bar"><i style="width:${Math.min(100, share)}%"></i></div><span>${bytes(n.bytes)}, ${share.toFixed(share < 10 ? 1 : 0)}%</span></div>
      <div class="slots" title="열린 연결 ${n.active} / 최대 ${n.conns}">${running ? slots : `<span class="v">${n.conns || ""}</span>`}</div>
      ${errs}
    </div>`;
  });
  setHTML(box, `<div class="lanes-head"><span>네트워크</span><span>최근 1분</span><span class="r">지금</span><span class="r">최고</span><span class="r">맡은 양</span><span class="r">연결</span></div>${rows.join("")}`);
}

function renderFacts(box, it) {
  const h = S.hist.get(it.id) || [];
  const nets = it.netStats || [];
  const now = it.state === "done" && it.finishedAt ? it.finishedAt : Date.now();
  const started = it.startedAt || it.createdAt;
  const elapsed = (now - started) / 1000;
  let avg = NaN;
  if (it.state === "done") avg = it.size / Math.max(elapsed, 0.001);
  else if (h.length > 1) avg = (h[h.length - 1].done - h[0].done) / ((h[h.length - 1].t - h[0].t) / 1000);
  const active = nets.reduce((s, n) => s + (n.active || 0), 0);
  const maxConns = nets.reduce((s, n) => s + (n.conns || 0), 0);
  const retries = nets.reduce((s, n) => s + (n.retries || 0), 0);
  const running = it.state === "downloading";
  const f = [
    ["받은 양", it.size > 0 ? `${bytes(it.done)} / ${bytes(it.size)}` : bytes(it.done)],
    ["남은 양", it.size > 0 ? bytes(Math.max(0, it.size - it.done)) : "–"],
    [running ? "남은 시간" : "걸린 시간", running ? dur(etaOf(it)) : it.state === "done" ? dur(elapsed) : "–"],
    [running ? "지난 시간" : "시작", running ? dur(elapsed) : new Date(it.createdAt).toLocaleString()],
    [it.state === "done" ? "평균 속도" : "최근 평균 속도", rate(avg)],
    ["최고 속도", rate(it.peak)],
    ["연결", running ? `${active}개 열림, 최대 ${maxConns}개` : `네트워크당 ${it.conns}개`],
    ["남은 조각", it.rangeOK && it.state !== "done" ? `${it.pieces}개` : "–"],
    ["다시 시도", retries ? `${retries}번` : "없음"],
    ["이어받기", it.state === "starting" || it.state === "probing" ? "확인 중" : it.rangeOK ? "지원함" : "지원 안 함"],
  ];
  setHTML(box, f.map(([k, v]) => `<div><dt>${k}</dt><dd>${esc(v)}</dd></div>`).join(""));
}

async function refresh() { try { onItems(await api().List()); } catch {} }

// ---------------- input ----------------
async function submit(e) {
  e && e.preventDefault();
  msg("");
  const urls = $("#urls").value.split(/\s+/).map((s) => s.trim()).filter(Boolean);
  const headers = $("#headers").value.split("\n").map((s) => s.trim()).filter(Boolean);
  if (!urls.length) { $("#urls").focus(); return msg("다운로드 주소를 넣어주세요"); }
  $("#goBtn").disabled = true;
  try {
    const ids = await api().Add({ urls, fileName: $("#fileName").value.trim(), headers });
    $("#urls").value = "";
    $("#fileName").value = "";
    autosize();
    if (ids && ids.length) S.sel = ids[ids.length - 1];
  } catch (err) { msg(err); }
  finally { $("#goBtn").disabled = false; refresh(); }
}
function autosize() {
  const t = $("#urls");
  t.style.height = "27px";
  t.style.height = Math.min(120, Math.max(27, t.scrollHeight)) + "px";
  const n = t.value.trim().split(/\s+/).filter(Boolean).length;
  $("#goBtn").textContent = n > 1 ? `${n}개 받기` : "받기";
  $("#fileName").disabled = n > 1;
  $("#fileName").placeholder = n > 1 ? "주소가 여러 개면 서버가 알려주는 이름으로 저장해요" : "비워두면 서버가 알려주는 이름";
}

function keyList(e) {
  const i = S.items.findIndex((x) => x.id === S.sel);
  if (e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    const n = S.items[Math.max(0, Math.min(S.items.length - 1, i + (e.key === "ArrowDown" ? 1 : -1)))];
    if (n) { select(n.id); $(`.row[data-id="${n.id}"]`)?.scrollIntoView({ block: "nearest" }); }
  } else if (e.key === " " && i >= 0) {
    e.preventDefault();
    const it = S.items[i];
    if (["downloading", "probing", "starting"].includes(it.state)) act(it.id, "pause");
    else if (["paused", "error"].includes(it.state)) act(it.id, "play");
  } else if ((e.key === "Backspace" || e.key === "Delete") && i >= 0 && !RUNNING.includes(S.items[i].state)) {
    e.preventDefault();
    act(S.items[i].id, "remove");
  } else if (e.key === "Enter" && i >= 0 && S.items[i].state === "done") act(S.items[i].id, "reveal");
}

async function init() {
  const isMac = navigator.userAgent.includes("Mac");
  document.body.classList.toggle("mac", isMac);
  $("#composer").addEventListener("submit", submit);
  $("#urls").addEventListener("input", autosize);
  $("#urls").addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) submit(e); });
  $("#optToggle").addEventListener("click", () => {
    const o = $("#options");
    o.hidden = !o.hidden;
    $("#optToggle").setAttribute("aria-expanded", String(!o.hidden));
    if (!o.hidden) $("#fileName").focus();
  });
  $("#refreshBtn").addEventListener("click", async () => { S.tests = {}; await loadNets(); });
  $("#testBtn").addEventListener("click", () => testNets());
  $("#virtualBtn").addEventListener("click", () => { S.showVirtual = !S.showVirtual; localStorage.setItem("showVirtual", S.showVirtual ? "1" : "0"); renderNets(); });
  $("#conns").addEventListener("input", (e) => { S.cfg.conns = +e.target.value; $("#connsOut").textContent = S.cfg.conns; connsHint(); clearTimeout(saveTimer); saveTimer = setTimeout(saveCfg, 250); });
  $("#dirBtn").addEventListener("click", async () => { try { const d = await api().PickDir(S.cfg.dir); if (d) { S.cfg.dir = d; await saveCfg(); } } catch (e) { msg(e); } });
  $("#dirOpen").addEventListener("click", () => api().OpenDir(S.cfg.dir).catch(msg));
  $("#clearBtn").addEventListener("click", async () => { await api().ClearFinished(); refresh(); });
  $("#list").addEventListener("keydown", keyList);
  document.addEventListener("dragover", (e) => e.preventDefault());
  document.addEventListener("drop", (e) => {
    const t = e.dataTransfer && (e.dataTransfer.getData("text/uri-list") || e.dataTransfer.getData("text/plain"));
    if (!t) return;
    e.preventDefault();
    const urls = t.split(/\s+/).filter((x) => /^[a-z][a-z0-9+.-]*:\/\//i.test(x));
    if (!urls.length) return;
    const box = $("#urls");
    box.value = (box.value.trim() ? box.value.trim() + "\n" : "") + urls.join("\n");
    autosize();
    box.focus();
  });

  const info = await api().Info();
  S.os = info.os;
  if (S.os === "windows") $("#dirOpen").textContent = "탐색기에서 열기";
  if (S.os === "linux") $("#dirOpen").textContent = "파일 관리자에서 열기";
  const cli = $("#cliInfo");
  if (info.cliError) { cli.textContent = info.cliError; cli.classList.add("bad"); }
  else { cli.textContent = `mndl ${info.cliVersion || ""}`; cli.title = `이 앱은 터미널용 mndl을 실행해서 동작해요.\n${info.cli}`; }

  try { S.cfg = await api().GetConfig(); } catch (e) { msg(e); }
  await loadNets();
  if (!(S.cfg.networks || []).length) {
    const phys = S.nets.filter((n) => !n.virtual).map((n) => n.id);
    if (phys.length) { S.cfg.networks = phys; await saveCfg(); }
  }
  renderCfg();
  renderNets();
  if ((S.cfg.networks || []).length) testNets();

  window.runtime.EventsOn("downloads", onItems);
  await refresh();
  setInterval(() => { if (S.items.some((i) => i.state === "downloading")) renderInspector(); }, 1000);
  $("#urls").focus();
}
window.addEventListener("DOMContentLoaded", init);
