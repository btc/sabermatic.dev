import { describe, expect, it, vi } from "vitest";

import {
  AudioPlayer,
  parsePCMMimeType,
  pcmToAudioBuffer,
  type PlayerAudioContext,
} from "../player";

// ---------------------------------------------------------------------------
// Fake Web Audio
// ---------------------------------------------------------------------------

class FakeBuffer {
  readonly channels: Float32Array[];
  constructor(
    readonly numberOfChannels: number,
    readonly length: number,
    readonly sampleRate: number,
  ) {
    this.channels = Array.from({ length: numberOfChannels }, () => new Float32Array(length));
  }
  get duration(): number {
    return this.length / this.sampleRate;
  }
  getChannelData(c: number): Float32Array {
    return this.channels[c]!;
  }
}

class FakeSource {
  buffer: FakeBuffer | null = null;
  onended: (() => void) | null = null;
  startedAt: number | null = null;
  stopped = false;
  connect = vi.fn();
  start(when = 0) {
    this.startedAt = when;
  }
  stop() {
    this.stopped = true;
  }
  /** Simulates the browser finishing playback of this source. */
  end() {
    this.onended?.();
  }
}

interface Deferred {
  data: Uint8Array;
  resolve: (b: FakeBuffer) => void;
  reject: (e: Error) => void;
}

class FakeContext {
  state: AudioContextState = "running";
  currentTime = 0;
  destination = {} as AudioNode;
  sources: FakeSource[] = [];
  decodes: Deferred[] = [];
  resume = vi.fn(async () => {
    this.state = "running";
  });
  close = vi.fn(async () => {});
  createBuffer(channels: number, length: number, rate: number) {
    return new FakeBuffer(channels, length, rate);
  }
  createBufferSource() {
    const s = new FakeSource();
    this.sources.push(s);
    return s;
  }
  decodeAudioData(data: ArrayBuffer) {
    return new Promise<FakeBuffer>((resolve, reject) => {
      this.decodes.push({ data: new Uint8Array(data), resolve, reject });
    });
  }
}

const PCM = "audio/L16;rate=24000;channels=1";

/** `frames` samples of mono PCM, every sample equal to `value`. */
function pcm(frames: number, value = 0): Uint8Array {
  const out = new Uint8Array(frames * 2);
  const view = new DataView(out.buffer);
  for (let i = 0; i < frames; i++) view.setInt16(i * 2, value, true);
  return out;
}

