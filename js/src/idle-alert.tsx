import { Component, render } from 'preact';

interface IdleAlertProps {
    timeout: number;
    onActivity: (cb: () => void) => () => void;
}

interface IdleAlertState {
    enabled: boolean;
    collapsed: boolean;
    timeoutSeconds: number;
    soundVolume: number;
    soundFrequencyHz: number;
    soundDurationMs: number;
    soundWaveform: OscillatorType;
    showSoundSettings: boolean;
}

const STORAGE_KEY_PREFIX = 'gotty.idleAlert.';
const MIN_TIMEOUT_SECONDS = 1;
const MAX_TIMEOUT_SECONDS = 86400;
const TICK_INTERVAL_MS = 250;

const SOUND_MIN_FREQUENCY_HZ = 100;
const SOUND_MAX_FREQUENCY_HZ = 4000;
const SOUND_MIN_DURATION_MS = 50;
const SOUND_MAX_DURATION_MS = 2000;

export class IdleAlert extends Component<IdleAlertProps, IdleAlertState> {
    private audioContext: AudioContext | null = null;
    private unsubscribeActivity: (() => void) | null = null;
    private intervalId: number | null = null;
    private lastActivityAtMs: number = Date.now();
    private waitingForActivityAfterBeep: boolean = false;
    private audioPreparedOnce: boolean = false;

    constructor(props: IdleAlertProps) {
        super(props);

        const storedEnabled = this.readBool('enabled');
        const storedCollapsed = this.readBool('collapsed');
        const storedTimeoutSeconds = this.readNumber('timeoutSeconds');
        const storedSoundVolume = this.readNumber('sound.volume');
        const storedSoundFrequencyHz = this.readNumber('sound.frequencyHz');
        const storedSoundDurationMs = this.readNumber('sound.durationMs');
        const storedSoundWaveform = this.readString('sound.waveform');

        const initialTimeout = this.clampTimeoutSeconds(
            typeof storedTimeoutSeconds === 'number' ? storedTimeoutSeconds : props.timeout
        );

        const initialSoundWaveform = this.clampWaveform(storedSoundWaveform ?? 'sine');

        this.state = {
            enabled: storedEnabled ?? false,
            collapsed: storedCollapsed ?? false,
            timeoutSeconds: initialTimeout,
            soundVolume: this.clampSoundVolume(typeof storedSoundVolume === 'number' ? storedSoundVolume : 0.3),
            soundFrequencyHz: this.clampSoundFrequencyHz(
                typeof storedSoundFrequencyHz === 'number' ? storedSoundFrequencyHz : 800
            ),
            soundDurationMs: this.clampSoundDurationMs(
                typeof storedSoundDurationMs === 'number' ? storedSoundDurationMs : 200
            ),
            soundWaveform: initialSoundWaveform,
            showSoundSettings: false,
        };
    }

    componentDidMount() {
        this.unsubscribeActivity = this.props.onActivity(() => {
            this.handleActivity();
        });

        if (this.state.enabled) {
            this.startTicker();
        }
    }

    componentWillUnmount() {
        if (this.unsubscribeActivity) {
            this.unsubscribeActivity();
            this.unsubscribeActivity = null;
        }
        this.stopTicker();
        if (this.audioContext) {
            this.audioContext.close();
        }
    }

    private toggleEnabled = () => {
        this.handleActivity();
        const newEnabled = !this.state.enabled;
        this.setState({ enabled: newEnabled }, () => {
            this.writeBool('enabled', this.state.enabled);
        });

        if (newEnabled) {
            // 浏览器安全策略通常要求用户交互后才能播放声音：在点击开启时初始化/恢复音频上下文
            this.prepareAudioContext();
            this.waitingForActivityAfterBeep = false;
            this.lastActivityAtMs = Date.now();
            this.startTicker();
        } else {
            this.stopTicker();
        }
    };

    private toggleCollapsed = () => {
        this.handleActivity();
        const nextCollapsed = !this.state.collapsed;
        this.setState(
            {
                collapsed: nextCollapsed,
                showSoundSettings: nextCollapsed ? false : this.state.showSoundSettings,
            },
            () => {
                this.writeBool('collapsed', this.state.collapsed);
            }
        );
    };

