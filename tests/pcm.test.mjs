import test from 'node:test';
import assert from 'node:assert/strict';
import { PCMFrameEncoder } from '../internal/web/static/pcm.js';

test('one second of worklet-sized blocks produces ten contiguous 100 ms PCM frames', () => {
  const frames = [];
  const encoder = new PCMFrameEncoder((buffer) => frames.push(buffer));
  for (let block = 0; block < 125; block++) encoder.push([new Float32Array(128).fill(0.5)]);
  encoder.flush();
  assert.equal(frames.length, 10);
  for (const [index, frame] of frames.entries()) {
    const view = new DataView(frame);
    assert.equal(frame.byteLength, 3212);
    assert.equal(view.getUint32(0, true), index);
    assert.equal(view.getBigUint64(4, true), BigInt(index * 1600));
    assert.equal(view.getInt16(12, true), 16384);
  }
});

test('stereo downmix, clipping and final partial frame preserve valid PCM samples', () => {
  const frames = [];
  const encoder = new PCMFrameEncoder((buffer) => frames.push(buffer));
  encoder.push([new Float32Array([1, -1, 3, -3, NaN]), new Float32Array([-1, 1, 3, -3, 0])]);
  encoder.flush(); encoder.flush();
  assert.equal(frames.length, 1);
  const view = new DataView(frames[0]);
  assert.equal(view.byteLength, 22);
  assert.deepEqual(Array.from({length: 5}, (_, i) => view.getInt16(12 + i * 2, true)), [0, 0, 32767, -32768, 0]);
});

test('a temporarily silent graph still advances the capture timeline', () => {
  const frames = [];
  const encoder = new PCMFrameEncoder((buffer) => frames.push(buffer));
  encoder.sampleIndex = 2 ** 32;
  encoder.push([], 1600);
  const view = new DataView(frames[0]);
  assert.equal(view.getBigUint64(4, true), 4294967296n);
  assert.equal(view.getInt16(12, true), 0);
});
