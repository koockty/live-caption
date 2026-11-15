const $ = (id) => document.getElementById(id);
const stateLabels = { idle: '等待开始', selecting: '等待选择标签页', connecting: '连接云端识别', created: '准备连接', running: '正在识别', finishing: '正在完成尾句', stopped: '本次会话已结束', failed: '会话已中断' };
let configured = false;
let run = null;
let captions = new Map();

function showError(message) {
  $('errorBanner').textContent = message;
  $('errorBanner').hidden = !message;
}

function state(value, message = '') {
  $('status').dataset.state = value;
  $('statusText').textContent = stateLabels[value] || value;
  $('startButton').disabled = !configured || !!run;
  $('stopButton').disabled = !run || run.stopping || run.terminal;
  $('stageLabel').textContent = value === 'running' ? '正在聆听' : '实时字幕';
  $('controlNote').textContent = message || ({ running: '保持采集页打开，可切回音源页播放。', finishing: '正在接收最后一句，请稍候。', stopped: '可以选择标签页开始下一次会话。' }[value] ?? '只采集你选择的标签页音频。');
  $('captureNode').classList.toggle('active', value === 'running');
  $('asrNode').classList.toggle('active', value === 'running' || value === 'finishing');
  if (value !== 'running' && value !== 'finishing') $('translationNode').classList.remove('active');
}

async function api(path, method = 'GET') {
  const response = await fetch(path, {
    method, credentials: 'same-origin',
    ...(method === 'POST' ? { headers: { 'Content-Type': 'application/json' }, body: '{}' } : {}),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `本地服务返回 HTTP ${response.status}`);
  return data;
}

function wsURL(path) { return `ws://${location.host}${path}`; }

function transcript(value) {
  $('confirmedText').textContent = value.confirmed || '';
  $('draftText').textContent = value.draft || '';
  if (value.confirmed || value.draft) $('stageHint').textContent = value.final ? '短句已确认，正在等待中文译文。' : '日文原文会随识别修订，中文等待短句确认。';
}

function renderHistory() {
  if (!captions.size) { $('emptyHistory').hidden = false; return; }
  const items = [...captions.values()].sort((a, b) => b.order - a.order).slice(0, 20);
  const elements = items.map((item) => {
    const row = document.createElement('article'); row.className = 'history-item';
    const order = document.createElement('span'); order.className = 'history-order'; order.textContent = String(item.order).padStart(2, '0');
    const body = document.createElement('div');
    const ja = document.createElement('p'); ja.className = 'history-ja'; ja.lang = 'ja'; ja.textContent = item.ja;
    const zh = document.createElement('p'); zh.className = 'history-zh'; zh.lang = 'zh-CN';
    if (item.state === 'translated') zh.textContent = item.zh;
    else if (item.state === 'translation_failed') { zh.textContent = item.error || '翻译暂时失败'; zh.classList.add('failed'); }
    else { zh.textContent = '正在翻译…'; zh.classList.add('pending'); }
    body.append(ja, zh); row.append(order, body); return row;
  });
  $('emptyHistory').hidden = true;
  $('history').replaceChildren($('emptyHistory'), ...elements);
  const translated = [...captions.values()].filter((item) => item.state === 'translated').length;
  $('captionCount').replaceChildren(document.createTextNode(`${translated} `), Object.assign(document.createElement('small'), { textContent: '组' }));
}

function upsertCaption(value) {
  const previous = captions.get(value.id);
  if (previous && previous.revision >= value.revision) return;
  captions.set(value.id, value);
  if (captions.size > 200) captions.delete(captions.keys().next().value);
  if (value.zh) {
    $('chinese').textContent = value.zh;
    $('stageHint').textContent = value.state === 'translated' ? '已完成本组翻译，继续聆听下一句。' : '正在接收中文译文…';
  } else if (value.state === 'translation_failed') {
    $('stageHint').textContent = value.error;
  }
  $('translationNode').classList.toggle('active', value.state === 'translating');
  renderHistory();
}

function applySnapshot(r, snapshot) {
  if (run !== r || snapshot.event_seq < r.eventSeq) return;
  r.eventSeq = snapshot.event_seq;
  if (snapshot.transcript.item_id) transcript(snapshot.transcript);
  captions = new Map();
  for (const item of snapshot.captions) upsertCaption(item);
  renderHistory();
  handleState(r, snapshot.state, snapshot.error);
}

function handleState(r, value, message) {
  if (run !== r) return;
  state(value, message);
  if (value === 'failed' || value === 'stopped') finishRun(r, value, message);
}

function subscribe(r, attempt = 0) {
  if (run !== r || r.terminal) return;
  const socket = new WebSocket(wsURL(`/v1/sessions/${r.id}/events`));
  r.events = socket;
  socket.onmessage = ({ data }) => {
    if (run !== r) return;
    const event = JSON.parse(data);
    if (event.type === 'snapshot') { applySnapshot(r, event.snapshot); return; }
    if (event.session_id !== r.id || event.seq <= r.eventSeq) return;
    r.eventSeq = event.seq;
    if (event.type === 'transcript') transcript(event.transcript);
    if (event.type === 'caption.upsert') upsertCaption(event.caption);
    if (event.type === 'status') handleState(r, event.state, event.message);
  };
  socket.onclose = () => {
    if (run !== r || r.terminal) return;
    if (attempt < 4) {
      setTimeout(() => subscribe(r, attempt + 1), Math.min(300 * 2 ** attempt, 2400));
    } else {
      showError('字幕事件连接已断开，请停止后重新开始。');
      void stopRun(r);
    }
  };
}

function openAudio(r) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(wsURL(`/v1/sessions/${r.id}/audio`));
    r.audio = socket;
    let ready = false;
    const timeout = setTimeout(() => { reject(new Error('连接云端识别超时，请检查网络与 Key。')); socket.close(); }, 15000);
    socket.onmessage = ({ data }) => {
      const event = JSON.parse(data);
      if (event.type === 'ready') { ready = true; clearTimeout(timeout); resolve(); }
      if (event.type === 'error') { clearTimeout(timeout); reject(new Error(event.message)); }
    };
    socket.onerror = () => { if (!ready) { clearTimeout(timeout); reject(new Error('无法连接音频通道，请检查 Go 服务。')); } };
    socket.onclose = () => {
      clearTimeout(timeout);
      if (!ready) reject(new Error('音频连接在准备完成前关闭。'));
      if (ready && run === r && !r.stopping && !r.terminal) {
        showError('音频连接已中断。本次采集将结束，请检查网络后重新开始。');
        void stopRun(r);
      }
    };
  });
}

