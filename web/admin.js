'use strict';
const $ = (s) => document.querySelector(s);
const esc = (t) => String(t ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
let S = null;           // last status
let formLoaded = false;
let popup = null;       // Tailscale login / enable window

async function post(action, body) {
  const r = await fetch('/api/admin/' + action, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}),
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}

async function refresh() {
  try {
    S = await (await fetch('/api/admin/status')).json();
  } catch { return; }
  render();
}

function render() {
  $('#title').textContent = '💍 ' + S.displayTitle;
  document.title = '관리 · ' + S.displayTitle;
  $('#ver').textContent = S.version;
  if (!formLoaded) {
    formLoaded = true;
    $('#n0').value = S.people[0].name; $('#p0').value = S.people[0].password;
    $('#n1').value = S.people[1].name; $('#p1').value = S.people[1].password;
    $('#ttl').value = S.title || '';
    $('#host').value = S.hostname; $('#hostPrev').textContent = S.hostname;
    $('#dir').textContent = S.photoDir;
    $('#base').textContent = S.baseDir;
  }
  $('#ok1').textContent = S.configured ? '✓ 완료' : '';
  $('#ok2').textContent = S.photos ? `✓ ${S.photos}장` : '';
  $('#cnt').textContent = S.photos + '장';
  $('#fin').textContent = S.finals + '장';
  $('#prep').textContent = S.photos ? `${S.cached} / ${S.photos}` : '-';
  $('#prepBar').style.width = S.photos ? (100 * S.cached / S.photos) + '%' : '0';
  $('#prepNote').textContent = S.photos && S.cached < S.photos
    ? '빠른 넘김을 위해 미리보기를 만드는 중이에요 (처음 한 번만, 사진 1000장에 수 분). 그동안에도 셀렉을 시작할 수 있어요.' : '';
  $('#openViewer').style.pointerEvents = S.configured ? '' : 'none';
  $('#openViewer button').disabled = !S.configured;
  renderTS(S.tailscale);
}

// ------------------------------------------------------------------ 1. people
$('#save').onclick = async () => {
  const m = $('#msg1'); m.className = 'msg'; m.textContent = '';
  try {
    await post('config', {
      title: $('#ttl').value, hostname: $('#host').value || S.hostname,
      people: [{ name: $('#n0').value, password: $('#p0').value }, { name: $('#n1').value, password: $('#p1').value }],
    });
    m.className = 'msg good'; m.textContent = '저장했어요 ✓';
    refresh();
  } catch (e) { m.className = 'msg err'; m.textContent = e.message; }
};
document.querySelectorAll('[data-eye]').forEach((b) => {
  b.onclick = () => { const i = $('#' + b.dataset.eye); i.type = i.type === 'password' ? 'text' : 'password'; b.textContent = i.type === 'password' ? '보기' : '숨기기'; };
});

