import { PCMFrameEncoder } from './pcm.js';

class PCM16Processor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.running = true;
    this.encoder = new PCMFrameEncoder((frame, level) => {
      this.port.postMessage({ type: 'frame', frame, level }, [frame]);
    });
    this.port.onmessage = ({ data }) => {
      if (data.type === 'flush') {
        this.running = false;
        this.encoder.flush();
        this.port.postMessage({ type: 'flushed' });
      }
    };
  }

  process(inputs) {
    if (!this.running) return false;
    this.encoder.push(inputs[0] || []);
    return true;
  }
}

registerProcessor('pcm16-capture', PCM16Processor);