async function start() {
  if (run || !configured) return;
  if (!window.isSecureContext || !navigator.mediaDevices?.getDisplayMedia || !window.AudioWorkletNode) {
    showError('请在新版 Chrome 中，通过本机 http://127.0.0.1 地址打开本页。'); return;
  }
  const r = { id: null, eventSeq: 0, stopping: false, terminal: false, sending: false, samples: 0 };
  run = r; showError(''); state('selecting');
  try {
    // Resume inside the button gesture. The browser, not this application,
    // performs sample-rate conversion before delivering worklet input.
    r.context = new AudioContext({ sampleRate: 16000, latencyHint: 'interactive' });
    await r.context.resume();
    if (r.context.sampleRate !== 16000) throw new Error('浏览器未能创建 16 kHz 音频上下文，请更新 Chrome。');
    r.stream = await navigator.mediaDevices.getDisplayMedia({
      video: { displaySurface: 'browser', frameRate: 1 },
      audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false, suppressLocalAudioPlayback: false },
      selfBrowserSurface: 'exclude', systemAudio: 'exclude', surfaceSwitching: 'exclude',
    });
    if (r.terminal || run !== r) { cleanupMedia(r); return; }
    const audioTrack = r.stream.getAudioTracks()[0];
    if (!audioTrack || audioTrack.readyState !== 'live') throw new Error('没有获得音频。请重新选择 Chrome 标签页，并勾选「共享标签页音频」。');
    if (r.stream.getVideoTracks()[0]?.getSettings().displaySurface !== 'browser') throw new Error('请选择「Chrome 标签页」，本原型不采集整个屏幕或应用窗口。');
    for (const track of r.stream.getTracks()) track.addEventListener('ended', () => { if (run === r && !r.stopping) void stopRun(r); });
    const session = await api('/v1/sessions', 'POST');
    r.id = session.session_id;
    if (r.terminal || run !== r) { void api(`/v1/sessions/${r.id}/stop`, 'POST').catch(() => {}); return; }
    captions = new Map(); renderHistory(); $('captionCount').textContent = '0 组'; $('audioDuration').textContent = '00:00';
    $('confirmedText').textContent = '等待标签页中的日语声音…'; $('draftText').textContent = ''; $('chinese').textContent = '等待第一句中文译文…';
    state('connecting'); subscribe(r);
    await openAudio(r);
    if (r.terminal || run !== r) return;
    await r.context.audioWorklet.addModule('/assets/audio-worklet.js');
    if (r.terminal || run !== r) return;
    r.node = new AudioWorkletNode(r.context, 'pcm16-capture');
    r.node.onprocessorerror = () => { showError('音频处理器已停止，请重新开始。'); void stopRun(r); };
    r.sending = true;
    r.node.port.onmessage = ({ data }) => {
      if (data.type === 'flushed') { r.flushed?.(); return; }
      if (data.type !== 'frame' || !r.sending || run !== r) return;
      if (r.audio.readyState !== WebSocket.OPEN) { void stopRun(r); return; }
      if (r.audio.bufferedAmount + data.frame.byteLength > 32768) {
        showError('音频上传积压超过一秒，已停止采集以避免字幕持续延迟。'); void stopRun(r); return;
      }
      r.audio.send(data.frame);
      r.samples += (data.frame.byteLength - 12) / 2;
      const seconds = Math.floor(r.samples / 16000);
      $('audioDuration').textContent = `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`;
      $('levelMeter').value = data.level;
      $('levelText').textContent = data.level > 1 ? '正在接收' : '静音';
    };
    r.source = r.context.createMediaStreamSource(new MediaStream([audioTrack]));
    r.mute = r.context.createGain(); r.mute.gain.value = 0;
    r.source.connect(r.node); r.node.connect(r.mute); r.mute.connect(r.context.destination);
    state('running');
  } catch (error) {
    if (run !== r) return;
    const message = ({
      NotAllowedError: '共享已取消或被拒绝。准备好后可重新选择标签页。',
      InvalidStateError: '请先切换到本采集标签页，再点击开始。Chrome 需要前台的用户操作。',
      NotReadableError: 'Chrome 无法读取共享来源，请确认标签页仍然打开并重新选择。',
    })[error.name] || error.message;
    if (r.id) void api(`/v1/sessions/${r.id}/stop`, 'POST').catch(() => {});
    finishRun(r, 'failed', message);
  }
}

