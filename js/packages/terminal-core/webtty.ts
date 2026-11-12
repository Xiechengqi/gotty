import {InputSender, type PasteReply, type PasteState} from './paste';
export const protocols = ["webtty"];

export const msgInputUnknown = '0';
export const msgInput = '1';
export const msgPing = '2';
export const msgResizeTerminal = '3';
export const msgSetEncoding = '4';
export const msgUploadFile = '7';
export const msgRestart = '9';

export const msgUnknownOutput = '0';
export const msgOutput = '1';
export const msgPong = '2';
export const msgSetWindowTitle = '3';
export const msgSetPreferences = '4';
export const msgSetReconnect = '5';
export const msgSetBufferSize = '6';
export const msgConnectionCount = '7';
export const msgTerminalState = '8';
export const msgAPINotification = '9';
export const msgReplayBegin = 'a';
export const msgReplayEnd = 'b';
export const msgStreamEvent = 'c';

declare var gotty_resize_debounce_ms: number;

interface ReplayBeginPayload {
    epoch: string;
    mode: "resume" | "tail" | "snapshot" | "events";
    columns?: number; rows?: number; sequence?: number; fromSequence?: number; truncated?: boolean;
    fromOffset: number;
}

interface ReplayEndPayload {
    endOffset: number;
    sequence?: number;
}


export interface Terminal {
    /*
     * Get dimensions of the terminal
     */
    info(): { columns: number, rows: number };

    /*
     * Process output from the server side
     */
    output(data: Uint8Array, replay?: boolean): void;
    // Drain the asynchronous terminal parser, including automatic query replies.
    flushOutput?(callback: () => void): void;
    setReplayMode?(active: boolean): void;

    /*
     * Display a message overlay on the terminal
     */
    showMessage(message: string, timeout: number): void;

    // Don't think we need this anymore
    //    getMessage(): HTMLElement;

    /*
     * Remove message shown by shoMessage. You only need to call
     * this if you want to dismiss it sooner than the timeout.
     */
    removeMessage(): void;


    /*
     * Set window title
     */
    setWindowTitle(title: string): void;

    /*
     * Set preferences. TODO: Add typings
     */
    setPreferences(value: Record<string, unknown>): void;


    /*
     * Sets an input (e.g. user types something) handler
     */
    onInput(callback: (input: string | Uint8Array) => void): void;
    setPasteSender?(sender: (text: string, bracketed: boolean) => void): void;
    onPasteProgress?(state: PasteState | undefined, cancel: () => void, dismiss: () => void): void;

    /*
     * Sets a resize handler
     */
    onResize(callback: (colmuns: number, rows: number) => void): void;

    /*
     * Update connection count display
     */
    updateConnectionCount?(count: number): void;
    updateTerminalState?(state: TerminalStatePayload): void;
    showAPIIndicator?(execId: string): void;
    hideAPIIndicator?(execId: string): void;
    focus?(): void;
    scrollToBottom?(): void;

    isVisible?(): boolean;
    onHistoryStatus?(truncated: boolean): void;
    setSizeControlSender?(sender: () => void): void;
    reset(): void;
    deactivate(): void;
    close(): void;
}

export interface TerminalStatePayload {
    activeCols: number;
    activeRows: number;
    policy: string;
    leaderClientId: string;
    reason: string;
    sourceClientId: string;
    connectedClients: number;
    resizeEnabled: boolean;
    sizeControl?: boolean;
    leaderDevice?: string;
    leaderVisible?: boolean;
    controlReason?: string;
    resizeReason?: string;
}

export interface Connection {
    open(): void;
    close(): void;

    /*
     * This takes fucking strings??
     */
    send(s: string): void | boolean;
    bufferedAmount?(): number;

    isOpen(): boolean;
    readyState(): number;
    onOpen(callback: () => void): void;
    onReceive(callback: (data: string) => void): void;
    onClose(callback: (event: CloseEvent) => void): void;
}

export interface ConnectionFactory {
    create(): Connection;
}

export class WebTTY {
    /*
     * A terminal instance that implements the Terminal interface.
     * This made a lot of sense when we had both HTerm and xterm, but
     * now I wonder if the abstraction makes sense. Keeping it for now,
     * though.
     */
    term: Terminal;

