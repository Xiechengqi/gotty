import { OurXterm } from "./xterm";

declare var gotty_ws_query_args: string;

type StartMode = 'button' | 'hotkey';

export interface VoiceInputOptions {
    term: OurXterm;
    authToken: string;
    enabled: boolean;
    permitWrite: boolean;
    holdMs: number;
    hotkeyCode: string;
}

export class VoiceInput {
    private term: OurXterm;
    private authToken: string;
    private enabled: boolean;
    private permitWrite: boolean;
    private holdMs: number;
    private hotkeyCode: string;

    private button: HTMLButtonElement;
    private buttonIcon: HTMLElement;

    private positionTimer: number | null = null;

    private holdTimer: number | null = null;
    private holdArmed: boolean = false;

    private recording: boolean = false;
    private captureEnabled: boolean = false;
    private startMode: StartMode = 'button';

    private ws: WebSocket | null = null;

    private audioCtx: AudioContext | null = null;
    private recorder: ScriptProcessorNode | null = null;
    private mediaStream: MediaStream | null = null;
    private recordSampleRate: number = 48000;

    private expectedSampleRate: number = 16000;

    private segments: Map<number, string> = new Map();
    private stopFinalizeTimer: number | null = null;

    private encoder: TextEncoder = new TextEncoder();

    private termFocused: boolean = false;

    constructor(opts: VoiceInputOptions) {
        this.term = opts.term;
        this.authToken = opts.authToken;
        this.enabled = opts.enabled;
        this.permitWrite = opts.permitWrite;
        this.holdMs = opts.holdMs;
        this.hotkeyCode = opts.hotkeyCode;

        this.button = document.createElement('button');
        this.button.type = 'button';
        this.button.className = 'gotty-voice-btn';
        this.button.title = '语音输入（点击开始/停止；按住右 Shift 开始，松开停止）';
        this.button.setAttribute('aria-label', 'Voice input');
        this.buttonIcon = document.createElement('span');
        this.buttonIcon.className = 'gotty-voice-btn__icon';
        this.buttonIcon.innerHTML = `
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false">
              <path fill="currentColor" d="M12 14a3 3 0 0 0 3-3V6a3 3 0 0 0-6 0v5a3 3 0 0 0 3 3Zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-2Z"/>
            </svg>
        `;
        this.button.appendChild(this.buttonIcon);

        this.button.addEventListener('click', () => {
            if (!this.enabled) return;
            if (this.recording) {
                this.stop();
            } else {
                this.start('button');
            }
        });

        this.term.elem.appendChild(this.button);

        this.term.elem.addEventListener('focusin', () => { this.termFocused = true; });
        this.term.elem.addEventListener('focusout', () => { this.termFocused = false; });

        document.addEventListener('keydown', this.onKeyDownCapture, true);
        document.addEventListener('keyup', this.onKeyUpCapture, true);
        window.addEventListener('blur', this.onWindowBlur);
        document.addEventListener('visibilitychange', this.onVisibilityChange);

        this.positionTimer = window.setInterval(() => {
            this.updateUI();
        }, 100);

        this.updateUI();
    }

    close() {
        this.cancelHold();
        if (this.recording) {
            this.finalizeStop(true);
        }

        if (this.positionTimer !== null) {
            window.clearInterval(this.positionTimer);
            this.positionTimer = null;
        }

        document.removeEventListener('keydown', this.onKeyDownCapture, true);
        document.removeEventListener('keyup', this.onKeyUpCapture, true);
        window.removeEventListener('blur', this.onWindowBlur);
        document.removeEventListener('visibilitychange', this.onVisibilityChange);

        try {
            this.term.elem.removeChild(this.button);
        } catch (_) {
        }
    }

    private updateUI() {
        if (!this.enabled) {
            this.button.style.display = 'none';
            return;
        }

        this.button.style.display = '';
        this.button.disabled = false;
        this.button.classList.toggle('gotty-voice-btn--disabled', !this.permitWrite || !this.canAccessMicFromHere());
        if (!this.permitWrite) {
            this.button.title = '语音输入不可用：gotty 需要 --permit-write';
        } else if (!this.canAccessMicFromHere()) {
            this.button.title = '语音输入不可用：需要 HTTPS（或 localhost）才能访问麦克风';
        } else {
            this.button.title = '语音输入（点击开始/停止；按住右 Shift 开始，松开停止）';
        }

        if (this.recording) {
            this.button.classList.add('gotty-voice-btn--active');
        } else {
            this.button.classList.remove('gotty-voice-btn--active');
        }

        const anchor = this.term.getCursorAnchor();
        if (!anchor) {
            this.button.classList.add('gotty-voice-btn--fallback');
            this.button.style.left = '';
            this.button.style.top = '';
            return;
        }

        this.button.classList.remove('gotty-voice-btn--fallback');
        const offsetX = 6;
        const offsetY = Math.max(0, Math.floor((anchor.height - 28) / 2));
        this.button.style.left = `${anchor.left + offsetX}px`;
        this.button.style.top = `${anchor.top + offsetY}px`;
    }

