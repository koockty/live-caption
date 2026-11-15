import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {WebSocketServer} from 'ws';
import {Backend} from '../desktop/backend.js';

test('desktop adapter authenticates locally and subscribes to the discovered caption session', async () => {
  const snapshot = {session_id:'desktop-test',event_seq:1,state:'running',transcript:{confirmed:'字幕テスト'},captions:[]};
  let origin;
  const server = http.createServer((request,response) => {
    if (request.url === '/') {response.setHeader('Set-Cookie','lc_local=test-local-cookie; HttpOnly; SameSite=Strict; Path=/'); response.end('local page'); return;}
    assert.equal(request.headers.cookie,'lc_local=test-local-cookie');
    assert.equal(request.headers.origin,origin);
    response.setHeader('Content-Type','application/json'); response.end(JSON.stringify({snapshot}));
  });
  const websocket = new WebSocketServer({noServer:true});
  server.on('upgrade', (request,socket,head) => {
    assert.equal(request.url,'/v1/sessions/desktop-test/events');
    assert.equal(request.headers.cookie,'lc_local=test-local-cookie');
    assert.equal(request.headers.origin,origin);
    websocket.handleUpgrade(request,socket,head,(client) => {
      client.send(JSON.stringify({type:'snapshot',snapshot}));
      client.send(JSON.stringify({type:'caption.upsert',session_id:'desktop-test',seq:2,caption:{zh:'桌面字幕测试'}}));
    });
  });
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  origin = `http://127.0.0.1:${server.address().port}`;
  const backend = new Backend(origin);
  try {
    const received = new Promise((resolve,reject) => {
      const timeout = setTimeout(()=>reject(new Error('desktop subscription timed out')),5000);
      backend.on('update',(event)=>{if(event.type==='caption.upsert'){clearTimeout(timeout);resolve(event);}});
    });
    backend.poll();
    const event = await received;
    assert.equal(event.caption.zh,'桌面字幕测试');
  } finally {
    backend.stop();
    for (const client of websocket.clients) client.terminate();
    await new Promise(resolve=>websocket.close(resolve));
    await new Promise(resolve=>server.close(resolve));
  }
});