    /*
     * ConnectionFactory and connection instance. We pass the factory
     * in instead of just a connection so that we can reconnect.
     */
    connectionFactory: ConnectionFactory;
    connection: Connection | null = null;

    /*
     * Arguments passed in by the user. We forward them to the backend
     * where they are appended to the command line.
     */
    args: string;

    /*
     * An authentication token. The client gets this from `/auth_token.js`.
     */
    authToken: string;

    /*
     * Server-provided reconnect hint, in seconds. The client now reconnects
     * by default even when the hint is absent.
     */
    reconnect: number;
    reconnectBaseMs: number;
    reconnectMaxMs: number;
    reconnectAttempts: number;
    reconnectTimer: ReturnType<typeof setTimeout> | null;
    pingTimer: ReturnType<typeof setInterval> | null;
    probeTimer: ReturnType<typeof setTimeout> | null;
    stopped: boolean;
    lastPongAt: number;
    lastOffset: number;
    epoch: string;
    replayActive: boolean;
    replayMode: "resume" | "tail" | "snapshot" | "events" | "";
    private sequence = 0;
    private receivedSequence = 0;
    private wireQueue: {data: string; sequence?: number; after?: () => void}[] = [];
    private draining = false;
    private queuedBytes = 0;
    private clientKey: string;
    private snapshotRequired = false;
    private historyTruncated = false;
    private initialized = false;
    skipNextOutputOffset: boolean;
    private replayGeneration = 0;
    private inputSender: InputSender;
    private adaptivePresence = false;

    /*
     * The server's buffer size. If a single message exceeds this size, it will
     * be truncated on the server, so we track it here so that we can split messages
     * into chunks small enough that we don't hurt the server's feelings.
     */
    bufSize: number;
    onConnect?: () => void;
    onDisconnect?: () => void;
    resizeDebounceMs: number;
    resizeTimer: ReturnType<typeof setTimeout> | null;
    pendingResize: { columns: number, rows: number } | null;

    constructor(term: Terminal, connectionFactory: ConnectionFactory, args: string, authToken: string, identityScope = location.pathname) {
        this.clientKey = terminalClientKey(identityScope);
        this.term = term;
        this.inputSender = new InputSender({
            send: frame => this.sendIfOpen(frame),
            buffered: () => this.connection?.bufferedAmount?.() ?? 0,
            chunkSize: () => Math.max(4, Math.floor((this.bufSize - 1) / 4) * 3),
            changed: state => this.term.onPasteProgress?.(state, () => this.inputSender.cancel(), () => this.inputSender.dismiss()),
            message: text => this.term.showMessage(text, 6000),
        });
        this.connectionFactory = connectionFactory;
        this.args = args;
        this.authToken = authToken;
        this.reconnect = -1;
        this.reconnectBaseMs = 500;
        this.reconnectMaxMs = 15000;
        this.reconnectAttempts = 0;
        this.reconnectTimer = null;
        this.pingTimer = null;
        this.probeTimer = null;
        this.stopped = false;
        this.lastPongAt = 0;
        this.lastOffset = 0;
        this.epoch = "";
        this.replayActive = false;
        this.replayMode = "";
        this.skipNextOutputOffset = false;
        this.bufSize = 1024;
        this.resizeDebounceMs =
            (typeof gotty_resize_debounce_ms === "number" && gotty_resize_debounce_ms >= 0)
                ? gotty_resize_debounce_ms
                : 120;
        this.resizeTimer = null;
        this.pendingResize = null;
    };

