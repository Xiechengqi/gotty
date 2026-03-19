export const protocols = ["webtty"];

export const msgInputUnknown = '0';
export const msgInput = '1';
export const msgPing = '2';
export const msgResizeTerminal = '3';
export const msgSetEncoding = '4';
export const msgUploadFile = '7';

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

declare var gotty_resize_debounce_ms: number;


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
    setPreferences(value: object): void;


    /*
     * Sets an input (e.g. user types something) handler
     */
    onInput(callback: (input: string) => void): void;

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
    onOpen(callback: () => void): void;
    onReceive(callback: (data: string) => void): void;
    onClose(callback: () => void): void;
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
    connection: Connection;

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
     * If connection is dropped, reconnect after `reconnect` seconds.
     * -1 means do not reconnect.
     */
    reconnect: number;

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
        this.bufSize = 1024;
        this.resizeDebounceMs =
            (typeof gotty_resize_debounce_ms === "number" && gotty_resize_debounce_ms >= 0)
                ? gotty_resize_debounce_ms
                : 120;
        this.resizeTimer = null;
        this.pendingResize = null;
    };

    open() {
        let connection = this.connectionFactory.create();
        let pingTimer: NodeJS.Timeout;
        let reconnectTimeout: NodeJS.Timeout;
        this.connection = connection;

        const setup = () => {
            connection.onOpen(() => {
                const termInfo = this.term.info();

                this.initializeConnection(this.args, this.authToken);

                this.term.onResize((columns: number, rows: number) => {
                    this.queueResizeTerminal(columns, rows);
                });

                this.sendResizeTerminal(termInfo.columns, termInfo.rows);

                this.sendSetEncoding("base64");

                this.term.onInput(
                    (input: string | Uint8Array) => {
                        this.sendInput(input);
                    }
                );

                // Initialize upload file sender
                if ('setUploadFileSender' in this.term) {
                    (this.term as any).setUploadFileSender((msg: string) => this.sendUploadFile(msg));
                }
                if ('setUploadFileBufferSize' in this.term) {
                    (this.term as any).setUploadFileBufferSize(this.bufSize);
                }

                pingTimer = setInterval(() => {
                    this.sendPing()
                }, 30 * 1000);
            });

            connection.onReceive((data) => {
                const payload = data.slice(1);
                switch (data[0]) {
                    case msgOutput:
                        this.term.output(Uint8Array.from(atob(payload), c => c.charCodeAt(0)));
                        break;
                    case msgPong:
                        break;
                    case msgSetWindowTitle:
                        this.term.setWindowTitle(payload);
                        break;
                    case msgSetPreferences:
                        const preferences = JSON.parse(payload);
                        this.term.setPreferences(preferences);
                        break;
                    case msgSetReconnect:
                        const autoReconnect = JSON.parse(payload);
                        console.log("Enabling reconnect: " + autoReconnect + " seconds")
                        this.reconnect = autoReconnect;
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
                        if (notification.type === 'api_exec_start' && this.term.showAPIIndicator) {
                            this.term.showAPIIndicator(notification.exec_id);
                        } else if (notification.type === 'api_exec_end' && this.term.hideAPIIndicator) {
                            this.term.hideAPIIndicator(notification.exec_id);
                        }
                        break;
                }
            });

            connection.onClose(() => {
                clearInterval(pingTimer);
                this.term.deactivate();
                this.term.showMessage("Connection Closed", 0);
                if (this.reconnect > 0) {
                    reconnectTimeout = setTimeout(() => {
                        connection = this.connectionFactory.create();
                        this.term.reset();
                        setup();
                    }, this.reconnect * 1000);
                }
            });

            connection.open();
        }

        setup();
        return () => {
            clearTimeout(reconnectTimeout);
            if (this.resizeTimer !== null) {
                clearTimeout(this.resizeTimer);
                this.resizeTimer = null;
            }
            this.pendingResize = null;
            connection.close();
        }
    };

    private initializeConnection(args, authToken) {
        this.connection.send(JSON.stringify(
            {
                Arguments: args,
                AuthToken: authToken,
            }
        ));
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
            this.connection.send(msgInput + btoa(inputChunk));
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
        this.connection.send(msg);
    }

    private sendPing(): void {
        this.connection.send(msgPing);
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
        this.connection.send(
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
        this.connection.send(msgSetEncoding + encoding)
    }

};