    private onKeyDownCapture = (ev: KeyboardEvent) => {
        if (!this.enabled) return;
        if (!this.permitWrite) return;
        if (!this.termFocused) return;

        if (this.recording) {
            return;
        }

        if (this.holdArmed && ev.code !== this.hotkeyCode) {
            this.cancelHold();
            return;
        }

        if (ev.code !== this.hotkeyCode) return;
        if (ev.repeat) return;
        if (ev.altKey || ev.ctrlKey || ev.metaKey) return;

        this.armHold();
    };

    private onKeyUpCapture = (ev: KeyboardEvent) => {
        if (ev.code !== this.hotkeyCode) return;

        if (this.holdArmed) {
            this.cancelHold();
            return;
        }

        if (this.recording && this.startMode === 'hotkey') {
            this.stop();
        }
    };

    private onWindowBlur = () => {
        this.cancelHold();
        if (this.recording && this.startMode === 'hotkey') {
            this.stop();
        }
    };

    private onVisibilityChange = () => {
        if (document.visibilityState !== 'visible') {
            this.cancelHold();
            if (this.recording && this.startMode === 'hotkey') {
                this.stop();
            }
        }
    };

    private armHold() {
        if (this.holdArmed) return;
        this.holdArmed = true;
        this.holdTimer = window.setTimeout(() => {
            this.holdTimer = null;
            this.holdArmed = false;
            this.start('hotkey');
        }, this.holdMs);
    }

    private cancelHold() {
        this.holdArmed = false;
        if (this.holdTimer !== null) {
            window.clearTimeout(this.holdTimer);
            this.holdTimer = null;
        }
    }

    private canAccessMicFromHere(): boolean {
        if (!navigator.mediaDevices?.getUserMedia) return false;
        if (window.isSecureContext) return true;
        return window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1';
    }

    private buildAsrWsUrl(): string {
        const httpsEnabled = window.location.protocol === "https:";
        const proto = httpsEnabled ? 'wss://' : 'ws://';
        const queryArgs = (typeof gotty_ws_query_args === 'string' && gotty_ws_query_args !== "") ? "?" + gotty_ws_query_args : "";
        return proto + window.location.host + window.location.pathname + 'asr/ws' + queryArgs;
    }

    private async start(mode: StartMode) {
        if (!this.enabled) return;
        if (!this.permitWrite) {
            this.term.showMessage("语音输入不可用：未启用写入权限（gotty 需要 --permit-write）", 2500);
            return;
        }
        if (this.recording) return;
        if (!navigator.mediaDevices?.getUserMedia) {
            this.term.showMessage("语音输入不可用：浏览器不支持麦克风接口（getUserMedia）", 2500);
            return;
        }
        if (!this.canAccessMicFromHere()) {
            this.term.showMessage("语音输入需要 HTTPS（或 localhost）才能访问麦克风", 2500);
            return;
        }

        this.startMode = mode;
        this.recording = true;
        this.captureEnabled = true;
        this.segments.clear();

        this.term.disableStdin();
        this.term.showMessage("录音中，停止后插入", 0);

        try {
            const wsUrl = this.buildAsrWsUrl();
            const ws = new WebSocket(wsUrl);
            ws.binaryType = 'arraybuffer';
            this.ws = ws;

            ws.addEventListener('message', (event: MessageEvent) => {
                try {
                    const message = JSON.parse(event.data);
                    const segment = Number(message.segment);
                    const text = String(message.text ?? "");
                    if (Number.isFinite(segment)) {
                        this.segments.set(segment, text);
                    }

                    if (this.stopFinalizeTimer !== null) {
                        window.clearTimeout(this.stopFinalizeTimer);
                        this.stopFinalizeTimer = window.setTimeout(() => this.finalizeStop(), 250);
                    }
                } catch (_) {
                }
            });

            ws.addEventListener('close', () => {
                if (this.stopFinalizeTimer !== null) {
                    window.clearTimeout(this.stopFinalizeTimer);
                }
                this.finalizeStop();
            });

            ws.addEventListener('error', () => {
                this.term.showMessage("语音输入连接失败", 2000);
                this.finalizeStop(true);
            });

            await new Promise<void>((resolve, reject) => {
                const timeout = window.setTimeout(() => {
                    reject(new Error("ASR websocket connect timeout"));
                }, 5000);
                ws.addEventListener('open', () => {
                    window.clearTimeout(timeout);
                    resolve();
                }, { once: true });
                ws.addEventListener('close', () => {
                    window.clearTimeout(timeout);
                    reject(new Error("ASR websocket closed before open"));
                }, { once: true });
            });

            ws.send(JSON.stringify({ AuthToken: this.authToken, Arguments: "" }));

            await this.startAudioCapture();
        } catch (e) {
            this.term.showMessage("无法开始语音输入：" + String(e), 2500);
            this.finalizeStop(true);
        }
    }