    open() {
        this.stopped = false;
        const onVisibilityChange = () => {
            this.reportVisibility();
            if (!document.hidden) {
                this.probeOrReconnect();
            }
        };
        const onPageShow = () => this.probeOrReconnect();
        const onOnline = () => this.probeOrReconnect();

        document.addEventListener("visibilitychange", onVisibilityChange);
        window.addEventListener("pageshow", onPageShow);
        window.addEventListener("online", onOnline);

        // Only adaptive servers opt in. Legacy leader/on_idle clients keep
        // their event-driven resize behavior. Focus changes are not visibility.
        const presence = setInterval(() => {
            if (this.adaptivePresence && !document.hidden && (this.term.isVisible?.() ?? true)) this.reportVisibility();
        }, 3000);

        this.connectNow(true);

        return () => {
            this.stopped = true;
            this.inputSender.dispose();
            clearInterval(presence);
            this.onDisconnect?.();
            document.removeEventListener("visibilitychange", onVisibilityChange);
            window.removeEventListener("pageshow", onPageShow);
            window.removeEventListener("online", onOnline);
            this.clearReconnectTimer();
            this.clearPingTimer();
            this.clearProbeTimer();
            if (this.resizeTimer !== null) {
                clearTimeout(this.resizeTimer);
                this.resizeTimer = null;
            }
            this.pendingResize = null;
            this.connection?.close();
            this.connection = null;
        }
    };

    private initializeConnection(args: string, authToken: string) {
        const payload: Record<string, string | number> = {
            Arguments: args,
            AuthToken: authToken,
            ClientKey: this.clientKey,
            ReplayVersion: 2,
            LastSequence: this.sequence,
        };
        if (this.epoch) {
            payload.LastOffset = this.lastOffset;
            payload.Epoch = this.epoch;
        }
        this.initialized = this.sendIfOpen(JSON.stringify(payload), true);
    }

    private connectNow(resetBackoff = false): void {
        if (this.stopped || document.hidden) {
            return;
        }
        if (this.connection && this.isConnectingOrOpen(this.connection)) {
            return;
        }
        if (resetBackoff) {
            this.reconnectAttempts = 0;
        }
        this.clearReconnectTimer();
        this.clearProbeTimer();

        let connection: Connection;
        try {
            connection = this.connectionFactory.create();
        } catch (err) {
            console.error("[GoTTY] Failed to create websocket:", err);
            this.scheduleReconnect();
            return;
        }

        this.connection = connection;
        this.initialized = false;
        connection.onOpen(() => {
            if (this.stopped || this.connection !== connection) {
                return;
            }
            this.reconnectAttempts = 0;
            this.enqueueAfterDrain(() => {
                if (this.stopped || this.connection !== connection) return;
                if (this.snapshotRequired) { this.epoch = ''; this.sequence = 0; this.snapshotRequired = false; }
                this.onConnect?.();
                this.term.removeMessage();
                const termInfo = this.term.info();

                this.initializeConnection(this.args, this.authToken);

                this.term.onResize((columns: number, rows: number) => {
                    this.queueResizeTerminal(columns, rows);
                });

                this.sendResizeTerminal(termInfo.columns, termInfo.rows);
                this.sendSetEncoding("base64");

                this.term.onInput((input: string | Uint8Array) => {
                    this.sendInput(input);
                });
                this.term.setPasteSender?.((text, bracketed) => {
                    if (!this.replayActive && !this.stopped && this.initialized) this.inputSender.paste(text, bracketed);
                });

                this.term.setSizeControlSender?.(() => this.takeSizeControl());
                if ('setUploadFileSender' in this.term) {
                    (this.term as any).setUploadFileSender((msg: string) => this.sendUploadFile(msg));
                }
                if ('setUploadFileBufferSize' in this.term) {
                    (this.term as any).setUploadFileBufferSize(this.bufSize);
                }

                this.lastPongAt = Date.now();
                this.clearPingTimer();
                this.pingTimer = setInterval(() => {
                    this.sendPing();
                }, 30 * 1000);
            });
        });

        connection.onReceive((data) => {
            if (this.connection !== connection) {
                return;
            }
            this.receiveMessage(data);
        });

        connection.onClose((event) => {
            if (this.connection !== connection) {
                return;
            }
            this.clearPingTimer();
            this.clearProbeTimer();
            this.connection = null;
            this.initialized = false;
            this.inputSender.disconnect();
            if (this.replayActive && this.replayMode !== "events" && this.replayMode !== "resume") this.snapshotRequired = true;
            this.term.deactivate();
            this.onDisconnect?.();
            if (!this.stopped) {
                const closeMessage = this.describeClose(event);
                if (this.isAuthenticationClose(event)) {
                    this.term.showMessage(`${closeMessage}. Refresh the page or sign in again.`, 0);
                    return;
                }
                this.scheduleReconnect(closeMessage);
            }
        });

        try {
            connection.open();
        } catch (err) {
            console.error("[GoTTY] Failed to open websocket:", err);
            if (this.connection === connection) {
                this.connection = null;
            }
            this.scheduleReconnect();
        }
    }

