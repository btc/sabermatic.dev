interface QueueEntry {
  data: string;
  seq: number;
}

// TODO: Add tests for AudioPlayer — requires AudioContext mock infrastructure
export class AudioPlayer {
  private ctx: AudioContext | null = null;
  private queue: QueueEntry[] = [];
  private _isPlaying = false;
  private isDone = false;
  private nextExpectedSeq = 0;
  private activeSource: AudioBufferSourceNode | null = null;
  private generation = 0;

  onComplete: (() => void) | null = null;

  get isPlaying(): boolean {
    return this._isPlaying;
  }

  initContext(): void {
    if (!this.ctx) {
      this.ctx = new AudioContext();
    }
  }

  enqueue(data: string, seq: number): void {
    this.queue.push({ data, seq });
    this.queue.sort((a, b) => a.seq - b.seq);

    if (!this._isPlaying) {
      this._isPlaying = true;
      this.playNext();
    }
  }

  private playNext(): void {
    if (!this.ctx) {
      // No context yet — nothing to play
      this._isPlaying = false;
      return;
    }

    // Find the next in-order chunk
    const idx = this.queue.findIndex((e) => e.seq === this.nextExpectedSeq);
    if (idx === -1) {
      // Nothing ready yet — wait for more enqueue calls or done()
      this._isPlaying = this.queue.length > 0;
      if (!this._isPlaying && this.isDone) {
        this.onComplete?.();
      }
      return;
    }

    const entry = this.queue.splice(idx, 1)[0]!;
    this.nextExpectedSeq++;
    this._isPlaying = true;

    const binary = atob(entry.data);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }

    this.ctx.decodeAudioData(
      bytes.buffer.slice(0),
      (audioBuffer) => {
        if (!this.ctx || !this._isPlaying) {
          // Cancelled while decoding
          if (this.queue.length === 0 && this.isDone) {
            this.onComplete?.();
          }
          return;
        }

        const source = this.ctx.createBufferSource();
        source.buffer = audioBuffer;
        source.connect(this.ctx.destination);
        this.activeSource = source;
        const capturedGeneration = this.generation;
        source.onended = () => {
          if (this.generation === capturedGeneration) {
            this.playNext();
          }
        };
        source.start();
      },
      () => {
        // Bad chunk — skip and continue with the next one
        this.playNext();
      },
    );
  }

  done(): void {
    this.isDone = true;
    // If nothing is currently playing and queue is empty, fire onComplete now
    if (!this._isPlaying && this.queue.length === 0) {
      this.onComplete?.();
    }
  }

  cancel(): void {
    this.generation++;
    this.activeSource?.stop();
    this.activeSource = null;
    this.queue = [];
    this._isPlaying = false;
    this.isDone = false;
    this.nextExpectedSeq = 0;

    if (this.ctx) {
      // Close and recreate to interrupt any active source nodes
      const old = this.ctx;
      this.ctx = null;
      old.close().catch(() => {});
    }
  }

  destroy(): void {
    this.cancel();
    // ctx already closed by cancel()
  }
}
