'use strict';
const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];
const enc = encodeURIComponent;

let ME = null, USERS = [], OTHER = null, NAMES = {}, LOCAL = false;
const esc = (t) => String(t ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
let PHOTOS = [], BY = {}, SCORES = {}, FINALS = {};
let list = [];          // filtered photo names (snapshot)
let idx = 0;
let filter = { basis: 'any', min: 0, finalOnly: false, unrated: false };
let draft = null;       // filter being edited in the sheet

// ------------------------------------------------------------------ api
async function api(path, body) {
  const r = await fetch(path, body === undefined ? {} : {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  });
  if (r.status === 401) { showLogin(); throw new Error('unauthorized'); }
  const j = await r.json();
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}
function applyState(s) { SCORES = s.scores || {}; FINALS = s.finals || {}; }

// ------------------------------------------------------------------ boot / login
async function boot() {
  const me = await (await fetch('/api/me')).json();
  USERS = me.people.map((p) => p.id);
  NAMES = Object.fromEntries(me.people.map((p) => [p.id, p.name]));
  LOCAL = me.local;
  $('#loginTitle').textContent = me.title;
  if (!me.configured) { showLogin(); $('#loginForm').innerHTML = '<h1>아직 준비 중이에요</h1><p class="muted">관리 화면에서 이름과 비밀번호를 먼저 설정해 주세요.</p>'; return; }
  document.title = me.title;
  if (!me.user) return showLogin();
  ME = me.user; OTHER = USERS.find((u) => u !== ME);
  const data = await api('/api/photos');
  PHOTOS = data.photos; BY = Object.fromEntries(PHOTOS.map((p) => [p.name, p]));
  applyState(data);
  try { Object.assign(filter, JSON.parse(localStorage.getItem('ws.filter.' + ME) || '{}')); } catch {}
  buildScoreRows();
  rebuildList(localStorage.getItem('ws.cur.' + ME));
  showScreen('viewer');
  layoutAll();
  setInterval(poll, 8000);
}

function showScreen(id) {
  $$('.screen').forEach((s) => s.classList.toggle('hidden', s.id !== id));
}

function showLogin() { showScreen('login'); $('#pw').focus(); }
$('#loginForm').onsubmit = async (e) => {
  e.preventDefault();
  $('#loginErr').textContent = '';
  try {
    const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: $('#pw').value }) });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error);
    location.reload();
  } catch (err) { $('#loginErr').textContent = err.message; }
};

// ------------------------------------------------------------------ filtering
const BASIS = () => [
  ['any', '둘 중 하나'], ['both', '둘 다'], ['avg', '평균'], [USERS[0], NAMES[USERS[0]]], [USERS[1], NAMES[USERS[1]]],
];
function passes(name, f) {
  const sc = SCORES[name] || {};
  if (f.finalOnly && !FINALS[name]) return false;
  if (f.unrated && sc[ME]) return false;
  if (!f.min) return true;
  const a = sc[USERS[0]] || 0, b = sc[USERS[1]] || 0;
  switch (f.basis) {
    case 'both': return a >= f.min && b >= f.min;
    case 'any': return a >= f.min || b >= f.min;
    case 'avg': { const v = [a, b].filter(Boolean); return v.length > 0 && v.reduce((x, y) => x + y) / v.length >= f.min; }
    default: return (sc[f.basis] || 0) >= f.min;
  }
}
function filterActive(f) { return f.min > 0 || f.finalOnly || f.unrated; }
function rebuildList(keepName) {
  list = PHOTOS.map((p) => p.name).filter((n) => passes(n, filter));
  let i = keepName ? list.indexOf(keepName) : -1;
  if (i < 0 && keepName && BY[keepName]) {
    // nearest following photo in the full order
    const order = PHOTOS.findIndex((p) => p.name === keepName);
    i = list.findIndex((n) => PHOTOS.findIndex((p) => p.name === n) >= order);
    if (i < 0) i = list.length - 1;
  }
  idx = Math.max(0, i);
  $('#filterDot').classList.toggle('hidden', !filterActive(filter));
  renderAllSlides();
}
function filterLabel(f) {
  if (!filterActive(f)) return '전체';
  const parts = [];
  if (f.min) parts.push(`${(BASIS().find((b) => b[0] === f.basis) || [0, f.basis])[1]} ${f.min}점↑`);
  if (f.finalOnly) parts.push('♥만');
  if (f.unrated) parts.push('안 본 것');
  return parts.join(' · ');
}