    reconnectNow(): void {
        if (this.stopped) return;
        this.inputSender.disconnect();
        if (this.replayActive && this.replayMode !== 'events' && this.replayMode !== 'resume') this.snapshotRequired = true;
        const old = this.connection;
        this.connection = null;
        this.initialized = false;
        old?.close();
        this.clearPingTimer(); this.clearProbeTimer(); this.clearReconnectTimer();
        this.term.deactivate(); this.onDisconnect?.();
        this.connectNow(true);
    }

    takeSizeControl(): void {
        const size = this.term.info();
        this.sendResizeTerminal(size.columns, size.rows, true);
    }

    reportVisibility(): void {
        const size = this.term.info();
        this.sendResizeTerminal(size.columns, size.rows);
    }

    private recoverSnapshot(): void {
        this.snapshotRequired = true;
        this.wireQueue = [];
        this.queuedBytes = 0;
        this.reconnectNow();
    }

    private enqueueAfterDrain(after: () => void): void {
        this.wireQueue.push({data: '', after});
        this.drainWireQueue();
    }

    private receiveMessage(data: string): void {
        // Receipts must not wait for the output renderer; a busy renderer must
        // not stall paste flow control or its cancellation response.
        if (data[0] === 'e') {
            try {
                const reply = JSON.parse(data.slice(1)) as PasteReply;
                if (!reply.id && reply.error) this.term.showMessage(reply.error, 6000);
                else this.inputSender.reply(reply);
            } catch { /* Invalid receipts time out, never imply success. */ }
            return;
        }
        let sequence: number | undefined;
        try {
            if (data[0] === msgReplayBegin) {
                const begin = JSON.parse(data.slice(1)) as ReplayBeginPayload;
                this.receivedSequence = begin.mode === 'events' ? (begin.fromSequence ?? this.sequence) : (begin.sequence ?? 0);
            } else if (data[0] === msgStreamEvent) {
                const event = JSON.parse(data.slice(1));
                if (!Number.isSafeInteger(event.sequence) || event.sequence !== this.receivedSequence + 1 || typeof event.data !== 'string' || !['1','8'].includes(event.data[0])) throw Error('Terminal event gap');
                this.receivedSequence = sequence = event.sequence;
                data = event.data;
            }
        } catch {
            this.recoverSnapshot(); return;
        }
        this.queuedBytes += data.length;
        if (this.queuedBytes > 32 * 1024 * 1024) {
            this.recoverSnapshot(); return;
        }
        this.wireQueue.push({data, sequence});
        this.drainWireQueue();
    }

    private drainWireQueue(): void {
        if (this.draining || this.stopped) return;
        while (this.wireQueue.length) {
            const item = this.wireQueue.shift()!;
            this.queuedBytes -= item.data.length;
            if (item.after) { item.after(); continue; }
            if (item.data[0] === msgReplayEnd) {
                this.draining = true;
                this.handleReplayEnd(JSON.parse(item.data.slice(1)), () => {this.draining = false; this.drainWireQueue();});
                return;
            }
            try { this.handleMessage(item.data); } catch { this.recoverSnapshot(); return; }
            if (item.data[0] === msgOutput) {
                // Commit resume positions only after xterm has parsed output.
                // Geometry behind this boundary is applied before later output.
                let sequence = item.sequence;
                let batchBytes = item.data.length;
                // Batch adjacent output, but never cross a resize or replay
                // boundary. A parser drain per network packet is too costly.
                while (this.wireQueue[0]?.data[0] === msgOutput && batchBytes < 256 * 1024) {
                    const next = this.wireQueue.shift()!;
                    this.queuedBytes -= next.data.length;
                    batchBytes += next.data.length;
                    try { this.handleMessage(next.data); } catch { this.recoverSnapshot(); return; }
                    sequence = next.sequence ?? sequence;
                }
                this.draining = true;
                const finish = () => {if (sequence !== undefined) this.sequence = sequence; this.draining = false; this.drainWireQueue();};
                if (this.term.flushOutput) this.term.flushOutput(finish); else finish();
                return;
            }
            if (item.sequence !== undefined) this.sequence = item.sequence;
        }
    }