async function setup() {
  const ctx = new FakeContext();
  const player = new AudioPlayer(() => ctx as unknown as PlayerAudioContext);
  const onComplete = vi.fn();
  player.onComplete = onComplete;
  await player.initContext();
  return { ctx, player, onComplete };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

describe("parsePCMMimeType", () => {
  it("parses rate and channels", () => {
    expect(parsePCMMimeType("audio/L16;rate=24000;channels=2")).toEqual({
      sampleRate: 24000,
      channels: 2,
    });
  });
  it("is case-insensitive, tolerates spaces, defaults to mono", () => {
    expect(parsePCMMimeType("Audio/l16; rate=16000")).toEqual({ sampleRate: 16000, channels: 1 });
  });
  it.each(["", "audio/mpeg", "audio/L16", "audio/L16;rate=abc", "audio/L16;rate=24000;channels=0"])(
    "rejects %j",
    (m) => {
      expect(parsePCMMimeType(m)).toBeNull();
    },
  );
});

describe("pcmToAudioBuffer", () => {
  it("converts little-endian int16 to floats and de-interleaves channels", () => {
    const ctx = new FakeContext() as unknown as PlayerAudioContext;
    // Two stereo frames: (L=16384, R=-32768), (L=0, R=32767), plus one stray byte.
    const data = new Uint8Array(9);
    const v = new DataView(data.buffer);
    v.setInt16(0, 16384, true);
    v.setInt16(2, -32768, true);
    v.setInt16(4, 0, true);
    v.setInt16(6, 32767, true);
    const buf = pcmToAudioBuffer(ctx, data, { sampleRate: 24000, channels: 2 })!;
    expect(buf.length).toBe(2);
    expect(Array.from(buf.getChannelData(0))).toEqual([0.5, 0]);
    expect(Array.from(buf.getChannelData(1))).toEqual([-1, 32767 / 32768]);
  });
  it("respects the view's byte offset", () => {
    const ctx = new FakeContext() as unknown as PlayerAudioContext;
    const backing = new Uint8Array([9, 9, 0, 64]); // second sample = 16384
    const buf = pcmToAudioBuffer(ctx, backing.subarray(2), { sampleRate: 8000, channels: 1 })!;
    expect(Array.from(buf.getChannelData(0))).toEqual([0.5]);
  });
  it("returns null when there is no whole frame", () => {
    const ctx = new FakeContext() as unknown as PlayerAudioContext;
    expect(pcmToAudioBuffer(ctx, new Uint8Array(1), { sampleRate: 24000, channels: 1 })).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// AudioPlayer
// ---------------------------------------------------------------------------

describe("AudioPlayer with PCM stream pieces", () => {
  it("schedules pieces back to back with no gaps", async () => {
    const { ctx, player } = await setup();
    player.enqueue(pcm(2400), 0, PCM); // 0.1 s each
    player.enqueue(pcm(2400), 1, PCM);
    player.enqueue(pcm(4800), 2, PCM); // 0.2 s
    expect(ctx.sources.map((s) => s.startedAt)).toEqual([0, 0.1, 0.2]);
    expect(player.isPlaying).toBe(true);
  });

  it("plays pieces in seq order when they arrive out of order", async () => {
    const { ctx, player } = await setup();
    player.enqueue(pcm(2400, 2), 1, PCM);
    expect(ctx.sources).toHaveLength(0);
    player.enqueue(pcm(2400, 1), 0, PCM);
    expect(ctx.sources.map((s) => s.buffer!.getChannelData(0)[0])).toEqual([1 / 32768, 2 / 32768]);
    expect(ctx.sources.map((s) => s.startedAt)).toEqual([0, 0.1]);
  });

  it("starts a late piece immediately after an underrun", async () => {
    const { ctx, player } = await setup();
    player.enqueue(pcm(2400), 0, PCM);
    ctx.sources[0]!.end();
    ctx.currentTime = 0.5;
    player.enqueue(pcm(2400), 1, PCM);
    expect(ctx.sources[1]!.startedAt).toBe(0.5);
  });

  it("skips a piece with an unplayable format and continues", async () => {
    const { ctx, player } = await setup();
    player.enqueue(pcm(2400), 0, "audio/L16;rate=bad");
    player.enqueue(pcm(2400), 1, PCM);
    expect(ctx.sources).toHaveLength(1);
    expect(ctx.sources[0]!.startedAt).toBe(0);
  });
});

describe("AudioPlayer with encoded files (backwards compatible path)", () => {
  it("decodes one file at a time and schedules them in order", async () => {
    const { ctx, player } = await setup();
    player.enqueue(new Uint8Array([1]), 0);
    player.enqueue(new Uint8Array([2]), 1);
    expect(ctx.decodes).toHaveLength(1);
    expect(Array.from(ctx.decodes[0]!.data)).toEqual([1]);

    ctx.decodes[0]!.resolve(new FakeBuffer(1, 24000, 24000)); // 1 s
    await flush();
    expect(ctx.decodes).toHaveLength(2);
    ctx.decodes[1]!.resolve(new FakeBuffer(1, 12000, 24000));
    await flush();
    expect(ctx.sources.map((s) => s.startedAt)).toEqual([0, 1]);
  });

  it("holds a later PCM piece until an earlier file is decoded", async () => {
    const { ctx, player } = await setup();
    player.enqueue(new Uint8Array([1]), 0);
    player.enqueue(pcm(2400), 1, PCM);
    expect(ctx.sources).toHaveLength(0);
    ctx.decodes[0]!.resolve(new FakeBuffer(1, 24000, 24000));
    await flush();
    expect(ctx.sources.map((s) => s.startedAt)).toEqual([0, 1]);
  });

  it("skips a file that fails to decode and continues", async () => {
    const { ctx, player } = await setup();
    player.enqueue(new Uint8Array([1]), 0);
    player.enqueue(new Uint8Array([2]), 1);
    ctx.decodes[0]!.reject(new Error("bad"));
    await flush();
    ctx.decodes[1]!.resolve(new FakeBuffer(1, 2400, 24000));
    await flush();
    expect(ctx.sources).toHaveLength(1);
  });
});

describe("AudioPlayer completion and control", () => {
  it("fires onComplete only after done() and every scheduled piece has ended", async () => {
    const { ctx, player, onComplete } = await setup();
    player.enqueue(pcm(2400), 0, PCM);
    player.enqueue(pcm(2400), 1, PCM);
    player.done();
    expect(onComplete).not.toHaveBeenCalled();
    ctx.sources[0]!.end();
    expect(onComplete).not.toHaveBeenCalled();
    ctx.sources[1]!.end();
    expect(onComplete).toHaveBeenCalledTimes(1);
    expect(player.isPlaying).toBe(false);
  });

  it("does not complete before done() even if everything has played", async () => {
    const { ctx, player, onComplete } = await setup();
    player.enqueue(pcm(2400), 0, PCM);
    ctx.sources[0]!.end();
    expect(onComplete).not.toHaveBeenCalled();
    player.done();
    expect(onComplete).toHaveBeenCalledTimes(1);
  });

  it("completes immediately when done() arrives with no audio", async () => {
    const { player, onComplete } = await setup();
    player.done();
    expect(onComplete).toHaveBeenCalledTimes(1);
  });

  it("queues chunks that arrive before the context exists and plays them on init", async () => {
    const ctx = new FakeContext();
    const player = new AudioPlayer(() => ctx as unknown as PlayerAudioContext);
    const onComplete = vi.fn();
    player.onComplete = onComplete;
    player.enqueue(pcm(2400), 0, PCM);
    player.done();
    expect(onComplete).not.toHaveBeenCalled();
    await player.initContext();
    expect(ctx.sources).toHaveLength(1);
    ctx.sources[0]!.end();
    expect(onComplete).toHaveBeenCalledTimes(1);
  });

  it("resumes a suspended context on init", async () => {
    const ctx = new FakeContext();
    ctx.state = "suspended";
    const player = new AudioPlayer(() => ctx as unknown as PlayerAudioContext);
    await player.initContext();
    expect(ctx.resume).toHaveBeenCalled();
  });

  it("cancel stops every scheduled piece, ignores late decodes, and restarts at seq 0", async () => {
    const { ctx, player, onComplete } = await setup();
    player.enqueue(pcm(2400), 0, PCM);
    player.enqueue(pcm(2400), 1, PCM);
    player.enqueue(new Uint8Array([1]), 2);
    player.cancel();
    expect(ctx.sources.every((s) => s.stopped)).toBe(true);
    expect(player.isPlaying).toBe(false);

    ctx.decodes[0]!.resolve(new FakeBuffer(1, 2400, 24000)); // from before the cancel
    await flush();
    expect(ctx.sources).toHaveLength(2);

    // Stopping fires onended in browsers; cancelled sources must not complete the next turn.
    ctx.sources[0]!.end();
    expect(onComplete).not.toHaveBeenCalled();

    ctx.currentTime = 3;
    player.enqueue(pcm(2400), 0, PCM);
    expect(ctx.sources).toHaveLength(3);
    expect(ctx.sources[2]!.startedAt).toBe(3);
  });

  it("destroy closes the context", async () => {
    const { ctx, player } = await setup();
    player.destroy();
    expect(ctx.close).toHaveBeenCalled();
  });
});
