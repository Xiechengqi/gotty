import { IDisposable, Terminal } from "xterm";
import { FitAddon } from 'xterm-addon-fit';
import { WebLinksAddon } from 'xterm-addon-web-links';
import { WebglAddon } from 'xterm-addon-webgl';
import { ZModemAddon } from "./zmodem";
import { TerminalStatePayload } from "./webtty";
import { render, createRef, Component } from 'preact';
import { Modal } from 'bootstrap';

declare var gotty_show_terminal_state: boolean;

// 消息类型常量
const MSG_UPLOAD_FILE = '7';
const MSG_UPLOAD_CANCEL = '8';

// 文件上传配置
const PREFERRED_CHUNK_SIZE = 8 * 1024; // 8KB per chunk

interface UploadFileMessage {
    name: string;
    size: number;
    chunk: number;
    totalChunks: number;
    data: string; // base64 encoded
}

// 上传任务状态
interface UploadTask {
    id: string;
    name: string;
    size: number;
    progress: number;
    speed: number; // bytes per second
    status: 'uploading' | 'completed' | 'cancelled' | 'error';
    startTime: number;
}

// ==================== Drop Overlay 组件 ====================

class DropOverlay {
    private elem: HTMLElement;

    constructor() {
        this.elem = document.createElement('div');
        this.elem.className = 'gotty-drop-overlay';
        this.elem.innerHTML = `
            <div class="gotty-drop-content">
                <div class="drop-icon">📁</div>
                <div class="drop-text">拖放文件到此处上传</div>
                <div class="file-list"></div>
                <div class="drop-hint">释放鼠标以上传</div>
            </div>
        `;
        document.body.appendChild(this.elem);
    }

    show(files: FileList | null) {
        const fileList = this.elem.querySelector('.file-list') as HTMLElement;
        if (files && files.length > 0) {
            fileList.innerHTML = Array.from(files)
                .map(f => `<div class="file-item"><span class="file-name">${this.escapeHtml(f.name)}</span> (${this.formatSize(f.size)})</div>`)
                .join('');
        } else {
            fileList.innerHTML = '';
        }
        this.elem.classList.add('active');
    }

    hide() {
        this.elem.classList.remove('active');
    }

    private formatSize(bytes: number): string {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    }

    private escapeHtml(text: string): string {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    destroy() {
        document.body.removeChild(this.elem);
    }
}

// ==================== Upload Progress Modal 组件 ====================

interface UploadProgressModalProps {
    task: UploadTask;
    onCancel: () => void;
    onDismiss: () => void;
}

interface UploadProgressModalState {
    show: boolean;
    progress: number;
    speed: string;
    remaining: string;
    transferred: string;
    status: string;
}

class UploadProgressModal extends Component<UploadProgressModalProps, UploadProgressModalState> {
    private modalRef = createRef<HTMLDivElement>();
    private modalInstance: Modal | null = null;
    private updateTimer: NodeJS.Timeout | null = null;
    private hideListener: (() => void) | null = null;

    constructor(props: UploadProgressModalProps) {
        super(props);
        this.state = {
            show: true,
            progress: 0,
            speed: '0 B/s',
            remaining: '--',
            transferred: '0 B',
            status: 'uploading'
        };
    }

    componentDidMount() {
        this.modalInstance = Modal.getOrCreateInstance(this.modalRef.current!);
        this.modalInstance.show();

        // 监听关闭事件
        this.hideListener = () => this.props.onDismiss();
        this.modalRef.current?.addEventListener('hidden.bs.modal', this.hideListener);

        // 定时更新状态
        this.updateTimer = setInterval(() => this.updateState(), 200);
    }

    componentWillUnmount() {
        if (this.updateTimer) {
            clearInterval(this.updateTimer);
        }
        if (this.hideListener && this.modalRef.current) {
            this.modalRef.current.removeEventListener('hidden.bs.modal', this.hideListener);
        }
        this.modalInstance?.dispose();
    }