// ------------------------------------------------------------------ 2. photos
const isImg = (f) => /\.(jpe?g|png)$/i.test(f.name) && !f.name.startsWith('.');
$('#pickFiles').onclick = () => $('#fileIn').click();
$('#pickDir').onclick = () => $('#dirIn').click();
$('#fileIn').onchange = (e) => upload([...e.target.files]);
$('#dirIn').onchange = (e) => upload([...e.target.files]);
const drop = $('#drop');
['dragenter', 'dragover'].forEach((t) => drop.addEventListener(t, (e) => { e.preventDefault(); drop.classList.add('over'); }));
['dragleave', 'drop'].forEach((t) => drop.addEventListener(t, (e) => { e.preventDefault(); drop.classList.remove('over'); }));
drop.addEventListener('drop', async (e) => {
  const items = [...(e.dataTransfer.items || [])].map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  if (items.length) upload(await walkEntries(items));
  else upload([...e.dataTransfer.files]);
});
async function walkEntries(entries) {
  const out = [];
  async function walk(en) {
    if (en.isFile) out.push(await new Promise((res, rej) => en.file(res, rej)));
    else if (en.isDirectory) {
      const rd = en.createReader();
      for (;;) {
        const batch = await new Promise((res, rej) => rd.readEntries(res, rej));
        if (!batch.length) break;
        for (const b of batch) await walk(b);
      }
    }
  }
  for (const e of entries) await walk(e);
  return out;
}
const fmtMB = (b) => (b / 1048576).toFixed(0) + 'MB';
async function upload(files) {
  files = files.filter(isImg);
  if (!files.length) { alertMsg('jpg/png 사진이 없어요'); return; }
  const total = files.reduce((a, f) => a + f.size, 0);
  let sent = 0, done = 0;
  $('#up').classList.remove('hidden');
  // 약 50MB씩 나눠서 순서대로 올림
  const batches = []; let cur = [], sz = 0;
  for (const f of files) { cur.push(f); sz += f.size; if (sz > 50e6) { batches.push(cur); cur = []; sz = 0; } }
  if (cur.length) batches.push(cur);
  try {
    for (const b of batches) {
      const fd = new FormData(); b.forEach((f) => fd.append('file', f, f.name));
      const bsize = b.reduce((a, f) => a + f.size, 0);
      await new Promise((res, rej) => {
        const x = new XMLHttpRequest(); x.open('POST', '/api/admin/upload'); x.setRequestHeader('X-WS', '1');
        x.upload.onprogress = (e) => { const p = sent + e.loaded * (bsize / (e.total || bsize)); show(p); };
        x.onload = () => (x.status === 200 ? res() : rej(new Error(x.responseText)));
        x.onerror = () => rej(new Error('업로드 실패'));
        x.send(fd);
      });
      sent += bsize; done += b.length; show(sent);
    }
    $('#upMsg').textContent = `✓ ${files.length}장 올림 완료`;
    await post('rescan'); refresh();
  } catch (e) { $('#upMsg').textContent = '업로드 중 오류: ' + e.message; }
  function show(p) {
    $('#upMsg').textContent = `올리는 중… ${done} / ${files.length}장 (${fmtMB(p)} / ${fmtMB(total)})`;
    $('#upBar').style.width = (100 * p / total) + '%';
  }
}
function alertMsg(t) { $('#up').classList.remove('hidden'); $('#upMsg').textContent = t; }
$('#openFolder').onclick = () => post('open-folder').catch((e) => alertMsg(e.message));
$('#openBase').onclick = () => post('open-base').catch((e) => alert(e.message));
$('#rescan').onclick = async () => { const r = await post('rescan'); alertMsg(`사진 ${r.photos}장을 찾았어요`); refresh(); };

