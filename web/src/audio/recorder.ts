function blobToArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
  // Blob.arrayBuffer() is not available in all environments (e.g. jsdom).
  // FileReader is universally supported.
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(reader.error);
    reader.readAsArrayBuffer(blob);
  });
}

export class AudioRecorder {
  private segments: Blob[] = [];
  private mediaRecorder: MediaRecorder | null = null;
  private stream: MediaStream | null = null;
  private _isRecording = false;
  private audioCtx: AudioContext | null = null;
  private analyser: AnalyserNode | null = null;
  private _totalDuration = 0;
  private recordingStartTime = 0;

  static preferredMimeType(): string {
    if (MediaRecorder.isTypeSupported("audio/webm;codecs=opus")) {
      return "audio/webm;codecs=opus";
    }
    if (MediaRecorder.isTypeSupported("audio/mp4")) {
      return "audio/mp4";
    }
    return "";
  }

  get isRecording(): boolean {
    return this._isRecording;
  }

  get segmentCount(): number {
    return this.segments.length;
  }

  get analyserNode(): AnalyserNode | null { return this.analyser; }
  get totalDuration(): number { return this._totalDuration; }

  async start(): Promise<void> {
    if (this._isRecording) return;

    // Reuse existing stream if still active
    if (!this.stream || this.stream.getTracks().some((t) => t.readyState === "ended")) {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    }
    const mimeType = AudioRecorder.preferredMimeType();
    const options = mimeType ? { mimeType } : undefined;
    this.mediaRecorder = new MediaRecorder(this.stream, options);

    if (!this.audioCtx) {
      this.audioCtx = new AudioContext();
      const source = this.audioCtx.createMediaStreamSource(this.stream);
      this.analyser = this.audioCtx.createAnalyser();
      this.analyser.fftSize = 256;
      source.connect(this.analyser);
    }
    this.recordingStartTime = performance.now();

    const chunks: Blob[] = [];

    this.mediaRecorder.ondataavailable = (e) => {
      if (e.data.size > 0) {
        chunks.push(e.data);
      }
    };

    this.mediaRecorder.onstop = () => {
      if (chunks.length > 0) {
        const mtype = mimeType || "audio/webm";
        this.segments.push(new Blob(chunks, { type: mtype }));
      }
    };

    this.mediaRecorder.start();
    this._isRecording = true;
  }

  stop(): Promise<void> {
    if (!this._isRecording || !this.mediaRecorder) return Promise.resolve();
    const recorder = this.mediaRecorder;
    return new Promise<void>((resolve) => {
      const prev = recorder.onstop;
      recorder.onstop = (e) => {
        if (typeof prev === "function") prev.call(recorder, e);
        this._totalDuration += (performance.now() - this.recordingStartTime) / 1000;
        resolve();
      };
      recorder.stop();
      this._isRecording = false;
    });
  }

  async submit(): Promise<string> {
    if (this.segments.length === 0) {
      throw new Error("No segments to submit");
    }

    const mimeType = this.segments[0]?.type || "audio/webm";
    const combined = new Blob(this.segments, { type: mimeType });
    this.segments = [];
    this._totalDuration = 0;

    const buffer = await blobToArrayBuffer(combined);
    const bytes = new Uint8Array(buffer);
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]!);
    }
    return btoa(binary);
  }

  discard(): void {
    this.segments = [];
    this._totalDuration = 0;
  }

  destroy(): void {
    if (this._isRecording) {
      this.stop();
    }
    if (this.stream) {
      for (const track of this.stream.getTracks()) {
        track.stop();
      }
      this.stream = null;
    }
    this.mediaRecorder = null;
    this.segments = [];
    if (this.audioCtx) {
      this.audioCtx.close().catch(() => {});
      this.audioCtx = null;
      this.analyser = null;
    }
  }
}