function openFilter() {
  draft = { ...filter };
  const basis = $('#fBasis'); basis.innerHTML = '';
  BASIS().forEach(([k, label]) => {
    const b = document.createElement('button'); b.textContent = label; b.dataset.k = k;
    b.onclick = () => { draft.basis = k; drawFilter(); };
    basis.appendChild(b);
  });
  const mn = $('#fMin'); mn.innerHTML = '';
  [0, 1, 2, 3, 4, 5].forEach((v) => {
    const b = document.createElement('button'); b.textContent = v ? v + '↑' : '전체'; b.dataset.v = v;
    b.onclick = () => { draft.min = v; drawFilter(); };
    mn.appendChild(b);
  });
  $$('#filterSheet .tog').forEach((b) => { b.onclick = () => { draft[b.dataset.opt] = !draft[b.dataset.opt]; drawFilter(); }; });
  drawFilter();
  $('#filterSheet').classList.remove('hidden');
}
function drawFilter() {
  $$('#fBasis button').forEach((b) => b.classList.toggle('on', b.dataset.k === draft.basis));
  $$('#fMin button').forEach((b) => b.classList.toggle('on', +b.dataset.v === draft.min));
  $$('#filterSheet .tog').forEach((b) => b.classList.toggle('on', !!draft[b.dataset.opt]));
  $('#fMinLbl').textContent = draft.min ? `${draft.min}점 이상` : '';
  const n = PHOTOS.filter((p) => passes(p.name, draft)).length;
  $('#fPreview').textContent = `(${n}장)`;
}
$('#fApply').onclick = () => {
  filter = draft; localStorage.setItem('ws.filter.' + ME, JSON.stringify(filter));
  closeSheets(); rebuildList(list[idx]);
  if (!$('#grid').classList.contains('hidden')) renderGrid();
  toast(filterLabel(filter) + ` · ${list.length}장`);
};
$('#fReset').onclick = () => { draft = { basis: 'any', min: 0, finalOnly: false, unrated: false }; drawFilter(); };

// ------------------------------------------------------------------ score UI
function starRow(user, mine) {
  const row = document.createElement('div');
  row.className = 'row ' + (mine ? 'me' : 'other');
  row.dataset.user = user;
  row.innerHTML = `<span class="who">${esc(NAMES[user])}</span><div class="stars"></div>`;
  const st = row.querySelector('.stars');
  for (let n = 1; n <= 5; n++) {
    const b = document.createElement('button'); b.textContent = '★'; b.dataset.n = n;
    if (mine) b.onclick = () => setScore(n);
    else b.tabIndex = -1;
    st.appendChild(b);
  }
  return row;
}
function buildScoreRows() {
  const box = $('#scoreRows'); box.innerHTML = '';
  box.appendChild(starRow(ME, true));
  if (OTHER) box.appendChild(starRow(OTHER, false));
}
function updateUI() {
  const name = list[idx];
  $('#empty').classList.toggle('hidden', list.length > 0);
  $('#counter').textContent = list.length ? `${idx + 1} / ${list.length}` : '0 / 0';
  $('#fname').textContent = name ? `${name.replace(/\.[^.]+$/, '')} · ${filterLabel(filter)}` : filterLabel(filter);
  const sc = (name && SCORES[name]) || {};
  $$('#scoreRows .row').forEach((row) => {
    const v = sc[row.dataset.user] || 0;
    row.querySelectorAll('button').forEach((b) => b.classList.toggle('on', +b.dataset.n <= v));
  });
  const fin = !!(name && FINALS[name]);
  $('#btnFinal').classList.toggle('on', fin);
  $('#btnFinal .heart').textContent = fin ? '♥' : '♡';
  $('#finalCount').textContent = `${Object.keys(FINALS).length}장`;
  slides.forEach((s) => { const b = s.querySelector('.badge'); b.textContent = FINALS[s.dataset.name] ? '♥' : ''; });
  if (name) localStorage.setItem('ws.cur.' + ME, name);
}
async function setScore(n) {
  const name = list[idx]; if (!name) return;
  const cur = (SCORES[name] || {})[ME] || 0;
  const v = cur === n ? 0 : n;
  SCORES[name] = { ...(SCORES[name] || {}), [ME]: v || undefined };
  updateUI();
  try { applyState(await api('/api/score', { photo: name, score: v })); updateUI(); }
  catch (e) { toast('저장 실패: ' + e.message); }
}
async function toggleFinal() {
  const name = list[idx]; if (!name) return;
  const on = !FINALS[name];
  if (on) FINALS[name] = ME; else delete FINALS[name];
  updateUI();
  toast(on ? `♥ 최종셀렉 (${Object.keys(FINALS).length}장)` : '최종셀렉 해제');
  try { applyState(await api('/api/final', { photo: name, on })); updateUI(); }
  catch (e) { toast('저장 실패: ' + e.message); }
}
$('#btnFinal').onclick = toggleFinal;