// ------------------------------------------------------------------ 3. tailscale
function openPopup(url) {
  const feat = 'width=560,height=760,menubar=no,toolbar=no';
  if (popup && !popup.closed) { try { popup.location.href = url; popup.focus(); return; } catch {} }
  popup = window.open(url, 'tailscale', feat);
}
function waitingPopup() {
  // 클릭 순간에 창을 열어둬야 팝업 차단에 안 걸림. 이 페이지는 스스로 상태를 따라
  // 로그인/허용 화면으로 넘어가므로 관리 화면이 가려져 있어도 멈추지 않는다.
  popup = window.open('/admin/ts', 'tailscale', 'width=560,height=760,menubar=no,toolbar=no');
}
let lastTSState = '';
function renderTS(t) {
  const box = $('#tsBody');
  $('#ok3').textContent = t.state === 'running' ? '✓ 공유 중' : '';
  // 상태가 바뀌면 열린 팝업을 알맞게 이동/닫기
  if (t.state !== lastTSState) {
    if (t.state === 'needs-funnel' && t.enableURL && popup && !popup.closed) openPopup(t.enableURL);
    if (t.state === 'off' && popup && !popup.closed) { try { popup.close(); } catch {} }
    lastTSState = t.state;
  } else if (box.dataset.key === String(S.tsLogin) + t.state + t.ready + (t.authURL || '') + (t.enableURL || '') + (t.publicURL || '') + (t.error || '')) {
    return; // 변화 없음
  }
  box.dataset.key = String(S.tsLogin) + t.state + t.ready + (t.authURL || '') + (t.enableURL || '') + (t.publicURL || '') + (t.error || '');
  let h = '';
  switch (t.state) {
    case 'off':
      h = `<ol class="steps">
          <li><b>외부 공유 시작</b>을 누르면 로그인 창이 떠요 (Google · Apple · Microsoft 계정으로 무료 가입/로그인)</li>
          <li>처음 한 번은 <b>Funnel 허용</b> 화면이 나올 수 있어요 → 창에서 허용(Enable) 누르기</li>
          <li><b>https://….ts.net</b> 주소가 나오면 복사해서 배우자에게 보내기</li></ol>
        <div class="row2" style="margin-top:0"><button id="tsStart" class="primary">외부 공유 시작</button>
        ${S.tsLogin ? '<button id="tsLogout" class="danger">Tailscale 로그아웃</button>' : ''}</div>`;
      break;
    case 'starting':
      h = '<p><span class="spin"></span>Tailscale에 연결하는 중…</p>';
      break;
    case 'needs-login':
      h = `<p>열린 창에서 <b>Tailscale에 로그인</b>해 주세요. 로그인하면 이 화면이 자동으로 넘어가요.</p>
        <div class="row2"><button id="tsLogin" class="primary">로그인 창 열기</button>
        <button id="tsStop">취소</button></div>
        <p class="desc">창이 안 열리면 팝업 차단을 해제하거나 <a href="${esc(t.authURL)}" target="_blank" rel="noopener" style="color:#9cf">여기</a>를 누르세요.</p>`;
      break;
    case 'needs-funnel':
      h = `<p>로그인 완료${t.account ? ` (<b>${esc(t.account)}</b>)` : ''}! 마지막으로 <b>외부 공개(Funnel) 허용</b>이 필요해요.</p>
        <p class="desc">열린 창에서 허용(Enable) 버튼을 누르세요. 허용되면 자동으로 넘어가요.</p>
        <div class="row2">${t.enableURL ? '<button id="tsEnable" class="primary">허용 창 열기</button>' : ''}
        <button id="tsStop">취소</button></div>
        ${t.enableURL ? '' : `<p class="desc">${esc(t.enableText || 'Tailscale 관리 화면에서 HTTPS 와 Funnel 을 켜 주세요.')}<br>
          <a href="https://login.tailscale.com/admin/dns" target="_blank" rel="noopener" style="color:#9cf">DNS 설정 (HTTPS 켜기)</a> ·
          <a href="https://login.tailscale.com/admin/acls" target="_blank" rel="noopener" style="color:#9cf">접근 정책 (funnel 허용)</a></p>`}
        <p class="desc"><span class="spin"></span>확인 중…</p>`;
      break;
    case 'running':
      if (!t.ready) {
        h = `<p><span class="spin"></span><b>인터넷 주소를 준비하고 있어요</b> (처음 한 번, 보통 1~3분)</p>
          <p class="desc">주소가 인터넷에 등록되고 보안 인증서가 발급되는 중이에요. 준비되면 여기에 주소가 나타나요.<br>
          ※ 미리 접속해 보면 한동안 "찾을 수 없음" 이 남을 수 있어 준비될 때까지 숨겨 두었어요.</p>
          <div class="row2"><button id="tsStop">공유 중지</button></div>`;
        if (popup && !popup.closed) { try { popup.close(); } catch {} }
        break;
      }
      h = `<p class="desc" style="margin-top:0">✓ 외부 접속 확인 완료! 아래 주소를 배우자에게 보내세요.</p>
        <div class="url"><a href="${esc(t.publicURL)}" target="_blank" rel="noopener">${esc(t.publicURL)}</a>
        <button id="tsCopy">복사</button></div>
        <div class="row2"><button id="tsMsg">공유 메시지 복사</button><button id="tsStop">공유 중지</button>
        <button id="tsLogout" class="danger">Tailscale 로그아웃</button></div>
        <p class="desc">${t.account ? '계정: ' + esc(t.account) + ' · ' : ''}다음에 프로그램을 켜면 자동으로 다시 공유돼요.</p>`;
      break;
    case 'error':
      h = `<p class="msg err">${esc(t.error)}</p><div class="row2"><button id="tsRetry" class="primary">다시 시도</button>
        <button id="tsStop">중지</button></div>`;
      break;
  }
  box.innerHTML = h;
  const on = (id, fn) => { const b = box.querySelector('#' + id); if (b) b.onclick = fn; };
  on('tsStart', async () => {
    if (!S.configured) { alert('먼저 1번에서 신랑·신부 이름과 비밀번호를 저장해 주세요.'); return; }
    waitingPopup(); await post('ts-start'); refresh();
  });
  on('tsLogin', () => openPopup(t.authURL));
  on('tsEnable', () => openPopup(t.enableURL));
  on('tsStop', async () => { await post('ts-stop'); if (popup) try { popup.close(); } catch {} refresh(); });
  on('tsRetry', async () => { await post('ts-retry'); refresh(); });
  on('tsLogout', async () => { if (confirm('이 컴퓨터의 Tailscale 로그인을 해제할까요? 공개 주소가 꺼지고, 다음에 다시 로그인해야 해요.\n(Tailscale 기기 목록에는 꺼진 상태로 남아요. 완전히 지우려면 login.tailscale.com/admin/machines 에서 Remove)')) { await post('ts-logout'); refresh(); } });
  on('tsCopy', () => copy(t.publicURL, '#tsCopy'));
  on('tsMsg', () => copy(`💍 ${S.displayTitle} 사진 셀렉\n${t.publicURL}\n\n비밀번호는 따로 알려줄게요! 들어가서 사진마다 1~5점 매겨줘요 :)`, '#tsMsg'));
}
async function copy(text, btn) {
  try { await navigator.clipboard.writeText(text); } catch {
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove();
  }
  const b = $(btn); const o = b.textContent; b.textContent = '복사됨 ✓'; setTimeout(() => { b.textContent = o; }, 1500);
}
$('#host').oninput = () => { $('#hostPrev').textContent = $('#host').value.toLowerCase().replace(/[^a-z0-9-]/g, '-') || 'wedding-select'; };
$('#saveHost').onclick = async () => {
  try {
    await post('config', {
      title: $('#ttl').value, hostname: $('#host').value,
      people: S.people.map((p) => ({ name: p.name, password: p.password })),
    });
    refresh();
  } catch (e) { alert(e.message); }
};