    private handleMessage(data: string): void {
        const payload = data.slice(1);
        switch (data[0]) {
            case msgOutput:
                this.handleOutput(payload);
                break;
            case msgPong:
                this.lastPongAt = Date.now();
                break;
            case msgSetWindowTitle:
                this.term.setWindowTitle(payload);
                break;
            case msgSetPreferences:
                const preferences = JSON.parse(payload);
                this.inputSender.acknowledged = preferences['paste-protocol'] === 1;
                this.term.setPreferences(preferences);
                if (typeof preferences.reconnect === "number") {
                    this.applyReconnectHint(preferences.reconnect);
                }
                break;
            case msgSetReconnect:
                const autoReconnect = JSON.parse(payload);
                this.applyReconnectHint(autoReconnect);
                break;
            case msgSetBufferSize:
                const bufSize = JSON.parse(payload);
                this.bufSize = bufSize;
                if ('setUploadFileBufferSize' in this.term) {
                    (this.term as any).setUploadFileBufferSize(this.bufSize);
                }
                break;
            case msgConnectionCount:
                if (this.term.updateConnectionCount) {
                    const count = parseInt(payload);
                    this.term.updateConnectionCount(count);
                }
                break;
            case msgTerminalState:
                const state = JSON.parse(payload) as TerminalStatePayload;
                this.adaptivePresence = state.policy === 'adaptive';
                if (this.term.updateTerminalState) {
                    this.term.updateTerminalState(state);
                }
                break;
            case msgAPINotification:
                const notification = JSON.parse(payload);
                console.log("[GoTTY] API notification received:", notification);
                if (notification.type === 'api_exec_start' && this.term.showAPIIndicator) {
                    console.log("[GoTTY] Showing API indicator for:", notification.exec_id);
                    this.term.showAPIIndicator(notification.exec_id);
                } else if (notification.type === 'api_exec_end' && this.term.hideAPIIndicator) {
                    console.log("[GoTTY] Hiding API indicator for:", notification.exec_id);
                    this.term.hideAPIIndicator(notification.exec_id);
                }
                break;
            case msgReplayBegin:
                this.handleReplayBegin(JSON.parse(payload) as ReplayBeginPayload);
                break;
            case msgReplayEnd:
                this.handleReplayEnd(JSON.parse(payload) as ReplayEndPayload);
                break;
        }
    }

    private handleOutput(payload: string): void {
        const decoded = Uint8Array.from(atob(payload), c => c.charCodeAt(0));
        this.term.output(decoded, this.replayMode !== "");
        if (this.epoch || this.replayActive) {
            if (this.skipNextOutputOffset) {
                this.skipNextOutputOffset = false;
                if (decoded.length === 2 && decoded[0] === 0x1b && decoded[1] === 0x63) {
                    return;
                }
            }
            this.lastOffset += decoded.length;
        }
    }

    private handleReplayBegin(payload: ReplayBeginPayload): void {
        this.replayGeneration++;
        this.epoch = payload.epoch || "";
        this.lastOffset = Number.isFinite(payload.fromOffset) ? payload.fromOffset : 0;
        this.replayMode = ['resume','snapshot','events'].includes(payload.mode) ? payload.mode : 'tail';
        if (payload.mode === 'snapshot') {
            this.sequence = 0;
            this.historyTruncated = payload.truncated === true;
        }
        if (payload.mode === 'snapshot' && payload.columns && payload.rows) {
            this.term.updateTerminalState?.({activeCols: payload.columns, activeRows: payload.rows,
                policy: '', leaderClientId: '', reason: 'snapshot', sourceClientId: '', connectedClients: 0, resizeEnabled: true});
        }
        this.replayActive = true;
        this.term.setReplayMode?.(true);

        if (this.replayMode === "tail" || this.replayMode === "snapshot") {
            this.term.reset();
            this.skipNextOutputOffset = this.replayMode === "tail";
            this.term.showMessage("Restoring session...", 0);
        } else {
            this.skipNextOutputOffset = false;
        }
    }

