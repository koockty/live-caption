// AudioContext is explicitly created at 16 kHz. Chrome performs band-limited
// resampling from the shared track before samples reach the AudioWorklet.
export class PCMFrameEncoder {
  constructor(onFrame, frameSamples = 1600) {
    this.onFrame = onFrame;
    this.frameSamples = frameSamples;
    this.samples = new Int16Array(frameSamples);
    this.used = 0;
    this.sequence = 0;
    this.sampleIndex = 0;
    this.energy = 0;
  }

  push(channels, silentLength = 128) {
    const length = channels[0]?.length ?? silentLength;
    for (let i = 0; i < length; i++) {
      let value = 0;
      for (const channel of channels) value += channel[i] ?? 0;
      value = channels.length ? value / channels.length : 0;
      value = Number.isFinite(value) ? Math.max(-1, Math.min(1, value)) : 0;
      this.samples[this.used++] = Math.round(value * (value < 0 ? 32768 : 32767));
      this.energy += value * value;
      if (this.used === this.frameSamples) this.emit();
    }
  }

  emit() {
    if (!this.used) return;
    const buffer = new ArrayBuffer(12 + this.used * 2);
    const view = new DataView(buffer);
    view.setUint32(0, this.sequence++, true);
    view.setBigUint64(4, BigInt(this.sampleIndex), true);
    for (let i = 0; i < this.used; i++) view.setInt16(12 + i * 2, this.samples[i], true);
    const level = Math.min(100, Math.sqrt(this.energy / this.used) * 260);
    this.sampleIndex += this.used;
    this.used = 0;
    this.energy = 0;
    this.onFrame(buffer, level);
  }

  flush() { this.emit(); }
}