// ------------------------------------------------------------------ reset / restore
const rsMsg = (t, ok) => { const m = $('#rsMsg'); m.className = 'msg ' + (ok ? 'good' : 'err'); m.textContent = t; };
[['rsAll', 'all', '두 사람의 점수와 최종셀렉을 모두 지울까요?'], ['rsScores', 'scores', '두 사람의 점수를 모두 지울까요? (최종셀렉은 그대로)'],
 ['rsFinals', 'finals', '최종셀렉을 비울까요? (점수는 그대로)']].forEach(([id, what, q]) => {
  $('#' + id).onclick = async () => {
    if (!confirm(q + '\n\n지우기 직전 상태는 백업되어 되돌릴 수 있어요.')) return;
    try { await post('reset', { what }); rsMsg('초기화했어요 ✓', true); refresh(); } catch (e) { rsMsg(e.message); }
  };
});
$('#rsUndo').onclick = async () => {
  if (!confirm('가장 최근 백업으로 되돌릴까요?\n(지금 상태도 백업되니 다시 되돌릴 수 있어요)')) return;
  try { await post('restore'); rsMsg('되돌렸어요 ✓', true); refresh(); } catch (e) { rsMsg(e.message); }
};

// ------------------------------------------------------------------ quit
$('#quit').onclick = async () => {
  if (!confirm('프로그램을 종료할까요? 외부 공유도 같이 꺼져요. (점수는 저장돼 있어요)')) return;
  await post('quit');
  document.body.innerHTML = '<div class="wrap"><h1>종료했어요</h1><p class="muted">다시 쓰려면 프로그램을 다시 실행하세요.</p></div>';
};

refresh();
// 팝업 때문에 이 탭이 가려져도 계속 상태를 갱신 (로컬 요청이라 부담 없음)
setInterval(refresh, 1500);
document.addEventListener('visibilitychange', refresh);