async function poll() {
  if (document.hidden || !ME) return;
  try {
    const s = await api('/api/state'); applyState(s);
    if (s.count !== PHOTOS.length) { // 사진이 추가/삭제됨
      const d = await api('/api/photos');
      PHOTOS = d.photos; BY = Object.fromEntries(PHOTOS.map((p) => [p.name, p])); applyState(d);
      rebuildList(list[idx]); layoutAll();
      if (!$('#grid').classList.contains('hidden')) renderGrid();
    } else updateUI();
  } catch {}
}
document.addEventListener('visibilitychange', poll);

let toastT;
function toast(msg) {
  const t = $('#toast'); t.textContent = msg; t.classList.add('show');
  clearTimeout(toastT); toastT = setTimeout(() => t.classList.remove('show'), 1400);
}

// ------------------------------------------------------------------ slides
const stage = $('#stage'), track = $('#track');
const slides = [-1, 0, 1].map(() => {
  const el = document.createElement('div'); el.className = 'slide';
  el.innerHTML = '<img alt=""><div class="badge"></div>';
  track.appendChild(el); return el;
});
let W = 0, H = 0;
let z = { s: 1, x: 0, y: 0 };   // zoom state of the current slide

function fitOf(name) {
  const p = BY[name]; if (!p) return { w: 0, h: 0 };
  const f = Math.min(W / p.w, H / p.h);
  return { w: p.w * f, h: p.h * f };
}
function placeSlides() {
  slides.forEach((el, i) => { el.style.transform = `translateX(${(i - 1) * 100}%)`; });
}
function renderSlide(el, i) {
  const name = list[i];
  const img = el.querySelector('img');
  el.dataset.name = name || '';
  el.dataset.orig = '';
  if (!name) { img.removeAttribute('src'); img.style.display = 'none'; return; }
  img.style.display = '';
  const f = fitOf(name);
  img.style.width = f.w + 'px'; img.style.height = f.h + 'px';
  img.style.transform = 'translate(-50%,-50%)';
  const src = '/img/view/' + enc(name);
  if (img.getAttribute('src') !== src) img.src = src;
}
function renderAllSlides() {
  z = { s: 1, x: 0, y: 0 };
  slides.forEach((el, i) => renderSlide(el, idx + i - 1));
  placeSlides(); track.style.transition = ''; track.style.transform = '';
  prefetch(); updateUI();
}
function prefetch() {
  [2, -2, 3].forEach((d) => { const n = list[idx + d]; if (n) new Image().src = '/img/view/' + enc(n); });
}
function layoutAll() {
  W = stage.clientWidth; H = stage.clientHeight;
  slides.forEach((el, i) => {
    const n = el.dataset.name; if (!n) return;
    const f = fitOf(n), img = el.querySelector('img');
    img.style.width = f.w + 'px'; img.style.height = f.h + 'px';
  });
  clampZ(); applyZ();
}
window.addEventListener('resize', layoutAll);