    private toggleSoundSettings = () => {
        this.handleActivity();
        this.setState({ showSoundSettings: !this.state.showSoundSettings });
    };

    private handleActivity() {
        if (!this.state.enabled) return;

        // 尝试在用户交互后恢复音频上下文（若之前因策略无法播放）
        if (!this.audioPreparedOnce) {
            this.audioPreparedOnce = true;
            this.prepareAudioContext();
        }

        this.lastActivityAtMs = Date.now();
        if (this.waitingForActivityAfterBeep) {
            this.waitingForActivityAfterBeep = false;
        }
    }

    private startTicker() {
        if (this.intervalId !== null) return;
        this.intervalId = window.setInterval(() => {
            this.tick();
        }, TICK_INTERVAL_MS);
    }

    private stopTicker() {
        if (this.intervalId === null) return;
        window.clearInterval(this.intervalId);
        this.intervalId = null;
    }

    private tick() {
        if (!this.state.enabled) return;
        if (this.waitingForActivityAfterBeep) return;

        const now = Date.now();
        const timeoutMs = this.state.timeoutSeconds * 1000;
        if (now - this.lastActivityAtMs < timeoutMs) return;

        this.waitingForActivityAfterBeep = true;
        this.playBeep();
    }

    private async prepareAudioContext() {
        try {
            if (!this.audioContext || this.audioContext.state === 'closed') {
                this.audioContext = new (window.AudioContext ||
                    (window as any).webkitAudioContext)();
            }
            if (this.audioContext.state === 'suspended') {
                await this.audioContext.resume();
            }
        } catch (e) {
            console.warn('Failed to prepare AudioContext:', e);
        }
    }

    private async playBeep() {
        try {
            if (!this.audioContext || this.audioContext.state === 'closed') {
                await this.prepareAudioContext();
            }
            if (!this.audioContext) return;

            const oscillator = this.audioContext.createOscillator();
            const gainNode = this.audioContext.createGain();

            oscillator.connect(gainNode);
            gainNode.connect(this.audioContext.destination);

            oscillator.frequency.value = this.state.soundFrequencyHz;
            oscillator.type = this.state.soundWaveform;

            const now = this.audioContext.currentTime;
            const durationSeconds = this.state.soundDurationMs / 1000;
            const attackSeconds = Math.min(0.01, durationSeconds / 4);
            const releaseSeconds = Math.min(0.03, durationSeconds / 3);
            const sustainSeconds = Math.max(0, durationSeconds - attackSeconds - releaseSeconds);

            gainNode.gain.setValueAtTime(0, now);
            gainNode.gain.linearRampToValueAtTime(this.state.soundVolume, now + attackSeconds);
            gainNode.gain.setValueAtTime(this.state.soundVolume, now + attackSeconds + sustainSeconds);
            gainNode.gain.linearRampToValueAtTime(0, now + attackSeconds + sustainSeconds + releaseSeconds);

            oscillator.start();
            oscillator.stop(now + durationSeconds);
        } catch (e) {
            console.warn('Failed to play beep:', e);
        }
    }

    private async testBeep() {
        this.handleActivity();
        await this.prepareAudioContext();
        await this.playBeep();
    }

    private clampTimeoutSeconds(value: number): number {
        if (!Number.isFinite(value)) return this.clampTimeoutSeconds(this.props.timeout);
        if (value < MIN_TIMEOUT_SECONDS) return MIN_TIMEOUT_SECONDS;
        if (value > MAX_TIMEOUT_SECONDS) return MAX_TIMEOUT_SECONDS;
        return Math.floor(value);
    }

    private clampSoundVolume(value: number): number {
        if (!Number.isFinite(value)) return 0.3;
        if (value < 0) return 0;
        if (value > 1) return 1;
        return value;
    }