    private handleReplayEnd(payload: ReplayEndPayload, after?: () => void): void {
        if (Number.isFinite(payload.endOffset)) {
            this.lastOffset = payload.endOffset;
        }
        const wasSnapshot = this.replayMode === 'snapshot';
        const wasTail = this.replayMode === "tail" || wasSnapshot;
        const generation = this.replayGeneration;
        this.skipNextOutputOffset = false;
        // The replay-end frame arrives before xterm has parsed the replay.
        // Release suppression at the parser boundary, before queued live output.
        const finish = () => {
            if (this.stopped || generation !== this.replayGeneration) return;
            if (Number.isSafeInteger(payload.sequence)) this.sequence = payload.sequence!;
            this.replayActive = false;
            this.replayMode = "";
            this.skipNextOutputOffset = false;
            this.term.setReplayMode?.(false);
            if (wasTail) {
                this.term.removeMessage();
                this.term.scrollToBottom?.();
                this.term.focus?.();
            }
            if (wasSnapshot) this.term.onHistoryStatus?.(this.historyTruncated);
            after?.();
        };
        if (this.term.flushOutput) this.term.flushOutput(finish);
        else finish();
    }

    private applyReconnectHint(seconds: number): void {
        if (Number.isFinite(seconds) && seconds > 0) {
            this.reconnect = seconds;
            this.reconnectBaseMs = Math.max(500, seconds * 1000);
        }
    }

    private probeOrReconnect(): void {
        if (this.stopped || document.hidden) {
            return;
        }
        const connection = this.connection;
        if (!connection || this.isDead(connection)) {
            this.connectNow(true);
            return;
        }
        if (connection.readyState() === WebSocket.OPEN) {
            const sentAt = Date.now();
            if (!this.sendPing()) {
                connection.close();
                return;
            }
            this.clearProbeTimer();
            this.probeTimer = setTimeout(() => {
                if (this.connection === connection && connection.readyState() === WebSocket.OPEN && this.lastPongAt < sentAt) {
                    connection.close();
                }
            }, 3000);
        }
    }