async function stopRun(r) {
  if (run !== r || r.stopping || r.terminal) return;
  r.stopping = true; state('finishing');
  try {
    if (r.node && r.sending) {
      await new Promise((resolve) => {
        const timer = setTimeout(resolve, 1200);
        r.flushed = () => { clearTimeout(timer); resolve(); };
        r.node.port.postMessage({ type: 'flush' });
      });
    }
    r.sending = false;
    if (r.audio?.readyState === WebSocket.OPEN) r.audio.send(JSON.stringify({ type: 'audio.end' }));
    else if (r.id) await api(`/v1/sessions/${r.id}/stop`, 'POST');
    if (!r.id) finishRun(r, 'stopped');
  } catch (error) {
    finishRun(r, 'failed', `停止会话失败：${error.message}`);
  } finally { cleanupMedia(r); }
}

function cleanupMedia(r) {
  r.source?.disconnect(); r.node?.disconnect(); r.mute?.disconnect();
  for (const track of r.stream?.getTracks() || []) track.stop();
  if (r.context && r.context.state !== 'closed') void r.context.close().catch(() => {});
  $('levelMeter').value = 0; $('levelText').textContent = '静音';
}

function finishRun(r, value, message) {
  if (run !== r || r.terminal) return;
  r.terminal = true; r.sending = false; run = null;
  cleanupMedia(r); r.audio?.close(); r.events?.close();
  state(value, message);
  if (message) showError(message);
  if (value === 'stopped') $('stageHint').textContent = '本次会话已结束，字幕仅保留在内存中。';
}

$('startButton').addEventListener('click', () => void start());
$('stopButton').addEventListener('click', () => { if (run) void stopRun(run); });
$('reloadButton').addEventListener('click', () => location.reload());
$('modeButton').addEventListener('click', () => {
  const onlyChinese = document.body.classList.toggle('only-chinese');
  $('modeButton').setAttribute('aria-pressed', String(onlyChinese));
  $('modeButton').textContent = onlyChinese ? '双语显示' : '仅中文';
  try { localStorage.setItem('captions.onlyChinese', String(onlyChinese)); } catch {}
});
$('fontSize').addEventListener('input', () => {
  document.documentElement.style.setProperty('--caption-size', `${$('fontSize').value}px`);
  try { localStorage.setItem('captions.fontSize', $('fontSize').value); } catch {}
});
try {
  if (localStorage.getItem('captions.onlyChinese') === 'true') $('modeButton').click();
  const size = Number(localStorage.getItem('captions.fontSize'));
  if (size >= 22 && size <= 46) { $('fontSize').value = String(size); $('fontSize').dispatchEvent(new Event('input')); }
} catch {}

window.addEventListener('pagehide', () => {
  if (run) {
    cleanupMedia(run);
    if (run.audio?.readyState === WebSocket.OPEN) run.audio.send(JSON.stringify({ type: 'audio.end' }));
    run.audio?.close();
  }
});

try {
  const config = await api('/v1/config');
  configured = config.configured;
  $('connection').classList.add('ready'); $('connectionText').textContent = 'Go 本地服务已连接';
  $('configNotice').hidden = configured;
  state('idle', configured ? '已读取 Key；开始时验证云端连接。' : '配置 API Key 并重启服务后即可开始。');
} catch (error) {
  $('connectionText').textContent = '本地服务连接失败'; showError(error.message);
}
