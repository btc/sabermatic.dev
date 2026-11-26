// AudioPlayer plays the interviewer's voice from TtsChunk messages.
//
// A chunk is either a complete encoded file (MP3 or WAV, empty mimeType),
// which is decoded with decodeAudioData, or a piece of a PCM stream
// ("audio/L16;rate=R;channels=C"), which is copied into an AudioBuffer
// directly. Chunks are played in seq order. Each buffer is scheduled to start
// exactly when the previous one ends, so stream pieces that arrive at uneven
// intervals still play without gaps.

/** The subset of AudioContext the player uses. Tests supply a fake. */
export interface PlayerAudioContext {
  readonly state: AudioContextState;
  readonly currentTime: number;
  readonly destination: AudioNode;
  resume(): Promise<void>;
  close(): Promise<void>;
  createBuffer(numberOfChannels: number, length: number, sampleRate: number): AudioBuffer;
  createBufferSource(): AudioBufferSourceNode;
  decodeAudioData(data: ArrayBuffer): Promise<AudioBuffer>;
}

export interface PCMFormat {
  sampleRate: number;
  channels: number;
}

/**
 * Parses "audio/L16;rate=R;channels=C" (case-insensitive, channels
 * optional and defaulting to 1). Returns null for any other type.
 */
export function parsePCMMimeType(mimeType: string): PCMFormat | null {
  const [type, ...params] = mimeType.split(";").map((s) => s.trim().toLowerCase());
  if (type !== "audio/l16") return null;
  let sampleRate = 0;
  let channels = 1;
  for (const p of params) {
    const [k, v] = p.split("=").map((s) => s.trim());
    const n = Number(v);
    if (k === "rate") sampleRate = n;
    if (k === "channels") channels = n;
  }
  if (!Number.isInteger(sampleRate) || sampleRate <= 0) return null;
  if (!Number.isInteger(channels) || channels <= 0) return null;
  return { sampleRate, channels };
}

/**
 * Converts interleaved 16-bit little-endian PCM to an AudioBuffer. A trailing
 * partial sample frame is ignored. Returns null if there is no whole frame.
 */
export function pcmToAudioBuffer(
  ctx: PlayerAudioContext,
  data: Uint8Array,
  format: PCMFormat,
): AudioBuffer | null {
  const frameBytes = 2 * format.channels;
  const frames = Math.floor(data.byteLength / frameBytes);
  if (frames === 0) return null;
  const view = new DataView(data.buffer, data.byteOffset, frames * frameBytes);
  const buffer = ctx.createBuffer(format.channels, frames, format.sampleRate);
  for (let c = 0; c < format.channels; c++) {
    const out = buffer.getChannelData(c);
    for (let i = 0; i < frames; i++) {
      out[i] = view.getInt16((i * format.channels + c) * 2, true) / 32768;
    }
  }
  return buffer;
}

interface QueueEntry {
  data: Uint8Array;
  seq: number;
  mimeType: string;
}

export class AudioPlayer {
  private readonly createContext: () => PlayerAudioContext;
  private ctx: PlayerAudioContext | null = null;
  private queue: QueueEntry[] = [];
  private _isPlaying = false;
  private isDone = false;
  private nextExpectedSeq = 0;
  // Context time at which the next buffer starts; 0 means "as soon as possible".
  private nextStartTime = 0;
  private readonly active = new Set<AudioBufferSourceNode>();
  // True while a chunk is being decoded; later chunks wait so order holds.
  private decoding = false;
  // Incremented by cancel(); callbacks from an earlier generation are ignored.
  private generation = 0;

  onComplete: (() => void) | null = null;

  constructor(createContext: () => PlayerAudioContext = () => new AudioContext()) {
    this.createContext = createContext;
  }

  get isPlaying(): boolean {
    return this._isPlaying;
  }

  async initContext(): Promise<void> {
    if (!this.ctx) {
      this.ctx = this.createContext();
    }
    if (this.ctx.state === "suspended") {
      await this.ctx.resume();
    }
    // Start playback if chunks arrived before the context was ready.
    this.pump();
  }

  enqueue(data: Uint8Array, seq: number, mimeType = ""): void {
    this.queue.push({ data, seq, mimeType });
    this.queue.sort((a, b) => a.seq - b.seq);
    this._isPlaying = true;
    this.pump();
  }

  done(): void {
    this.isDone = true;
    this.maybeComplete();
  }

  cancel(): void {
    this.generation++;
    for (const source of this.active) {
      source.onended = null;
      try {
        source.stop();
      } catch {
        // Already stopped.
      }
    }
    this.active.clear();
    this.queue = [];
    this._isPlaying = false;
    this.isDone = false;
    this.decoding = false;
    this.nextExpectedSeq = 0;
    this.nextStartTime = 0;
    // Keep this.ctx alive: AudioContexts are expensive to create and
    // browsers limit how many can be active.
  }

  destroy(): void {
    this.cancel();
    if (this.ctx) {
      this.ctx.close().catch(() => {});
      this.ctx = null;
    }
  }

  /** Schedules every queued chunk that is next in seq order. */
  private pump(): void {
    const ctx = this.ctx;
    if (!ctx) return;
    while (!this.decoding) {
      const idx = this.queue.findIndex((e) => e.seq === this.nextExpectedSeq);
      if (idx === -1) break;
      const entry = this.queue.splice(idx, 1)[0]!;
      this.nextExpectedSeq++;

      if (entry.mimeType === "") {
        this.decodeAndSchedule(ctx, entry.data);
        continue;
      }
      const format = parsePCMMimeType(entry.mimeType);
      const buffer = format ? pcmToAudioBuffer(ctx, entry.data, format) : null;
      if (buffer) this.schedule(ctx, buffer);
      // A chunk that cannot be played is skipped.
    }
    this.maybeComplete();
  }

  private decodeAndSchedule(ctx: PlayerAudioContext, data: Uint8Array): void {
    this.decoding = true;
    const generation = this.generation;
    // Copy to a standalone ArrayBuffer: decodeAudioData takes ownership, and
    // data.buffer may be a view into a larger allocation.
    const arrayBuf = data.slice().buffer as ArrayBuffer;
    ctx.decodeAudioData(arrayBuf).then(
      (buffer) => {
        if (generation !== this.generation) return;
        this.decoding = false;
        this.schedule(ctx, buffer);
        this.pump();
      },
      () => {
        // A file that cannot be decoded is skipped.
        if (generation !== this.generation) return;
        this.decoding = false;
        this.pump();
      },
    );
  }

  private schedule(ctx: PlayerAudioContext, buffer: AudioBuffer): void {
    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(ctx.destination);
    const startAt = Math.max(ctx.currentTime, this.nextStartTime);
    this.nextStartTime = startAt + buffer.duration;
    this.active.add(source);
    this._isPlaying = true;
    source.onended = () => {
      this.active.delete(source);
      this.maybeComplete();
    };
    source.start(startAt);
  }

  /** Fires onComplete once everything received has finished playing. */
  private maybeComplete(): void {
    // Chunks still queued (no context yet, or waiting on an earlier seq)
    // will play later, so the message is not complete.
    if (!this.isDone || this.decoding || this.active.size > 0 || this.queue.length > 0) return;
    this._isPlaying = false;
    this.isDone = false;
    this.onComplete?.();
  }
}