    private scheduleReconnect(reason = ""): void {
        if (this.stopped || document.hidden) {
            return;
        }
        this.clearReconnectTimer();
        const delay = this.nextReconnectDelay();
        const seconds = Math.max(1, Math.round(delay / 1000));
        const message = reason
            ? `${reason}. Reconnecting in ${seconds}s...`
            : `Reconnecting in ${seconds}s...`;
        this.term.showMessage(message, 0);
        this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null;
            this.connectNow(false);
        }, delay);
    }

    private describeClose(event: CloseEvent): string {
        const code = event.code || "no code";
        const reason = event.reason ? `: ${event.reason}` : "";
        return `WebSocket closed (${code})${reason}`;
    }

    private isAuthenticationClose(event: CloseEvent): boolean {
        return event.code === 1008 && /auth/i.test(event.reason);
    }

    private nextReconnectDelay(): number {
        const exponent = Math.min(this.reconnectAttempts, 5);
        const raw = Math.min(this.reconnectMaxMs, this.reconnectBaseMs * Math.pow(2, exponent));
        this.reconnectAttempts++;
        return Math.round(raw * (0.8 + Math.random() * 0.4));
    }

    private clearReconnectTimer(): void {
        if (this.reconnectTimer !== null) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
        }
    }

    private clearPingTimer(): void {
        if (this.pingTimer !== null) {
            clearInterval(this.pingTimer);
            this.pingTimer = null;
        }
    }

    private clearProbeTimer(): void {
        if (this.probeTimer !== null) {
            clearTimeout(this.probeTimer);
            this.probeTimer = null;
        }
    }

    private isConnectingOrOpen(connection: Connection): boolean {
        const state = connection.readyState();
        return state === WebSocket.CONNECTING || state === WebSocket.OPEN;
    }

    private isDead(connection: Connection): boolean {
        const state = connection.readyState();
        return state === WebSocket.CLOSING || state === WebSocket.CLOSED;
    }

    private sendIfOpen(data: string, initialization = false): boolean {
        if ((!initialization && !this.initialized) || !this.connection || this.connection.readyState() !== WebSocket.OPEN) {
            return false;
        }
        try {
            return this.connection.send(data) !== false;
        } catch (err) {
            console.warn("[GoTTY] websocket send failed:", err);
            return false;
        }
    }

    /*
     * sendInput sends data to the server. It accepts strings or Uint8Arrays.
     * strings will be encoded as UTF-8. Uint8Arrays are passed along as-is.
     */
    private sendInput(input: string | Uint8Array) {
        // Historical queries must never send replies into the current PTY.
        if (this.replayActive || this.stopped) return;
        this.inputSender.input(input);
    }

    private sendUploadFile(msg: string) {
        const encoder = new TextEncoder();
        const msgBytes = encoder.encode(msg).length;
        console.log(`[Upload] Message size: ${msgBytes} bytes, Buffer size: ${this.bufSize} bytes`);
        if (msgBytes > this.bufSize) {
            console.warn("[Upload] Message exceeds server buffer size, aborting send.");
            return;
        }
        console.log("[Upload] Sending message to server");
        this.sendIfOpen(msg);
    }

    private sendPing(): boolean {
        return this.sendIfOpen(msgPing);
    }

    private queueResizeTerminal(columns: number, rows: number) {
        this.pendingResize = { columns, rows };
        if (this.resizeTimer !== null) {
            clearTimeout(this.resizeTimer);
        }

        this.resizeTimer = setTimeout(() => {
            if (!this.pendingResize) {
                return;
            }
            const next = this.pendingResize;
            this.pendingResize = null;
            this.sendResizeTerminal(next.columns, next.rows);
        }, this.resizeDebounceMs);
    }

    private getDeviceClass(viewportWidth: number, inputClass: string): string {
        if (inputClass === 'fine') return 'desktop';
        if (inputClass === 'coarse') return Math.min(viewportWidth, window.innerHeight) < 640 ? 'mobile' : 'tablet';
        if (viewportWidth < 640) return "mobile";
        if (viewportWidth < 1024) return "tablet";
        return "desktop";
    }

    private sendResizeTerminal(colmuns: number, rows: number, takeControl = false) {
        const viewportWidth = window.innerWidth || document.documentElement.clientWidth || 0;
        const viewportHeight = window.innerHeight || document.documentElement.clientHeight || 0;
        const inputClass = window.matchMedia?.('(any-pointer: fine) and (any-hover: hover)').matches ? 'fine'
            : window.matchMedia?.('(pointer: coarse)').matches ? 'coarse' : 'unknown';
        this.sendIfOpen(
            msgResizeTerminal + JSON.stringify(
                {
                    columns: colmuns,
                    visible: !document.hidden && (this.term.isVisible?.() ?? true),
                    takeControl,
                    rows: rows,
                    deviceClass: this.getDeviceClass(viewportWidth, inputClass),
                    inputClass,
                    presence: true,
                    pixelRatio: window.devicePixelRatio || 1,
                    viewportWidth: viewportWidth,
                    viewportHeight: viewportHeight
                }
            )
        );
    }

    private sendSetEncoding(encoding: "base64" | "null") {
        this.sendIfOpen(msgSetEncoding + encoding)
    }

};

// Store identity only, never terminal text or credentials. Storage denial keeps
// a per-object identity and still permits normal reconnects.
function terminalClientKey(scope: string): string {
    const key = 'gotty:terminal-client:' + scope;
    try { const saved = sessionStorage.getItem(key); if (saved && /^[a-f0-9]{32}$/.test(saved)) return saved; } catch { /* Storage is optional. */ }
    const value = Array.from(crypto.getRandomValues(new Uint32Array(4)), n => n.toString(16).padStart(8,'0')).join('');
    try { sessionStorage.setItem(key,value); } catch { /* Use the in-memory identity. */ }
    return value;
}
