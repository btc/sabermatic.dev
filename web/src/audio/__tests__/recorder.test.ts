import { describe, it, expect, vi, beforeEach } from "vitest";
import { AudioRecorder } from "../recorder";

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

  it("submit returns base64 and clears segments", async () => {
    const recorder = new AudioRecorder();
    (recorder as unknown as { segments: Blob[] }).segments = [new Blob(["hello"])];
    const result = await recorder.submit();
    expect(typeof result).toBe("string");
    expect(result.length).toBeGreaterThan(0);
    expect(recorder.segmentCount).toBe(0);
  });

  it("submit throws when no segments", async () => {
    const recorder = new AudioRecorder();
    await expect(recorder.submit()).rejects.toThrow("No segments to submit");
  });
});