    private clampSoundFrequencyHz(value: number): number {
        if (!Number.isFinite(value)) return 800;
        if (value < SOUND_MIN_FREQUENCY_HZ) return SOUND_MIN_FREQUENCY_HZ;
        if (value > SOUND_MAX_FREQUENCY_HZ) return SOUND_MAX_FREQUENCY_HZ;
        return Math.floor(value);
    }

    private clampSoundDurationMs(value: number): number {
        if (!Number.isFinite(value)) return 200;
        if (value < SOUND_MIN_DURATION_MS) return SOUND_MIN_DURATION_MS;
        if (value > SOUND_MAX_DURATION_MS) return SOUND_MAX_DURATION_MS;
        return Math.floor(value);
    }

    private clampWaveform(value: string): OscillatorType {
        if (value === 'sine' || value === 'square' || value === 'triangle' || value === 'sawtooth') {
            return value;
        }
        return 'sine';
    }

    private readBool(key: string): boolean | null {
        try {
            const value = localStorage.getItem(STORAGE_KEY_PREFIX + key);
            if (value === null) return null;
            if (value === 'true') return true;
            if (value === 'false') return false;
            return null;
        } catch {
            return null;
        }
    }

    private readNumber(key: string): number | null {
        try {
            const value = localStorage.getItem(STORAGE_KEY_PREFIX + key);
            if (value === null) return null;
            const parsed = Number(value);
            return Number.isFinite(parsed) ? parsed : null;
        } catch {
            return null;
        }
    }

    private readString(key: string): string | null {
        try {
            const value = localStorage.getItem(STORAGE_KEY_PREFIX + key);
            return value === null ? null : value;
        } catch {
            return null;
        }
    }

    private writeBool(key: string, value: boolean) {
        try {
            localStorage.setItem(STORAGE_KEY_PREFIX + key, value ? 'true' : 'false');
        } catch {
            // ignore
        }
    }

    private writeNumber(key: string, value: number) {
        try {
            localStorage.setItem(STORAGE_KEY_PREFIX + key, String(value));
        } catch {
            // ignore
        }
    }

    private setTimeoutSeconds(nextValue: number) {
        const clamped = this.clampTimeoutSeconds(nextValue);
        this.setState({ timeoutSeconds: clamped }, () => {
            this.writeNumber('timeoutSeconds', this.state.timeoutSeconds);
            // 修改 timeout 也算活动：从当前时刻开始重新计时
            this.handleActivity();
        });
    }

    private setSoundVolume(nextValue: number) {
        this.handleActivity();
        const clamped = this.clampSoundVolume(nextValue);
        this.setState({ soundVolume: clamped }, () => {
            this.writeNumber('sound.volume', this.state.soundVolume);
        });
    }

    private setSoundFrequencyHz(nextValue: number) {
        this.handleActivity();
        const clamped = this.clampSoundFrequencyHz(nextValue);
        this.setState({ soundFrequencyHz: clamped }, () => {
            this.writeNumber('sound.frequencyHz', this.state.soundFrequencyHz);
        });
    }

    private setSoundDurationMs(nextValue: number) {
        this.handleActivity();
        const clamped = this.clampSoundDurationMs(nextValue);
        this.setState({ soundDurationMs: clamped }, () => {
            this.writeNumber('sound.durationMs', this.state.soundDurationMs);
        });
    }

    private setSoundWaveform(nextValue: string) {
        this.handleActivity();
        const clamped = this.clampWaveform(nextValue);
        this.setState({ soundWaveform: clamped }, () => {
            this.writeString('sound.waveform', this.state.soundWaveform);
        });
    }

    private writeString(key: string, value: string) {
        try {
            localStorage.setItem(STORAGE_KEY_PREFIX + key, value);
        } catch {
            // ignore
        }
    }

