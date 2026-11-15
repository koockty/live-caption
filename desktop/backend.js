import http from 'node:http';
import {EventEmitter} from 'node:events';
import WebSocket from 'ws';

export function backendURL(value = 'http://127.0.0.1:17831') {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname) || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('后端地址必须是本机 HTTP 回环地址');
  return url.origin;
}

export class Backend extends EventEmitter {
  constructor(base) {super(); this.base = backendURL(base); this.cookie = ''; this.closed = false; this.socket = null; this.sessionID = null;}

  async request(path) {
    return new Promise((resolve, reject) => {
      const request = http.get(this.base + path, {headers: {Origin: this.base, ...(this.cookie ? {Cookie: this.cookie} : {})}}, (response) => {
        const cookie = response.headers['set-cookie']?.find((v) => v.startsWith('lc_local='));
        if (cookie) this.cookie = cookie.split(';')[0];
        let body = ''; response.setEncoding('utf8');
        response.on('data', (chunk) => {body += chunk; if (body.length > 2 * 1024 * 1024) request.destroy(new Error('后端数据过大'));});
        response.on('end', () => {
          if (response.statusCode !== 200) {this.cookie = ''; reject(new Error(`后端返回 ${response.statusCode}`)); return;}
          if (path === '/') {resolve(null); return;}
          try {resolve(JSON.parse(body));} catch {reject(new Error('后端数据无效'));}
        });
        response.on('error', reject);
      });
      request.setTimeout(3000, () => request.destroy(new Error('后端连接超时')));
      request.on('error', reject);
    });
  }

  async poll() {
    if (this.closed) return;
    try {
      if (!this.cookie) await this.request('/');
      const data = await this.request('/v1/sessions/current');
      if (this.closed) return;
      const snapshot = data.snapshot;
      this.emit('update', {type: 'snapshot', snapshot});
      if (snapshot && !['stopped', 'failed'].includes(snapshot.state)) {
        if (this.sessionID !== snapshot.session_id || !this.socket) this.subscribe(snapshot.session_id);
      } else if (this.socket) {this.detach();}
    } catch {
      if (!this.closed) {this.detach(); this.emit('update', {type: 'connection', message: '等待 Go 服务 · 请启动本地后端'});}
    } finally {
      if (!this.closed) this.timer = setTimeout(() => this.poll(), 1000);
    }
  }

  subscribe(id) {
    this.detach(); this.sessionID = id;
    const socket = new WebSocket(this.base.replace('http:', 'ws:') + `/v1/sessions/${encodeURIComponent(id)}/events`, {headers: {Origin: this.base, Cookie: this.cookie}, handshakeTimeout: 3000, maxPayload: 2 * 1024 * 1024});
    this.socket = socket;
    socket.on('message', (data) => {
      if (this.socket !== socket || this.closed) return;
      try {const event = JSON.parse(data.toString()); if (event.type === 'snapshot' || event.session_id === id) this.emit('update', event);} catch {socket.close();}
    });
    socket.on('error', () => {});
    socket.on('close', () => {if (this.socket === socket) {this.socket = null;}});
  }

  detach() {const socket = this.socket; this.socket = null; if (socket) {socket.on('error', () => {}); socket.terminate();}}
  stop() {this.closed = true; clearTimeout(this.timer); this.detach();}
}
