import { IDisposable, Terminal } from "xterm";
import { FitAddon } from 'xterm-addon-fit';
import { WebLinksAddon } from 'xterm-addon-web-links';
import { WebglAddon } from 'xterm-addon-webgl';
import { ZModemAddon } from "./zmodem";

export class GoTTYXterm {
    // The HTMLElement that contains our terminal
    elem: HTMLElement;

    // The xtermjs.XTerm
    term: Terminal;

    resizeListener: () => void;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: NodeJS.Timeout;

    onResizeHandler: IDisposable;
    onDataHandler: IDisposable;

    fitAddOn: FitAddon;
    zmodemAddon: ZModemAddon;
    toServer: (data: string | Uint8Array) => void;
    encoder: TextEncoder

    // 输出/输入回调列表，用于空闲提醒等功能（可解绑）
    private outputCallbacks: Set<() => void> = new Set();
    private inputCallbacks: Set<() => void> = new Set();
    private selectionCallbacks: Set<() => void> = new Set();

    constructor(elem: HTMLElement) {
        this.elem = elem;
        this.term = new Terminal({
            theme: {
                background: 'rgb(40, 41, 53)'
            }
        });
        this.fitAddOn = new FitAddon();
        this.zmodemAddon = new ZModemAddon({
            toTerminal: (x: Uint8Array) => this.term.write(x),
            toServer: (x: Uint8Array) => this.sendInput(x)
        });
        this.term.loadAddon(new WebLinksAddon());
        this.term.loadAddon(this.fitAddOn);
        this.term.loadAddon(this.zmodemAddon);

        this.message = elem.ownerDocument.createElement("div");
        this.message.className = "xterm-overlay";
        this.messageTimeout = 2000;

        // Auto-copy selection to clipboard
        this.term.onSelectionChange(() => {
            this.selectionCallbacks.forEach((cb) => cb());
            if (this.term.hasSelection()) {
                const text = this.term.getSelection();

                // Try modern Clipboard API first
                if (navigator.clipboard && navigator.clipboard.writeText) {
                    navigator.clipboard.writeText(text).then(() => {
                        // Keep focus on terminal after copying
                        this.term.focus();
                    }).catch(err => {
                        console.warn('Clipboard API failed, trying fallback:', err);
                        this.fallbackCopyToClipboard(text);
                    });
                } else {
                    // Fallback for non-secure contexts
                    this.fallbackCopyToClipboard(text);
                }
            }
        });

        this.resizeListener = () => {
            this.fitAddOn.fit();
            this.term.scrollToBottom();
            this.showMessage(String(this.term.cols) + "x" + String(this.term.rows), this.messageTimeout);
        };

        this.term.open(elem);
        this.term.focus();
        this.resizeListener();

        window.addEventListener("resize", () => { this.resizeListener(); });
    };

    info(): { columns: number, rows: number } {
        return { columns: this.term.cols, rows: this.term.rows };
    };

    // This gets called from the Websocket's onReceive handler
    output(data: Uint8Array) {
        this.zmodemAddon.consume(data);
        // 通知所有输出回调
        this.outputCallbacks.forEach((cb) => cb());
    };

    // 注册输出回调（返回解绑函数）
    onOutput(callback: () => void): () => void {
        this.outputCallbacks.add(callback);
        return () => {
            this.outputCallbacks.delete(callback);
        };
    }

    // 注册输入回调（返回解绑函数）
    onInputActivity(callback: () => void): () => void {
        this.inputCallbacks.add(callback);
        return () => {
            this.inputCallbacks.delete(callback);
        };
    }

    // 注册选择回调（返回解绑函数）
    onSelectionActivity(callback: () => void): () => void {
        this.selectionCallbacks.add(callback);
        return () => {
            this.selectionCallbacks.delete(callback);
        };
    }

    getMessage(): HTMLElement {
        return this.message;
    }

    showMessage(message: string, timeout: number) {
        this.message.innerHTML = message;
        this.showMessageElem(timeout);
    }

    showMessageElem(timeout: number) {
        this.elem.appendChild(this.message);

        if (this.messageTimer) {
            clearTimeout(this.messageTimer);
        }
        if (timeout > 0) {
            this.messageTimer = setTimeout(() => {
                try {
                    this.elem.removeChild(this.message);
                } catch (error) {
                    console.error(error);
                }
            }, timeout);
        }
    };

    removeMessage(): void {
        if (this.message.parentNode == this.elem) {
            this.elem.removeChild(this.message);
        }
    }

    setWindowTitle(title: string) {
        document.title = title;
    };

    setPreferences(value: object) {
        Object.keys(value).forEach((key) => {
            if (key == "EnableWebGL" && key) {
                this.term.loadAddon(new WebglAddon());
            } else if (key == "font-size") {
                this.term.options.fontSize = value[key]
            } else if (key == "font-family") {
                this.term.options.fontFamily = value[key]
            }
        });
    };

    sendInput(data: Uint8Array) {
        return this.toServer(data)
    }

    onInput(callback: (input: string) => void) {
        this.encoder = new TextEncoder()
        this.toServer = callback;

        // I *think* we're ok like this, but if not, we can dispose
        // of the previous handler and put the new one in place.
        if (this.onDataHandler !== undefined) {
            return
        }

        this.onDataHandler = this.term.onData((input) => {
            this.inputCallbacks.forEach((cb) => cb());
            this.toServer(this.encoder.encode(input));
        });
    };

    onResize(callback: (colmuns: number, rows: number) => void) {
        this.onResizeHandler = this.term.onResize(() => {
            callback(this.term.cols, this.term.rows);
        });
    };

    deactivate(): void {
        this.onDataHandler.dispose();
        this.onResizeHandler.dispose();
        this.term.blur();
    }

    reset(): void {
        this.removeMessage();
        this.term.clear();
    }

    close(): void {
        window.removeEventListener("resize", this.resizeListener);
        this.term.dispose();
    }

    disableStdin(): void {
        this.term.options.disableStdin = true;
    }

    enableStdin(): void {
        this.term.options.disableStdin = false;
    }

    focus(): void {
        this.term.focus();
    }

    fallbackCopyToClipboard(text: string): void {
        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();

        try {
            document.execCommand('copy');
        } catch (err) {
            console.warn('Fallback copy failed:', err);
        }

        document.body.removeChild(textarea);

        // Restore focus to terminal
        this.term.focus();
    }

    getCursorAnchor(): { left: number; top: number; height: number } | null {
        const anyTerm: any = this.term as any;
        const core = anyTerm?._core;
        const renderService = core?._renderService;
        const dims = renderService?.dimensions;
        const cellWidth = dims?.actualCellWidth;
        const cellHeight = dims?.actualCellHeight;
        if (typeof cellWidth !== 'number' || typeof cellHeight !== 'number') return null;

        const cursorX = this.term.buffer.active.cursorX;
        const cursorY = this.term.buffer.active.cursorY;

        const screen = this.term.element?.querySelector('.xterm-screen') as HTMLElement | null;
        if (!screen) return null;

        const containerRect = this.elem.getBoundingClientRect();
        const screenRect = screen.getBoundingClientRect();

        const left = (screenRect.left - containerRect.left) + (cursorX + 1) * cellWidth;
        const top = (screenRect.top - containerRect.top) + cursorY * cellHeight;

        return { left, top, height: cellHeight };
    }
}

export { GoTTYXterm as OurXterm };