    static getDerivedStateFromProps(props: UploadProgressModalProps, state: UploadProgressModalState) {
        const newState = { ...state };

        if (props.task.status === 'completed') {
            newState.progress = 100;
            newState.speed = '完成';
            newState.remaining = '';
            newState.status = 'completed';
        } else if (props.task.status === 'cancelled') {
            newState.status = 'cancelled';
        } else if (props.task.status === 'error') {
            newState.status = 'error';
        } else {
            newState.progress = props.task.progress;
            newState.speed = UploadProgressModal.formatSpeed(props.task.speed);

            const elapsed = Date.now() - props.task.startTime;
            if (elapsed > 1000 && props.task.progress > 0) {
                const totalTime = elapsed / (props.task.progress / 100);
                const remaining = Math.ceil((totalTime - elapsed) / 1000);
                newState.remaining = remaining > 60
                    ? `${Math.ceil(remaining / 60)} 分`
                    : `${remaining} 秒`;
            } else {
                newState.remaining = '计算中...';
            }

            newState.transferred = UploadProgressModal.formatSize(
                Math.floor((props.task.size * props.task.progress) / 100)
            );
        }

        return newState;
    }

    private updateState() {
        // 状态已在 getDerivedStateFromProps 中更新
        this.forceUpdate();
    }

    private handleCancel = () => {
        this.props.onCancel();
        // 不直接隐藏，让状态变化触发完成界面显示
    };

    private handleDismiss = () => {
        this.modalInstance?.hide();
        this.props.onDismiss();
    };

    render() {
        const { task } = this.props;
        const { progress, speed, remaining, transferred, status } = this.state;

        const progressBarClass = status === 'error' ? 'bg-danger' :
            status === 'cancelled' ? 'bg-warning' : 'bg-primary';
        const progressText = status === 'completed' ? '上传完成' :
            status === 'cancelled' ? '已取消' :
                status === 'error' ? '上传失败' : '';

        return (
            <div class="modal fade gotty-upload-modal" ref={this.modalRef} tabIndex={-1}>
                <div class="modal-dialog modal-dialog-centered">
                    <div class="modal-content">
                        <div class="modal-header">
                            <h5 class="modal-title">上传文件</h5>
                            {status === 'uploading' && (
                                <button type="button" class="btn-close" onClick={this.handleDismiss}></button>
                            )}
                        </div>
                        <div class="modal-body">
                            {status === 'uploading' ? (
                                <div class="upload-progress">
                                    <div class="mb-2">
                                        <strong>{task.name}</strong>
                                        <span class="text-muted ms-2">({UploadProgressModal.formatSize(task.size)})</span>
                                    </div>

                                    <div class="progress">
                                        <div
                                            class={`progress-bar ${progressBarClass}`}
                                            role="progressbar"
                                            style={{ width: `${progress}%` }}
                                            aria-valuenow={progress}
                                            aria-valuemin={0}
                                            aria-valuemax={100}
                                        >
                                            {progress}%
                                        </div>
                                    </div>

                                    <div class="upload-stats">
                                        <span class="upload-speed">{speed}</span>
                                        <span class="upload-remaining">剩余 {remaining}</span>
                                    </div>

                                    <div class="text-center mt-2 text-muted" style={{ fontSize: '12px' }}>
                                        已传输: {transferred}
                                    </div>
                                </div>
                            ) : (
                                <div class="upload-complete">
                                    {status === 'completed' && <div class="success-icon">✓</div>}
                                    {status === 'cancelled' && <div class="text-warning" style={{ fontSize: '48px' }}>✕</div>}
                                    {status === 'error' && <div class="text-danger" style={{ fontSize: '48px' }}>✕</div>}
                                    <div class="upload-filename">{task.name}</div>
                                    <div class="text-muted mt-2">{progressText}</div>
                                </div>
                            )}
                        </div>
                        <div class="modal-footer">
                            {status === 'uploading' ? (
                                <button class="btn btn-outline-secondary" onClick={this.handleCancel}>
                                    取消上传
                                </button>
                            ) : (
                                <button class="btn btn-primary" onClick={this.handleDismiss}>
                                    确定
                                </button>
                            )}
                        </div>
                    </div>
                </div>
            </div>
        );
    }

    private static formatSize(bytes: number): string {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
    }