    render() {
        const {
            enabled,
            collapsed,
            timeoutSeconds,
            showSoundSettings,
            soundVolume,
            soundFrequencyHz,
            soundDurationMs,
            soundWaveform,
        } = this.state;

        // 折叠状态：隐藏到右侧边缘，只显示小箭头
        if (collapsed) {
            return (
                <div class="idle-alert-collapsed"
                     onClick={this.toggleCollapsed}
                     title="展开空闲提醒">
                    &#8249;
                </div>
            );
        }

        // 展开状态：固定右上角，显示折叠按钮 + 喇叭图标
        return (
            <div class="idle-alert">
                <span class="idle-alert-collapse"
                      onClick={this.toggleCollapsed}
                      title="折叠到侧边">
                    &#8250;
                </span>
                <button
                    class="idle-alert-sound-button"
                    type="button"
                    onClick={this.toggleSoundSettings}
                    title="设置"
                >
                    ⚙
                </button>
                <span class={`idle-alert-icon ${enabled ? 'enabled' : ''}`}
                      onClick={this.toggleEnabled}
                      title={enabled ? '点击关闭空闲提醒' : '点击开启空闲提醒'}>
                    {enabled ? '\u{1F514}' : '\u{1F515}'}
                </span>
                {showSoundSettings && (
                    <div class="idle-alert-sound-panel">
                        <label class="idle-alert-sound-row">
                            <span class="idle-alert-sound-label">间隔</span>
                            <input
                                class="idle-alert-sound-number"
                                type="number"
                                min={MIN_TIMEOUT_SECONDS}
                                max={MAX_TIMEOUT_SECONDS}
                                step={1}
                                value={timeoutSeconds}
                                onInput={(e) => this.setTimeoutSeconds(Number((e.currentTarget as HTMLInputElement).value))}
                            />
                            <span class="idle-alert-sound-unit">秒</span>
                        </label>
                        <label class="idle-alert-sound-row">
                            <span class="idle-alert-sound-label">音量</span>
                            <input
                                class="idle-alert-sound-slider"
                                type="range"
                                min={0}
                                max={1}
                                step={0.01}
                                value={soundVolume}
                                onInput={(e) => this.setSoundVolume(Number((e.currentTarget as HTMLInputElement).value))}
                            />
                            <span class="idle-alert-sound-value">{soundVolume.toFixed(2)}</span>
                        </label>
                        <label class="idle-alert-sound-row">
                            <span class="idle-alert-sound-label">频率</span>
                            <input
                                class="idle-alert-sound-number"
                                type="number"
                                min={SOUND_MIN_FREQUENCY_HZ}
                                max={SOUND_MAX_FREQUENCY_HZ}
                                step={1}
                                value={soundFrequencyHz}
                                onInput={(e) => this.setSoundFrequencyHz(Number((e.currentTarget as HTMLInputElement).value))}
                            />
                            <span class="idle-alert-sound-unit">Hz</span>
                        </label>
                        <label class="idle-alert-sound-row">
                            <span class="idle-alert-sound-label">时长</span>
                            <input
                                class="idle-alert-sound-number"
                                type="number"
                                min={SOUND_MIN_DURATION_MS}
                                max={SOUND_MAX_DURATION_MS}
                                step={10}
                                value={soundDurationMs}
                                onInput={(e) => this.setSoundDurationMs(Number((e.currentTarget as HTMLInputElement).value))}
                            />
                            <span class="idle-alert-sound-unit">ms</span>
                        </label>
                        <label class="idle-alert-sound-row">
                            <span class="idle-alert-sound-label">波形</span>
                            <select
                                class="idle-alert-sound-select"
                                value={soundWaveform}
                                onChange={(e) => this.setSoundWaveform((e.currentTarget as HTMLSelectElement).value)}
                            >
                                <option value="sine">sine</option>
                                <option value="square">square</option>
                                <option value="triangle">triangle</option>
                                <option value="sawtooth">sawtooth</option>
                            </select>
                        </label>
                        <button
                            class="idle-alert-sound-test"
                            type="button"
                            onClick={() => void this.testBeep()}
                            title="试音"
                        >
                            试音
                        </button>
                    </div>
                )}
            </div>
        );
    }
}

export function createIdleAlert(
    container: HTMLElement,
    timeout: number,
    onActivity: (cb: () => void) => () => void
): void {
    render(
        <IdleAlert timeout={timeout} onActivity={onActivity} />,
        container
    );
}
