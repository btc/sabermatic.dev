interface QueueEntry {
  data: Uint8Array;
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

  async initContext(): Promise<void> {
    if (!this.ctx) {
      this.ctx = new AudioContext();
    }
    if (this.ctx.state === "suspended") {
      await this.ctx.resume();
    }
    // Kick off playback if chunks arrived before context was ready.
    if (this.queue.length > 0 && !this._isPlaying) {
      this._isPlaying = true;
      this.playNext();
    }
  }

  enqueue(data: Uint8Array, seq: number): void {
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

    // Copy to a standalone ArrayBuffer — decodeAudioData requires ownership
    // and Uint8Array.buffer may be a view into a larger allocation.
    const arrayBuf = entry.data.slice().buffer as ArrayBuffer;
    this.ctx.decodeAudioData(
      arrayBuf,
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
    // Keep this.ctx alive — AudioContexts are expensive to create and
    // browsers limit the number of active instances. Generation increment
    // + source.stop() is sufficient to cancel playback.
  }

  destroy(): void {
    this.cancel();
    if (this.ctx) {
      this.ctx.close().catch(() => {});
      this.ctx = null;
    }
  }
}
