import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AudioRecorder } from "../recorder";

// Minimal MediaRecorder mock that fires onstop synchronously when stop() is called.
function makeMediaRecorderMock() {
  const mr = {
    ondataavailable: null as ((e: { data: Blob }) => void) | null,
    onstop: null as ((e: Event) => void) | null,
    start() {
      // fire ondataavailable with a tiny chunk
      if (this.ondataavailable) {
        this.ondataavailable({ data: new Blob(["x"], { type: "audio/webm" }) });
      }
    },
    stop() {
      if (this.onstop) {
        this.onstop(new Event("stop"));
      }
    },
  };
  return mr;
}

function setupBrowserMocks() {
  // MediaRecorder class mock
  const MediaRecorderMock = vi.fn().mockImplementation(() => makeMediaRecorderMock());
  MediaRecorderMock.isTypeSupported = (type: string) => type === "audio/webm;codecs=opus";
  vi.stubGlobal("MediaRecorder", MediaRecorderMock);

  // getUserMedia mock
  const mockTrack = { stop: vi.fn(), readyState: "live" };
  const mockStream = { getTracks: () => [mockTrack] } as unknown as MediaStream;
  vi.stubGlobal("navigator", {
    mediaDevices: { getUserMedia: vi.fn().mockResolvedValue(mockStream) },
  });

  // AudioContext mock
  const analyserMock = {
    fftSize: 0,
    connect: vi.fn(),
  };
  const sourceMock = { connect: vi.fn() };
  const AudioContextMock = vi.fn().mockImplementation(() => ({
    createAnalyser: vi.fn().mockReturnValue(analyserMock),
    createMediaStreamSource: vi.fn().mockReturnValue(sourceMock),
    close: vi.fn().mockResolvedValue(undefined),
  }));
  vi.stubGlobal("AudioContext", AudioContextMock);
}

describe("AudioRecorder", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("starts with zero segments and not recording", () => {
    const recorder = new AudioRecorder();
    expect(recorder.segmentCount).toBe(0);
    expect(recorder.isRecording).toBe(false);
  });

  it("detects WebM/Opus as preferred mime type", () => {
    vi.stubGlobal("MediaRecorder", {
      isTypeSupported: (type: string) => type === "audio/webm;codecs=opus",
    });
    expect(AudioRecorder.preferredMimeType()).toBe("audio/webm;codecs=opus");
  });

  it("falls back to mp4 for Safari", () => {
    vi.stubGlobal("MediaRecorder", {
      isTypeSupported: (type: string) => type === "audio/mp4",
    });
    expect(AudioRecorder.preferredMimeType()).toBe("audio/mp4");
  });

  it("discard clears segments", () => {
    const recorder = new AudioRecorder();
    // Access internal segments for test setup
    (recorder as unknown as { segments: Blob[] }).segments = [new Blob(["a"]), new Blob(["b"])];
    expect(recorder.segmentCount).toBe(2);
    recorder.discard();
    expect(recorder.segmentCount).toBe(0);
  });

  it("submit returns Uint8Array and clears segments", async () => {
    const recorder = new AudioRecorder();
    (recorder as unknown as { segments: Blob[] }).segments = [new Blob(["hello"])];
    const result = await recorder.submit();
    expect(result).toBeInstanceOf(Uint8Array);
    expect(result.length).toBeGreaterThan(0);
    expect(recorder.segmentCount).toBe(0);
  });

  it("submit throws when no segments", async () => {
    const recorder = new AudioRecorder();
    await expect(recorder.submit()).rejects.toThrow("No segments to submit");
  });

  describe("duration tracking (with browser mocks)", () => {
    beforeEach(() => {
      setupBrowserMocks();
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it("tracks duration across segments", async () => {
      const recorder = new AudioRecorder();
      await recorder.start();
      await new Promise((r) => setTimeout(r, 100));
      await recorder.stop();
      expect(recorder.totalDuration).toBeGreaterThan(0);

      await recorder.start();
      await new Promise((r) => setTimeout(r, 100));
      await recorder.stop();
      expect(recorder.totalDuration).toBeGreaterThan(0.1);
    });

    it("resets duration on submit", async () => {
      const recorder = new AudioRecorder();
      await recorder.start();
      await new Promise((r) => setTimeout(r, 50));
      await recorder.stop();
      expect(recorder.totalDuration).toBeGreaterThan(0);
      await recorder.submit();
      expect(recorder.totalDuration).toBe(0);
    });

    it("resets duration on discard", async () => {
      const recorder = new AudioRecorder();
      await recorder.start();
      await new Promise((r) => setTimeout(r, 50));
      await recorder.stop();
      expect(recorder.totalDuration).toBeGreaterThan(0);
      recorder.discard();
      expect(recorder.totalDuration).toBe(0);
    });
  });
});