const cur = () => slides[1];
function applyZ(anim) {
  const img = cur().querySelector('img');
  img.style.transition = anim ? 'transform .2s ease-out' : '';
  img.style.transform = `translate(-50%,-50%) translate(${z.x}px,${z.y}px) scale(${z.s})`;
  if (z.s >= 1.8) upgradeOrig();
}
function clampZ() {
  const n = cur().dataset.name; if (!n) return;
  const f = fitOf(n);
  const mx = Math.max(0, (f.w * z.s - W) / 2), my = Math.max(0, (f.h * z.s - H) / 2);
  z.x = Math.min(mx, Math.max(-mx, z.x)); z.y = Math.min(my, Math.max(-my, z.y));
}
function zoomTo(s, px, py, anim) {     // px,py: point relative to stage center that stays fixed
  s = Math.min(8, Math.max(1, s));
  z.x = px - (px - z.x) * (s / z.s); z.y = py - (py - z.y) * (s / z.s); z.s = s;
  clampZ(); applyZ(anim);
}
function upgradeOrig() {
  const el = cur(), n = el.dataset.name;
  if (!n || el.dataset.orig) return;
  el.dataset.orig = '1';
  const url = '/img/orig/' + enc(n), pre = new Image();
  pre.onload = () => { if (el.dataset.name === n) el.querySelector('img').src = url; };
  pre.src = url;
}

let animating = false;
function go(dir) {
  if (animating) return;
  const t = idx + dir;
  if (t < 0 || t >= list.length) { bounce(dir); return; }
  animating = true;
  track.style.transition = 'transform .22s ease-out';
  track.style.transform = `translateX(${-dir * W}px)`;
  setTimeout(() => {
    idx = t;
    if (dir > 0) slides.push(slides.shift()); else slides.unshift(slides.pop());
    // reset zoom on the slide we left and render the newly exposed edge slide
    const edge = dir > 0 ? slides[2] : slides[0];
    renderSlide(edge, idx + dir);
    const old = dir > 0 ? slides[0] : slides[2];
    renderSlide(old, idx - dir);
    z = { s: 1, x: 0, y: 0 };
    track.style.transition = ''; track.style.transform = '';
    placeSlides(); applyZ(); prefetch(); updateUI();
    animating = false;
  }, 230);
}
function bounce(dir) {
  track.style.transition = 'transform .15s ease-out';
  track.style.transform = `translateX(${-dir * 40}px)`;
  setTimeout(() => { track.style.transform = ''; }, 150);
  toast(dir > 0 ? '마지막 사진이에요' : '첫 사진이에요');
}
function jump(i) { idx = Math.max(0, Math.min(list.length - 1, i)); renderAllSlides(); }

// ------------------------------------------------------------------ gestures
const ptrs = new Map();
let g = null;            // gesture state
let lastTap = { t: 0, x: 0, y: 0 };
const rel = (p) => { const r = stage.getBoundingClientRect(); return { x: p.x - r.left - r.width / 2, y: p.y - r.top - r.height / 2 }; };

