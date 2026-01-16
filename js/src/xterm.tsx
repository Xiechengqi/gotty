import { IDisposable, Terminal } from "xterm";
import { FitAddon } from 'xterm-addon-fit';
import { WebLinksAddon } from 'xterm-addon-web-links';
import { WebglAddon } from 'xterm-addon-webgl';
import { ZModemAddon } from "./zmodem";

// 消息类型常量
const MSG_UPLOAD_FILE = '7';

// 文件上传配置
const PREFERRED_CHUNK_SIZE = 8 * 1024; // 8KB per chunk

interface UploadFileMessage {
    name: string;
    size: number;
    chunk: number;
    totalChunks: number;
    data: string; // base64 encoded
}

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
    sendUploadFile?: (msg: string) => void;
    private uploadMaxMessageSize = 1024;

    // 输出/输入回调列表，用于空闲提醒等功能（可解绑）
    private outputCallbacks: Set<() => void> = new Set();
    private inputCallbacks: Set<() => void> = new Set();
    private selectionCallbacks: Set<() => void> = new Set();
    private uploadCallbacks: Set<(fileName: string, progress: number) => void> = new Set();

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
        this.encoder = new TextEncoder();
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

        // Set up drag & drop handler for file upload
        this.setupDragDropHandler();

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

    // Set up drag & drop handler for file upload
    private setupDragDropHandler() {
        const terminalContainer = this.term.element;

        if (!terminalContainer) return;

        // Prevent default drag behaviors
        ['dragenter', 'dragover', 'dragleave', 'drop'].forEach(eventName => {
            terminalContainer.addEventListener(eventName, (e) => {
                e.preventDefault();
                e.stopPropagation();
            }, false);
        });

        // Highlight on drag enter
        terminalContainer.addEventListener('dragenter', () => {
            this.showMessage('Drop file here to upload', 0);
            terminalContainer.classList.add('gotty-drag-over');
        }, false);

        // Remove highlight on drag leave
        terminalContainer.addEventListener('dragleave', (e: DragEvent) => {
            if (!terminalContainer.contains(e.relatedTarget as Node)) {
                this.removeMessage();
                terminalContainer.classList.remove('gotty-drag-over');
            }
        }, false);

        // Handle dropped files
        terminalContainer.addEventListener('drop', async (e: DragEvent) => {
            e.preventDefault();
            terminalContainer.classList.remove('gotty-drag-over');
            this.removeMessage();

            const files = e.dataTransfer?.files;
            if (files && files.length > 0) {
                // Notify input activity
                this.inputCallbacks.forEach((cb) => cb());

                // Upload all dropped files
                for (let i = 0; i < files.length; i++) {
                    const file = files[i];
                    await this.uploadFile(file);

                    // If multiple files, show separator
                    if (i < files.length - 1) {
                        await new Promise(resolve => setTimeout(resolve, 100));
                    }
                }
            }
        }, false);
    }

    // Upload file to server
    private async uploadFile(file: File): Promise<void> {
        const fileName = file.name;
        const fileSize = file.size;
        if (fileSize === 0) {
            this.showMessage(`${fileName} is empty`, 3000);
            return;
        }

        const chunkSize = this.getUploadChunkSize(fileName, fileSize);
        if (chunkSize <= 0) {
            this.showMessage(`Failed to upload ${fileName}: chunk too large`, 3000);
            return;
        }

        const totalChunks = Math.ceil(fileSize / chunkSize);

        // Notify upload start
        this.uploadCallbacks.forEach((cb) => cb(fileName, 0));
        this.showMessage(`Uploading ${fileName}...`, 0);

        try {
            const arrayBuffer = await file.arrayBuffer();
            const uint8Array = new Uint8Array(arrayBuffer);

            for (let chunk = 0; chunk < totalChunks; chunk++) {
                const start = chunk * chunkSize;
                const end = Math.min(start + chunkSize, fileSize);
                const chunkData = uint8Array.slice(start, end);

                // Convert to base64 efficiently
                const base64Data = btoa(String.fromCharCode.apply(null, chunkData as unknown as number[]));

                // Create upload message
                const uploadMsg: UploadFileMessage = {
                    name: fileName,
                    size: fileSize,
                    chunk: chunk,
                    totalChunks: totalChunks,
                    data: base64Data
                };

                // Send to server
                if (this.sendUploadFile) {
                    this.sendUploadFile(MSG_UPLOAD_FILE + JSON.stringify(uploadMsg));
                }

                // Calculate and notify progress
                const progress = Math.round(((chunk + 1) / totalChunks) * 100);
                this.uploadCallbacks.forEach((cb) => cb(fileName, progress));
            }

            this.showMessage(`${fileName} uploaded successfully`, 3000);
        } catch (error) {
            console.error('Upload failed:', error);
            this.showMessage(`Failed to upload ${fileName}`, 3000);
        }
    }

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

    // Set the upload file sender (called from WebTTY)
    setUploadFileSender(sender: (msg: string) => void) {
        this.sendUploadFile = sender;
    }

    setUploadFileBufferSize(size: number) {
        if (Number.isFinite(size) && size > 0) {
            this.uploadMaxMessageSize = size;
        }
    }

    private getUploadChunkSize(fileName: string, fileSize: number): number {
        const maxPayloadBytes = Math.max(0, this.uploadMaxMessageSize - 1);
        const maxValue = Math.max(1, fileSize);
        const overheadMessage: UploadFileMessage = {
            name: fileName,
            size: fileSize,
            chunk: maxValue - 1,
            totalChunks: maxValue,
            data: ""
        };
        const overheadBytes = this.encoder.encode(JSON.stringify(overheadMessage)).length;
        const available = maxPayloadBytes - overheadBytes;
        if (available < 4) {
            return 0;
        }
        const maxRaw = Math.floor(available / 4) * 3;
        return Math.min(PREFERRED_CHUNK_SIZE, maxRaw);
    }

    // Register upload callback (returns unsubscribe function)
    onUpload(callback: (fileName: string, progress: number) => void): () => void {
        this.uploadCallbacks.add(callback);
        return () => {
            this.uploadCallbacks.delete(callback);
        };
    }

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
}

export { GoTTYXterm as OurXterm };