    private static formatSpeed(bytesPerSecond: number): string {
        if (bytesPerSecond < 1024) return bytesPerSecond + ' B/s';
        if (bytesPerSecond < 1024 * 1024) return (bytesPerSecond / 1024).toFixed(1) + ' KB/s';
        return (bytesPerSecond / (1024 * 1024)).toFixed(2) + ' MB/s';
    }
}

// ==================== 主类 ====================

export class GoTTYXterm {
    // The HTMLElement that contains our terminal
    elem: HTMLElement;

    // The xtermjs.XTerm
    term: Terminal;

    resizeListener: () => void;
    viewportResizeListener: (() => void) | null = null;
    private fitRequestId: number | null = null;

    message: HTMLElement;
    messageTimeout: number;
    messageTimer: NodeJS.Timeout;

    connectionCountElem: HTMLElement;
    clearHistoryBtn: HTMLElement;
    terminalStateElem: HTMLElement | null = null;
    private showTerminalStateOverlay: boolean;

    onResizeHandler: IDisposable;
    onDataHandler: IDisposable;

    fitAddOn: FitAddon;
    zmodemAddon: ZModemAddon;
    toServer: (data: string | Uint8Array) => void;
    encoder: TextEncoder
    sendUploadFile?: (msg: string) => void;
    private uploadMaxMessageSize = 1024;

    // Drop overlay and upload modal
    private dropOverlay: DropOverlay | null = null;
    private uploadModalContainer: HTMLElement | null = null;
    private currentUploadTask: UploadTask | null = null;
    private uploadStartTime: number = 0;

    // 输出/输入回调列表，用于空闲提醒等功能（可解绑）
    private outputCallbacks: Set<() => void> = new Set();
    private inputCallbacks: Set<() => void> = new Set();
    private selectionCallbacks: Set<() => void> = new Set();
    private uploadCallbacks: Set<(fileName: string, progress: number) => void> = new Set();