    private async startAudioCapture() {
        this.mediaStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        this.audioCtx = new AudioContext();
        this.recordSampleRate = this.audioCtx.sampleRate;

        const source = this.audioCtx.createMediaStreamSource(this.mediaStream);
        const bufferSize = 2048;
        this.recorder = this.audioCtx.createScriptProcessor(bufferSize, 1, 1);
        const gain = this.audioCtx.createGain();
        gain.gain.value = 0;

        this.recorder.onaudioprocess = (e) => {
            if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
            if (!this.captureEnabled) return;

            const input = new Float32Array(e.inputBuffer.getChannelData(0));
            const samples = this.downsampleBuffer(input, this.expectedSampleRate);
            this.ws.send(samples);
        };

        source.connect(this.recorder);
        this.recorder.connect(gain);
        gain.connect(this.audioCtx.destination);
    }

    private stop() {
        if (!this.recording) return;
        this.cancelHold();

        this.captureEnabled = false;
        this.cleanupAudio();

        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            try {
                this.ws.send("Done");
            } catch (_) {
            }
        }

        if (this.stopFinalizeTimer !== null) {
            window.clearTimeout(this.stopFinalizeTimer);
        }
        this.stopFinalizeTimer = window.setTimeout(() => this.finalizeStop(), 700);
    }

    private finalizeStop(skipInsert: boolean = false) {
        if (!this.recording) return;

        this.recording = false;
        this.captureEnabled = false;
        if (this.stopFinalizeTimer !== null) {
            window.clearTimeout(this.stopFinalizeTimer);
            this.stopFinalizeTimer = null;
        }

        const text = Array.from(this.segments.entries())
            .sort((a, b) => a[0] - b[0])
            .map(([, t]) => (t ?? "").trim())
            .filter((t) => t.length > 0)
            .join(" ");

        if (!skipInsert && text.length > 0) {
            this.term.sendInput(this.encoder.encode(text));
        }

        this.term.enableStdin();
        this.term.removeMessage();
        this.cleanupAudio();
        this.cleanupWs();
        this.updateUI();
    }

    private cleanupWs() {
        if (this.ws) {
            try {
                this.ws.onmessage = null;
                this.ws.onclose = null;
                this.ws.onerror = null;
                if (this.ws.readyState === WebSocket.OPEN) {
                    this.ws.close();
                }
            } catch (_) {
            }
        }
        this.ws = null;
    }

    private cleanupAudio() {
        try {
            if (this.recorder) {
                this.recorder.disconnect();
                this.recorder.onaudioprocess = null;
            }
        } catch (_) {
        }
        this.recorder = null;

        try {
            if (this.audioCtx && this.audioCtx.state !== 'closed') {
                this.audioCtx.close();
            }
        } catch (_) {
        }
        this.audioCtx = null;

        try {
            if (this.mediaStream) {
                this.mediaStream.getTracks().forEach(t => t.stop());
            }
        } catch (_) {
        }
        this.mediaStream = null;
    }

    private downsampleBuffer(buffer: Float32Array, exportSampleRate: number): Float32Array {
        if (exportSampleRate === this.recordSampleRate) {
            return buffer;
        }
        const sampleRateRatio = this.recordSampleRate / exportSampleRate;
        const newLength = Math.round(buffer.length / sampleRateRatio);
        const result = new Float32Array(newLength);
        let offsetResult = 0;
        let offsetBuffer = 0;
        while (offsetResult < result.length) {
            const nextOffsetBuffer = Math.round((offsetResult + 1) * sampleRateRatio);
            let accum = 0;
            let count = 0;
            for (let i = offsetBuffer; i < nextOffsetBuffer && i < buffer.length; i++) {
                accum += buffer[i];
                count++;
            }
            result[offsetResult] = count > 0 ? (accum / count) : 0;
            offsetResult++;
            offsetBuffer = nextOffsetBuffer;
        }
        return result;
    }
}
