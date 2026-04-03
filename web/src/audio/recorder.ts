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

  async start(): Promise<void> {
    if (this._isRecording) return;

    this.stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const mimeType = AudioRecorder.preferredMimeType();
    const options = mimeType ? { mimeType } : undefined;
    this.mediaRecorder = new MediaRecorder(this.stream, options);

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

  stop(): void {
    if (!this._isRecording || !this.mediaRecorder) return;
    this.mediaRecorder.stop();
    this._isRecording = false;
  }

  async submit(): Promise<string> {
    if (this.segments.length === 0) {
      throw new Error("No segments to submit");
    }

    const mimeType = this.segments[0]?.type || "audio/webm";
    const combined = new Blob(this.segments, { type: mimeType });
    this.segments = [];

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
  }
}