    constructor(elem: HTMLElement) {
        this.elem = elem;
        this.showTerminalStateOverlay =
            (typeof gotty_show_terminal_state !== 'undefined') ? !!gotty_show_terminal_state : false;
        this.term = new Terminal({
            theme: {
                background: 'rgb(40, 41, 53)'
            },
            scrollback: 500
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

        // 创建连接数显示
        this.connectionCountElem = elem.ownerDocument.createElement("div");
        this.connectionCountElem.className = "connection-count";
        this.connectionCountElem.style.cssText = "position:fixed;top:10px;right:10px;background:rgba(0,0,0,0.7);color:#fff;padding:5px 10px;border-radius:4px;font-size:12px;z-index:1000;";
        this.connectionCountElem.textContent = "连接数: 1";
        elem.appendChild(this.connectionCountElem);

        // 创建清理历史按钮
        this.clearHistoryBtn = elem.ownerDocument.createElement("button");
        this.clearHistoryBtn.className = "clear-history-btn";
        this.clearHistoryBtn.innerHTML = "🗑️";
        this.clearHistoryBtn.title = "清除历史消息";
        this.clearHistoryBtn.style.cssText = "position:fixed;top:10px;right:120px;background:rgba(0,0,0,0.7);color:#fff;padding:5px 10px;border-radius:4px;font-size:14px;z-index:1000;cursor:pointer;border:none;";
        this.clearHistoryBtn.addEventListener('click', () => {
            this.clearHistory();
        });
        elem.appendChild(this.clearHistoryBtn);

        if (this.showTerminalStateOverlay) {
            this.terminalStateElem = elem.ownerDocument.createElement("div");
            this.terminalStateElem.className = "terminal-state";
            this.terminalStateElem.style.cssText = "position:fixed;top:42px;right:10px;background:rgba(0,0,0,0.7);color:#fff;padding:4px 8px;border-radius:4px;font-size:11px;z-index:1000;";
            this.terminalStateElem.textContent = "尺寸策略: --";
            elem.appendChild(this.terminalStateElem);
        }

        // 创建上传模态框容器
        this.uploadModalContainer = document.createElement('div');
        document.body.appendChild(this.uploadModalContainer);

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
            this.scheduleFit();
        };

        this.term.open(elem);
        this.fitTerminal();
        this.term.focus();

        window.addEventListener("resize", this.resizeListener);
        window.addEventListener("orientationchange", this.resizeListener);
        if (window.visualViewport) {
            this.viewportResizeListener = () => this.scheduleFit();
            window.visualViewport.addEventListener("resize", this.viewportResizeListener);
        }
    };

    // Set up drag & drop handler for file upload
    private setupDragDropHandler() {
        // 创建拖放遮罩
        this.dropOverlay = new DropOverlay();

        // Bind to window to ensure we capture all drag events
        // This prevents Chrome from opening dropped files
        ['dragenter', 'dragover', 'dragleave', 'drop'].forEach(eventName => {
            window.addEventListener(eventName, (e: DragEvent) => {
                e.preventDefault();
                e.stopPropagation();
            }, false);
        });

        // Show overlay on drag enter
        window.addEventListener('dragenter', (e: DragEvent) => {
            if (e.dataTransfer?.files.length) {
                this.dropOverlay?.show(e.dataTransfer.files);
                document.body.classList.add('gotty-drag-border');
            }
        }, false);

        // Update file list on drag over (throttled)
        let lastDragOver = 0;
        window.addEventListener('dragover', (e: DragEvent) => {
            const now = Date.now();
            if (e.dataTransfer?.files.length && now - lastDragOver > 100) {
                lastDragOver = now;
                this.dropOverlay?.show(e.dataTransfer.files);
            }
        }, false);

        // Remove overlay on drag leave
        window.addEventListener('dragleave', (e: DragEvent) => {
            // Only hide if leaving the window or dropping outside
            if (!e.relatedTarget || (
                typeof e.relatedTarget === 'object' &&
                !document.body.contains(e.relatedTarget as Node)
            )) {
                this.dropOverlay?.hide();
                document.body.classList.remove('gotty-drag-border');
            }
        }, false);

        // Handle dropped files
        window.addEventListener('drop', async (e: DragEvent) => {
            e.preventDefault();
            this.dropOverlay?.hide();
            document.body.classList.remove('gotty-drag-border');

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
            this.showMessage(`${fileName} 为空文件`, 3000);
            return;
        }

        const chunkSize = this.getUploadChunkSize(fileName, fileSize);
        if (chunkSize <= 0) {
            this.showMessage(`上传 ${fileName} 失败: 分块过大`, 3000);
            return;
        }

        const totalChunks = Math.ceil(fileSize / chunkSize);

        // 创建上传任务状态
        const uploadTask: UploadTask = {
            id: Date.now().toString(36) + Math.random().toString(36).substr(2),
            name: fileName,
            size: fileSize,
            progress: 0,
            speed: 0,
            status: 'uploading',
            startTime: Date.now()
        };

        this.currentUploadTask = uploadTask;
        this.uploadStartTime = Date.now();

        // 显示上传进度模态框
        this.showUploadModal(uploadTask);

        // Notify upload start
        this.uploadCallbacks.forEach((cb) => cb(fileName, 0));
        this.showMessage(`正在上传 ${fileName}...`, 0);

        try {
            const arrayBuffer = await file.arrayBuffer();
            const uint8Array = new Uint8Array(arrayBuffer);

            for (let chunk = 0; chunk < totalChunks; chunk++) {
                // 检查是否已取消
                if (this.currentUploadTask?.status === 'cancelled') {
                    this.showMessage(`已取消上传: ${fileName}`, 3000);
                    this.hideUploadModal();
                    return;
                }

                const start = chunk * chunkSize;
                const end = Math.min(start + chunkSize, fileSize);
                const chunkData = uint8Array.slice(start, end);

                // Convert to base64 using binary string conversion (avoids stack overflow for large chunks)
                let binary = '';
                for (let i = 0; i < chunkData.length; i++) {
                    binary += String.fromCharCode(chunkData[i]);
                }
                const base64Data = btoa(binary);

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

                // Calculate progress and speed
                const progress = Math.round(((chunk + 1) / totalChunks) * 100);
                const now = Date.now();
                const elapsed = (now - this.uploadStartTime) / 1000;
                const uploadedBytes = (chunk + 1) * chunkSize;
                const speed = elapsed > 0 ? uploadedBytes / elapsed : 0;

                // 更新任务状态
                uploadTask.progress = progress;
                uploadTask.speed = speed;
                uploadTask.size = fileSize; // 确保使用原始大小

                // Notify progress
                this.uploadCallbacks.forEach((cb) => cb(fileName, progress));
            }

            // 上传完成 - 等待用户点击确定按钮关闭
            uploadTask.status = 'completed';
            uploadTask.progress = 100;
            this.showMessage(`${fileName} 上传成功`, 3000);
            // 不自动关闭模态框，等待用户点击确定按钮

        } catch (error) {
            console.error('Upload failed:', error);
            uploadTask.status = 'error';
            this.showMessage(`上传 ${fileName} 失败`, 3000);
        }
    }

    // 显示上传模态框
    private showUploadModal(task: UploadTask) {
        if (!this.uploadModalContainer) return;

        render(
            <UploadProgressModal
                task={task}
                onCancel={() => this.cancelUpload()}
                onDismiss={() => this.hideUploadModal()}
            />,
            this.uploadModalContainer
        );
    }

    // 隐藏上传模态框
    private hideUploadModal() {
        if (this.uploadModalContainer) {
            // 先调用模态框的 hide 方法，等待 Bootstrap 清理 backdrop
            const modalElem = this.uploadModalContainer.querySelector('.modal');
            if (modalElem) {
                const bsModal = Modal.getInstance(modalElem);
                if (bsModal) {
                    // 监听隐藏完成事件
                    const onHidden = () => {
                        this.uploadModalContainer!.innerHTML = '';
                        this.currentUploadTask = null;
                        modalElem.removeEventListener('hidden.bs.modal', onHidden);
                    };
                    modalElem.addEventListener('hidden.bs.modal', onHidden);
                    bsModal.hide();
                    return;
                }
            }
            // 如果没有找到模态框实例，直接清理
            this.uploadModalContainer.innerHTML = '';
        }
        this.currentUploadTask = null;
    }

    // 取消上传
    private cancelUpload() {
        if (this.currentUploadTask && this.currentUploadTask.status === 'uploading') {
            this.currentUploadTask.status = 'cancelled';

            // 发送取消消息到服务器
            if (this.sendUploadFile) {
                this.sendUploadFile(MSG_UPLOAD_CANCEL);
            }
        }
    }

    private scheduleFit(): void {
        if (this.fitRequestId !== null) {
            cancelAnimationFrame(this.fitRequestId);
        }
        this.fitRequestId = requestAnimationFrame(() => {
            this.fitRequestId = null;
            this.fitTerminal();
        });
    }

    private fitTerminal(): void {
        // Fit terminal size to the container so the viewport can reach the bottom.
        this.fitAddOn.fit();
        this.term.scrollToBottom();
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

    updateConnectionCount(count: number) {
        this.connectionCountElem.textContent = `连接数: ${count}`;
    };

    updateTerminalState(state: TerminalStatePayload) {
        if (!this.terminalStateElem) {
            return;
        }
        const leader = state.leaderClientId || "-";
        const size = `${state.activeCols}x${state.activeRows}`;
        this.terminalStateElem.textContent = `尺寸: ${size} | 策略: ${state.policy} | leader: ${leader}`;
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
        this.hideUploadModal();
    }

    clearHistory(): void {
        this.term.clear();
        this.showMessage("历史消息已清除", 2000);
    }

    close(): void {
        window.removeEventListener("resize", this.resizeListener);
        window.removeEventListener("orientationchange", this.resizeListener);
        if (window.visualViewport && this.viewportResizeListener) {
            window.visualViewport.removeEventListener("resize", this.viewportResizeListener);
        }
        if (this.fitRequestId !== null) {
            cancelAnimationFrame(this.fitRequestId);
            this.fitRequestId = null;
        }
        this.term.dispose();
        this.dropOverlay?.destroy();
        this.hideUploadModal();
        if (this.uploadModalContainer) {
            document.body.removeChild(this.uploadModalContainer);
        }
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
