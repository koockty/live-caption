export class CaptionView {
  constructor() {this.sessionID = null; this.seq = -1; this.japanese = ''; this.chinese = ''; this.status = '等待 Chrome 采集页开始';}
  apply(message) {
    if (message.type === 'connection') {this.status = message.message; return true;}
    if (message.type === 'snapshot') {
      const s = message.snapshot;
      if (!s) {this.status = '打开 Chrome 采集页，选择标签页并开始'; return true;}
      if (s.session_id === this.sessionID && s.event_seq < this.seq) return false;
      if (s.session_id !== this.sessionID) {this.japanese = ''; this.chinese = ''; this.sessionID = s.session_id;}
      this.seq = s.event_seq; this.japanese = (s.transcript.confirmed || '') + (s.transcript.draft || '');
      const latest = [...s.captions].reverse().find((c) => c.zh);
      if (latest) this.chinese = latest.zh;
      this.setStatus(s.state, s.error); return true;
    }
    if (message.session_id !== this.sessionID || message.seq <= this.seq) return false;
    this.seq = message.seq;
    if (message.type === 'transcript') this.japanese = (message.transcript.confirmed || '') + (message.transcript.draft || '');
    if (message.type === 'caption.upsert' && message.caption.zh) this.chinese = message.caption.zh;
    if (message.type === 'status') this.setStatus(message.state, message.message);
    return true;
  }
  setStatus(state, error) {this.status = error || ({created:'等待音频采集',connecting:'正在连接识别服务',running:'正在识别 · 拖动字幕区域可移动窗口',finishing:'正在完成最后一句',stopped:'本次采集已结束',failed:'采集已中断，请在 Chrome 重新开始'}[state] || state);}
}
