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

declare var gotty_resize_debounce_ms: number;

interface ReplayBeginPayload {
    epoch: string;
    mode: "resume" | "tail";
    fromOffset: number;
}

interface ReplayEndPayload {
    endOffset: number;
}


export interface Terminal {
    /*
     * Get dimensions of the terminal
     */
    info(): { columns: number, rows: number };

    /*
     * Process output from the server side
     */
    output(data: Uint8Array): void;

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
}

export interface Connection {
    open(): void;
    close(): void;

    /*
     * This takes fucking strings??
     */
    send(s: string): void;

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
    replayMode: "resume" | "tail" | "";
    skipNextOutputOffset: boolean;

    /*
     * The server's buffer size. If a single message exceeds this size, it will
     * be truncated on the server, so we track it here so that we can split messages
     * into chunks small enough that we don't hurt the server's feelings.
     */
    bufSize: number;
    resizeDebounceMs: number;
    resizeTimer: ReturnType<typeof setTimeout> | null;
    pendingResize: { columns: number, rows: number } | null;

    constructor(term: Terminal, connectionFactory: ConnectionFactory, args: string, authToken: string) {
        this.term = term;
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
            if (!document.hidden) {
                this.probeOrReconnect();
            }
        };
        const onPageShow = () => this.probeOrReconnect();
        const onOnline = () => this.probeOrReconnect();

        document.addEventListener("visibilitychange", onVisibilityChange);
        window.addEventListener("pageshow", onPageShow);
        window.addEventListener("online", onOnline);

        this.connectNow(true);

        return () => {
            this.stopped = true;
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
        };
        if (this.epoch) {
            payload.LastOffset = this.lastOffset;
            payload.Epoch = this.epoch;
        }
        this.sendIfOpen(JSON.stringify(payload));
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
        connection.onOpen(() => {
            if (this.stopped || this.connection !== connection) {
                return;
            }
            this.reconnectAttempts = 0;
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

        connection.onReceive((data) => {
            if (this.connection !== connection) {
                return;
            }
            this.handleMessage(data);
        });

        connection.onClose(() => {
            if (this.connection !== connection) {
                return;
            }
            this.clearPingTimer();
            this.clearProbeTimer();
            this.connection = null;
            this.term.deactivate();
            if (!this.stopped) {
                this.term.showMessage("Reconnecting...", 0);
                this.scheduleReconnect();
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
        this.term.output(decoded);
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
        this.epoch = payload.epoch || "";
        this.lastOffset = Number.isFinite(payload.fromOffset) ? payload.fromOffset : 0;
        this.replayMode = payload.mode === "resume" ? "resume" : "tail";
        this.replayActive = true;

        if (this.replayMode === "tail") {
            this.term.reset();
            this.skipNextOutputOffset = true;
            this.term.showMessage("Restoring session...", 0);
        } else {
            this.skipNextOutputOffset = false;
        }
    }

    private handleReplayEnd(payload: ReplayEndPayload): void {
        if (Number.isFinite(payload.endOffset)) {
            this.lastOffset = payload.endOffset;
        }
        const wasTail = this.replayMode === "tail";
        this.replayActive = false;
        this.replayMode = "";
        this.skipNextOutputOffset = false;
        if (wasTail) {
            this.term.removeMessage();
            this.term.scrollToBottom?.();
            this.term.focus?.();
        }
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

    private scheduleReconnect(): void {
        if (this.stopped || document.hidden) {
            return;
        }
        this.clearReconnectTimer();
        const delay = this.nextReconnectDelay();
        const seconds = Math.max(1, Math.round(delay / 1000));
        this.term.showMessage(`Reconnecting in ${seconds}s...`, 0);
        this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null;
            this.connectNow(false);
        }, delay);
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

    private sendIfOpen(data: string): boolean {
        if (!this.connection || this.connection.readyState() !== WebSocket.OPEN) {
            return false;
        }
        try {
            this.connection.send(data);
            return true;
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
        let effectiveBufferSize = this.bufSize - 1;
        let dataString: string;

        if (typeof input === "string") {
            dataString = input;
        } else {
            dataString = String.fromCharCode(...input)
        }

        // Account for base64 encoding
        let maxChunkSize = Math.floor(effectiveBufferSize / 4) * 3;

        for (let i = 0; i < Math.ceil(dataString.length / maxChunkSize); i++) {
            let inputChunk = dataString.substring(i * maxChunkSize, Math.min((i + 1) * maxChunkSize, dataString.length))
            this.sendIfOpen(msgInput + btoa(inputChunk));
        }
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

    private getDeviceClass(viewportWidth: number): string {
        if (viewportWidth < 640) return "mobile";
        if (viewportWidth < 1024) return "tablet";
        return "desktop";
    }

    private sendResizeTerminal(colmuns: number, rows: number) {
        const viewportWidth = window.innerWidth || document.documentElement.clientWidth || 0;
        const viewportHeight = window.innerHeight || document.documentElement.clientHeight || 0;
        this.sendIfOpen(
            msgResizeTerminal + JSON.stringify(
                {
                    columns: colmuns,
                    rows: rows,
                    deviceClass: this.getDeviceClass(viewportWidth),
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