function startSingle(p) {
  g = { mode: z.s > 1.01 ? 'pan' : 'swipe', sx: p.x, sy: p.y, zx: z.x, zy: z.y, t: performance.now(), moved: 0 };
}
function startPinch() {
  const [a, b] = [...ptrs.values()];
  const m = rel({ x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
  g = { mode: 'pinch', d0: Math.hypot(a.x - b.x, a.y - b.y) || 1, mx: m.x, my: m.y, s0: z.s, x0: z.x, y0: z.y, moved: 99 };
  track.style.transition = 'transform .15s'; track.style.transform = '';
}
stage.addEventListener('pointerdown', (e) => {
  if (animating) return;
  stage.setPointerCapture(e.pointerId);
  ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
  if (ptrs.size === 1) startSingle({ x: e.clientX, y: e.clientY });
  else if (ptrs.size === 2) startPinch();
});
stage.addEventListener('pointermove', (e) => {
  if (!ptrs.has(e.pointerId) || !g) return;
  ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
  if (g.mode === 'pinch' && ptrs.size >= 2) {
    const [a, b] = [...ptrs.values()];
    const d = Math.hypot(a.x - b.x, a.y - b.y);
    const m = rel({ x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
    const s = Math.min(8, Math.max(0.6, g.s0 * d / g.d0));
    z.s = s; z.x = m.x - (g.mx - g.x0) * (s / g.s0); z.y = m.y - (g.my - g.y0) * (s / g.s0);
    const img = cur().querySelector('img');
    img.style.transition = '';
    img.style.transform = `translate(-50%,-50%) translate(${z.x}px,${z.y}px) scale(${z.s})`;
    return;
  }
  const dx = e.clientX - g.sx, dy = e.clientY - g.sy;
  g.moved = Math.max(g.moved, Math.hypot(dx, dy));
  if (g.mode === 'swipe') {
    let x = dx;
    if ((idx === 0 && dx > 0) || (idx === list.length - 1 && dx < 0)) x = dx / 3;
    track.style.transition = ''; track.style.transform = `translateX(${x}px)`;
    g.lastX = dx; g.lastT = performance.now();
    g.vx = g.prevX !== undefined ? (dx - g.prevX) / Math.max(1, g.lastT - g.prevT) : 0;
    g.prevX = dx; g.prevT = g.lastT;
  } else if (g.mode === 'pan') {
    z.x = g.zx + dx; z.y = g.zy + dy;
    const before = z.x; clampZ();
    g.over = before - z.x;      // how far we pushed past the horizontal edge
    applyZ();
  }
});
function endPointer(e) {
  if (!ptrs.has(e.pointerId)) return;
  ptrs.delete(e.pointerId);
  if (!g) return;
  if (g.mode === 'pinch') {
    if (ptrs.size === 1) { // continue as pan with remaining finger
      const p = [...ptrs.values()][0];
      if (z.s < 1) { z = { s: 1, x: 0, y: 0 }; }
      clampZ(); applyZ(true);
      g = { mode: z.s > 1.01 ? 'pan' : 'swipe', sx: p.x, sy: p.y, zx: z.x, zy: z.y, t: 0, moved: 99 };
      return;
    }
    if (z.s <= 1.02) z = { s: 1, x: 0, y: 0 };
    clampZ(); applyZ(true); g = null; return;
  }
  if (ptrs.size > 0) return;
  const dt = performance.now() - g.t;
  if (g.moved < 10 && dt < 300) { handleTap(e); g = null; return; }
  if (g.mode === 'swipe') {
    const dx = g.lastX || 0, v = g.vx || 0;
    if (dx < -W * 0.18 || v < -0.45) go(1);
    else if (dx > W * 0.18 || v > 0.45) go(-1);
    else { track.style.transition = 'transform .18s ease-out'; track.style.transform = ''; }
  } else if (g.mode === 'pan') {
    // zoomed in and dragged well past the edge -> next/prev photo
    if (g.over < -90) go(1); else if (g.over > 90) go(-1);
  }
  g = null;
}
stage.addEventListener('pointerup', endPointer);
stage.addEventListener('pointercancel', endPointer);

function handleTap(e) {
  const now = performance.now();
  if (now - lastTap.t < 320 && Math.hypot(e.clientX - lastTap.x, e.clientY - lastTap.y) < 40) {
    const p = rel({ x: e.clientX, y: e.clientY });
    if (z.s > 1.05) { z = { s: 1, x: 0, y: 0 }; applyZ(true); }
    else zoomTo(2.8, p.x, p.y, true);
    lastTap.t = 0; return;
  }
  lastTap = { t: now, x: e.clientX, y: e.clientY };
}

stage.addEventListener('wheel', (e) => {
  e.preventDefault();
  const p = rel({ x: e.clientX, y: e.clientY });
  if (e.ctrlKey || Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
    zoomTo(z.s * Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0025)), p.x, p.y);
  }
}, { passive: false });
// iOS Safari: block native page pinch
['gesturestart', 'gesturechange'].forEach((t) => document.addEventListener(t, (e) => e.preventDefault()));

document.addEventListener('keydown', (e) => {
  if ($('#viewer').classList.contains('hidden') || e.target.tagName === 'INPUT') return;
  if (!$$('.sheet').every((s) => s.classList.contains('hidden'))) { if (e.key === 'Escape') closeSheets(); return; }
  if (e.key === 'ArrowRight' || e.key === 'ArrowDown') go(1);
  else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') go(-1);
  else if (/^[1-5]$/.test(e.key)) setScore(+e.key);
  else if (e.key === '0') { const c = (SCORES[list[idx]] || {})[ME]; if (c) setScore(c); }
  else if (e.key === 'f' || e.key === 'F' || e.key === ' ') { e.preventDefault(); toggleFinal(); }
  else if (e.key === 'g' || e.key === 'G') openGrid();
  else return;
});

// ------------------------------------------------------------------ grid (페이지 단위)
// 한 페이지 = 화면에 딱 맞게 들어가는 만큼 (가로 칸 수 × 세로 줄 수), 화면 크기가 바뀌면 다시 계산
let PAGE = 60;
let gridPage = 0;
const pageCount = () => Math.max(1, Math.ceil(list.length / PAGE));
function fitGrid() {
  const box = $('#gridList'), gap = 3;
  const W = box.clientWidth - gap * 2, H = box.clientHeight - gap * 2;
  if (W <= 0 || H <= 0) return false;
  const minCell = W >= 900 ? 150 : 105;
  const cols = Math.max(3, Math.floor((W + gap) / (minCell + gap)));
  const size = (W - gap * (cols - 1)) / cols;
  const rows = Math.max(2, Math.floor((H + gap) / (size + gap)));
  box.style.gridTemplateColumns = `repeat(${cols}, 1fr)`;
  box.style.gridAutoRows = Math.floor((H - gap * (rows - 1)) / rows) + 'px';   // 세로도 화면에 꽉 차게
  box.classList.add('fit');
  const first = gridPage * PAGE, changed = PAGE !== cols * rows;
  PAGE = cols * rows;
  if (changed) gridPage = Math.floor(first / PAGE);   // 보던 위치 유지
  return changed;
}
function renderGrid() {
  fitGrid();
  gridPage = Math.min(gridPage, pageCount() - 1);
  $('#gridTitle').textContent = `${filterLabel(filter)} · ${list.length}장`;
  const box = $('#gridList'); box.innerHTML = '';
  const frag = document.createDocumentFragment();
  const i0 = esc(NAMES[USERS[0]]), i1 = esc(NAMES[USERS[1]]);
  const from = gridPage * PAGE, to = Math.min(list.length, from + PAGE);
  for (let i = from; i < to; i++) {
    const n = list[i];
    const c = document.createElement('div'); c.className = 'cell' + (i === idx ? ' cur' : '');
    const sc = SCORES[n] || {};
    c.innerHTML = `<img loading="lazy" decoding="async" src="/img/thumb/${enc(n)}" alt="">` +
      `<span class="no">${i + 1}</span>` +
      (FINALS[n] ? '<span class="h">♥</span>' : '') +
      `<div class="tag"><span class="a">${sc[USERS[0]] ? i0 + ' ' + sc[USERS[0]] : ''}</span>` +
      `<span class="b">${USERS[1] && sc[USERS[1]] ? i1 + ' ' + sc[USERS[1]] : ''}</span></div>`;
    c.onclick = () => openAt(i);
    frag.appendChild(c);
  }
  box.appendChild(frag);
  // pager
  const pc = pageCount(), sel = $('#pgSel');
  if (sel.dataset.key !== pc + '/' + PAGE + '/' + list.length) {
    sel.dataset.key = pc + '/' + PAGE + '/' + list.length;
    sel.innerHTML = '';
    for (let p = 0; p < pc; p++) {
      const o = document.createElement('option'); o.value = p;
      o.textContent = `${p + 1} / ${pc} 페이지 (${p * PAGE + 1}~${Math.min(list.length, (p + 1) * PAGE)})`;
      sel.appendChild(o);
    }
  }
  sel.value = gridPage;
  $('#pgPrev').disabled = gridPage === 0;
  $('#pgNext').disabled = gridPage >= pc - 1;
  $('#jumpIn').placeholder = list.length ? `번호(1~${list.length}) 또는 파일명(예: ${(list[0] || '').replace(/\.[^.]+$/, '').slice(-4)})` : '사진 없음';
}
function goPage(p) {
  gridPage = Math.max(0, Math.min(pageCount() - 1, p));
  renderGrid(); $('#gridList').scrollTop = 0;
}
function openAt(i) { idx = i; showScreen('viewer'); layoutAll(); renderAllSlides(); }
function openGrid() {
  showScreen('grid'); fitGrid();
  gridPage = Math.floor(idx / PAGE);
  renderGrid();
}
window.addEventListener('resize', () => { if (!$('#grid').classList.contains('hidden') && fitGrid()) renderGrid(); });
$('#pgPrev').onclick = () => goPage(gridPage - 1);
$('#pgNext').onclick = () => goPage(gridPage + 1);
$('#pgSel').onchange = (e) => goPage(+e.target.value);
function jump() {
  const v = $('#jumpIn').value.trim();
  if (!v) return;
  let i = -1;
  if (/^[1-9]\d*$/.test(v) && +v <= list.length) i = +v - 1;               // 순서 번호 (0으로 시작하면 파일명)
  else {                                                                     // 파일명 (예: 0614, JTG_0614)
    const q = v.toLowerCase();
    i = list.findIndex((n) => n.toLowerCase().replace(/\.[^.]+$/, '') === q);
    if (i < 0) i = list.findIndex((n) => n.toLowerCase().includes(q));
  }
  if (i < 0) { $('#jumpIn').select(); toast(`"${v}" 사진이 지금 목록에 없어요`); return; }
  $('#jumpIn').value = ''; $('#jumpIn').blur();
  openAt(i);
}
$('#jumpGo').onclick = jump;
$('#jumpIn').addEventListener('keydown', (e) => { if (e.key === 'Enter') jump(); });
$('#btnGrid').onclick = openGrid;
$('#gridBack').onclick = () => { showScreen('viewer'); layoutAll(); renderAllSlides(); };
$('#btnFilter').onclick = $('#gridFilter').onclick = openFilter;

// ------------------------------------------------------------------ menu
function openMenu() {
  const total = PHOTOS.length;
  const cnt = (u) => Object.values(SCORES).filter((s) => s[u]).length;
  const nf = Object.keys(FINALS).length;
  $('#stats').innerHTML =
    `<div>전체<b>${total}장</b></div><div>♥ 최종셀렉<b>${nf}장</b></div>` +
    USERS.map((u) => `<div>${esc(NAMES[u])} 평가<b>${cnt(u)} / ${total}</b></div>`).join('');
  $('#mDownload').textContent = `⬇ 최종셀렉 원본 다운로드 (${nf}장, zip)`;
  $('#mAdmin').classList.toggle('hidden', !LOCAL);
  $('#mOrig').classList.toggle('hidden', !list[idx] || !$('#grid').classList.contains('hidden'));
  $('#menuSheet').classList.remove('hidden');
}
$('#btnMenu').onclick = $('#gridMenu').onclick = openMenu;
function download(url) {
  const a = document.createElement('a'); a.href = url; a.download = ''; document.body.appendChild(a); a.click(); a.remove();
}
$('#mDownload').onclick = () => {
  if (!Object.keys(FINALS).length) { toast('최종셀렉된 사진이 없어요'); return; }
  download('/api/download-finals'); closeSheets(); toast('다운로드 시작… (용량이 커서 시간이 걸려요)');
};
$('#mOrig').onclick = () => { download('/img/orig/' + enc(list[idx]) + '?dl=1'); closeSheets(); };
$('#mAdmin').onclick = () => { location.href = '/admin'; };
$('#mHelp').onclick = () => { closeSheets(); $('#helpSheet').classList.remove('hidden'); };
$('#mLogout').onclick = async () => { await fetch('/api/logout', { method: 'POST', body: '{}' }); location.reload(); };

function closeSheets() { $$('.sheet').forEach((s) => s.classList.add('hidden')); }
$$('.sheet').forEach((s) => s.addEventListener('click', (e) => { if (e.target === s || e.target.hasAttribute('data-close')) closeSheets(); }));

boot().catch((e) => console.error(e));
