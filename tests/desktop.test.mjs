import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeSettings, fitBounds} from '../desktop/settings.js';
import {CaptionView} from '../desktop/view.js';
import {backendURL} from '../desktop/backend.js';

test('display settings clamp malformed values and preserve supported controls', () => {
  assert.deepEqual(normalizeSettings({mode:'invalid',fontSize:Infinity,opacity:NaN}), {mode:'bilingual',fontSize:32,opacity:.82});
  assert.deepEqual(normalizeSettings({mode:'chinese',fontSize:100,opacity:0}), {mode:'chinese',fontSize:60,opacity:.15});
  assert.deepEqual(normalizeSettings({fontSize:18,opacity:.95}), {mode:'bilingual',fontSize:18,opacity:.95});
  const bounds = fitBounds({x:4000,y:-2000,width:900,height:260},{x:0,y:0,width:1440,height:900});
  assert.deepEqual(bounds,{x:540,y:0,width:900,height:260});
});

test('desktop subtitle state restores snapshots, rejects stale events, and clears an old session', () => {
  const view = new CaptionView();
  view.apply({type:'snapshot',snapshot:{session_id:'a',event_seq:2,state:'running',transcript:{confirmed:'今日は',draft:'晴れ'},captions:[]}});
  assert.equal(view.japanese,'今日は晴れ');
  view.apply({type:'caption.upsert',session_id:'a',seq:3,caption:{zh:'今天'}});
  view.apply({type:'caption.upsert',session_id:'a',seq:4,caption:{zh:'今天天气很好。'}});
  assert.equal(view.chinese,'今天天气很好。');
  assert.equal(view.apply({type:'caption.upsert',session_id:'a',seq:3,caption:{zh:'旧译文'}}),false);
  view.apply({type:'connection',message:'等待后端'});
  assert.equal(view.chinese,'今天天气很好。');
  view.apply({type:'snapshot',snapshot:{session_id:'b',event_seq:0,state:'created',transcript:{},captions:[]}});
  assert.equal(view.chinese,'');
  assert.equal(view.apply({type:'transcript',session_id:'a',seq:100,transcript:{confirmed:'旧原文'}}),false);
});

test('desktop backend target remains a local origin without embedded credentials', () => {
  assert.equal(backendURL(),'http://127.0.0.1:17831');
  for (const value of ['https://example.com','http://192.168.1.5:17831','http://user:secret@localhost:17831','http://localhost:17831/another']) assert.throws(()=>backendURL(value));
});
