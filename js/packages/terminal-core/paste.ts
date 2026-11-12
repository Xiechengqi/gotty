// Paste progress measures UTF-8 payload bytes, excluding bracketed-paste markers.
// A confirmed result means PTY write acceptance, never application completion.
export type PasteState = {
    id: string; total: number; loaded: number; sent: number; confirmed: boolean;
    status: 'running' | 'stopping' | 'done' | 'sent' | 'cancelled' | 'error';
    visible: boolean; error?: string;
};
export type PasteReply = {id: string; seq: number; received: number; status: 'writing' | 'done' | 'cancelled' | 'error'; error?: string};
type Transport = {send: (frame: string) => boolean; buffered: () => number; chunkSize: () => number; changed: (state?: PasteState) => void; message: (text: string) => void};
type Task = {state: PasteState; bytes: Uint8Array; bracketed: boolean; started: boolean; ending: boolean; seq: number; ack: number; pending: Map<number, number>; lastActivity: number; began: number};
const START = '\x1b[200~', END = '\x1b[201~';
const encoder = new TextEncoder();
const MAX_PASTE = 64 * 1024 * 1024;
const WINDOW = 8;
const BUFFER = 64 * 1024;
const TIMEOUT = 30000;

export function base64Bytes(bytes: Uint8Array): string {
    // Never spread a whole paste onto the JS stack.
    let text = '';
    for (let i = 0; i < bytes.length; i++) text += String.fromCharCode(bytes[i]);
    return btoa(text);
}

// Replies still have to flow while user keystrokes wait behind a paste. The
// server additionally verifies CPR ownership against outstanding PTY queries.
function isReply(input: string | Uint8Array): boolean {
    if (input.length > 1024) return false;
    const text = typeof input === 'string' ? input : new TextDecoder().decode(input);
    return /^(?:\x1b\[[?>]?[0-9;:]*[cnR]|\x1b\[\??[0-9;]*\$y|\x1b\](?:4;[0-9]+|10|11|12);[^\x1b\x07]*(?:\x07|\x1b\\)|\x1bP[01][+$]r[^\x1b]*\x1b\\)$/.test(text);
}

export class InputSender {
    acknowledged = false;
    private task?: Task;
    private queue: Uint8Array[] = [];
    private queued = 0;
    private timer?: ReturnType<typeof setTimeout>;
    private dismissTimer?: ReturnType<typeof setTimeout>;
    private queueSince = 0;
    private reportedAt = 0;
    private reportedStatus = '';
    constructor(private transport: Transport) {}

    input(input: string | Uint8Array): void {
        const bytes = typeof input === 'string' ? encoder.encode(input) : input.slice();
        if (!bytes.length) return;
        if (this.task && isReply(input)) {
            if (!this.transport.send('1' + base64Bytes(bytes))) this.disconnect();
            return;
        }
        if (this.queued + bytes.length > MAX_PASTE || (this.task && this.queued + bytes.length > BUFFER)) {
            this.transport.message('Input queue is full; wait for the paste to finish'); return;
        }
        if (!this.queued) this.queueSince = Date.now();
        this.queue.push(bytes); this.queued += bytes.length;
        this.pump();
    }

    paste(text: string, bracketed: boolean): void {
        if (this.task || this.queued) {this.transport.message('Another input transfer is running'); return;}
        // Refuse before normalization/encoding very large clipboard strings.
        if (text.length > MAX_PASTE) {this.transport.message('Maximum paste size: 64 MiB'); return;}
        const bytes = encoder.encode(text.replace(/\r?\n/g, '\r'));
        if (!bytes.length) return;
        if (bytes.length > MAX_PASTE) {this.transport.message('Maximum paste size: 64 MiB'); return;}
        clearTimeout(this.dismissTimer);
        const id = globalThis.crypto?.randomUUID?.() ?? Date.now().toString(36) + Math.random().toString(36).slice(2);
        const state: PasteState = {id, total: bytes.length, loaded: 0, sent: 0, confirmed: this.acknowledged, status: 'running', visible: bytes.length > 8192};
        this.task = {state, bytes, bracketed, started: false, ending: false, seq: 0, ack: -1, pending: new Map(), lastActivity: Date.now(), began: Date.now()};
        this.reportedAt = 0; this.reportedStatus = '';
        this.emit(); this.schedule(); // Always yield so the initial UI can paint.
    }

    cancel(): void {
        const task = this.task;
        if (!task || task.state.status !== 'running') return;
        this.queue = []; this.queued = 0;
        task.state.status = 'stopping'; task.lastActivity = Date.now();
        if (task.state.confirmed && task.started) {
            if (!this.transport.send('5' + JSON.stringify({id: task.state.id, cancel: true}))) {this.disconnect(); return;}
        } else if (task.state.confirmed || !task.started) {this.finish('cancelled'); return;}
        this.emit(); this.schedule();
    }

    reply(reply: PasteReply): void {
        const task = this.task;
        if (!task || !task.state.confirmed || reply.id !== task.state.id) return;
        if (!Number.isSafeInteger(reply.received) || reply.received < task.state.loaded || reply.received > task.state.sent) {this.fail('Invalid paste confirmation'); return;}
        if (reply.status === 'writing' || reply.status === 'done') {
            if (reply.seq !== task.ack + 1 || task.pending.get(reply.seq) !== reply.received) {this.fail('Invalid paste confirmation'); return;}
            task.pending.delete(reply.seq); task.ack = reply.seq;
        }
        task.state.loaded = reply.received; task.lastActivity = Date.now();
        if (reply.status === 'error') {this.finish('error', reply.error || 'Paste write failed'); return;}
        if (reply.status === 'cancelled') {this.finish('cancelled'); return;}
        if (reply.status === 'done') {
            if (reply.received !== task.state.total || !task.ending) {this.fail('Invalid paste confirmation'); return;}
            this.finish('done'); return;
        }
        this.emit(); this.schedule();
    }

    disconnect(): void {
        clearTimeout(this.timer); this.timer = undefined;
        this.queue = []; this.queued = 0;
        if (this.task) this.finish('error', 'Paste interrupted; already written text cannot be undone. Nothing will be resent');
        clearTimeout(this.timer); this.timer = undefined;
        this.acknowledged = false;
    }
    dispose(): void {this.disconnect(); clearTimeout(this.dismissTimer); this.transport.changed(undefined);}
    dismiss(): void {if (!this.task) {clearTimeout(this.dismissTimer); this.transport.changed(undefined);}}

    private emit(): void {
        if (!this.task) return;
        const {state, began} = this.task;
        state.visible ||= Date.now() - began >= 150 || state.status === 'error';
        const status = `${state.status}:${state.visible}`;
        if (status === this.reportedStatus && Date.now() - this.reportedAt < 50) return;
        this.reportedAt = Date.now(); this.reportedStatus = status;
        this.transport.changed({...state});
    }
    private finish(status: PasteState['status'], error?: string): void {
        if (!this.task) return;
        this.task.state.status = status; this.task.state.error = error;
        this.emit(); this.task = undefined;
        if (status !== 'done' && status !== 'sent') {this.queue = []; this.queued = 0;}
        else this.queueSince = Date.now();
        clearTimeout(this.dismissTimer);
        if (status === 'done' || status === 'sent') this.dismissTimer = setTimeout(() => this.transport.changed(undefined), 1000);
        this.schedule();
    }
    private fail(message: string): void {
        const task = this.task;
        if (task?.state.confirmed && task.started) this.transport.send('5' + JSON.stringify({id: task.state.id, cancel: true}));
        this.finish('error', message);
    }
    private schedule(): void {
        if (this.timer === undefined) this.timer = setTimeout(() => {this.timer = undefined; this.pump();}, 8);
    }
    private pump(): void {
        const task = this.task;
        const now = Date.now();
        if (task) {
            if (now - task.lastActivity > TIMEOUT) {this.fail('Paste timed out; delivery is uncertain. Nothing will be resent'); return;}
            this.emit();
            if (this.transport.buffered() < BUFFER) {
                if (task.state.status === 'stopping') {
                    if (!task.state.confirmed) {
                        if (task.bracketed && !this.transport.send('1' + btoa(END))) {this.disconnect(); return;}
                        this.finish('cancelled'); return;
                    }
                } else {
                    for (let batch = 0; batch < 4 && !task.ending && task.pending.size < WINDOW && this.transport.buffered() < BUFFER; batch++) {
                        const start = task.state.sent;
                        // Keep UTF-8 codepoints intact at chunk/cancellation boundaries.
                        let end = Math.min(start + Math.max(4, Math.min(4096, this.transport.chunkSize() - 256)), task.bytes.length);
                        while (end < task.bytes.length && (task.bytes[end] & 0xc0) === 0x80) end--;
                        const final = end === task.bytes.length;
                        const data = base64Bytes(task.bytes.subarray(start, end));
                        let frame: string;
                        if (task.state.confirmed) {
                            frame = '5' + JSON.stringify({id: task.state.id, seq: task.seq, size: task.state.total, bracketed: task.bracketed, data, final});
                        } else {
                            if (!task.started && task.bracketed && !this.transport.send('1' + btoa(START))) {this.disconnect(); return;}
                            frame = '1' + data;
                        }
                        // Record before sending: also works with synchronous test transports.
                        task.pending.set(task.seq++, end); task.state.sent = end; task.started = true; task.ending = final;
                        if (!this.transport.send(frame)) {this.disconnect(); return;}
                        if (!task.state.confirmed) {
                            task.pending.clear(); task.lastActivity = now;
                            task.state.loaded = end;
                            if (final) {
                                if (task.bracketed && !this.transport.send('1' + btoa(END))) {this.disconnect(); return;}
                                this.finish('sent'); return;
                            }
                        }
                    }
                }
            }
            this.emit(); this.schedule(); return;
        }
        if (this.queued && now - this.queueSince > TIMEOUT) {
            this.queue = []; this.queued = 0; this.transport.message('Input timed out; queued text was discarded'); return;
        }
        for (let count = 0; count < 4 && this.queue.length && this.transport.buffered() < BUFFER; count++) {
            const bytes = this.queue[0], length = Math.min(bytes.length, this.transport.chunkSize(), 8192);
            if (!this.transport.send('1' + base64Bytes(bytes.subarray(0, length)))) {this.disconnect(); return;}
            this.queued -= length; this.queueSince = now;
            if (length === bytes.length) this.queue.shift(); else this.queue[0] = bytes.subarray(length);
        }
        if (this.queue.length) this.schedule();
    }
}

// Capture is necessary: xterm stops the native event during bubbling. This
// preserves its newline and single bracket-pair semantics without accessing
// private xterm internals or disabling protocol responses through disableStdin.
export function bindPaste(element: HTMLElement, options: () => {enabled: boolean; bracketed: boolean}, send: (text: string, bracketed: boolean) => void): () => void {
    const paste = (event: ClipboardEvent) => {
        if (!event.clipboardData) return;
        const {enabled, bracketed} = options();
        event.preventDefault(); event.stopImmediatePropagation();
        if (enabled) send(event.clipboardData.getData('text/plain'), bracketed);
        const textarea = element.querySelector('textarea'); if (textarea) textarea.value = '';
    };
    element.addEventListener('paste', paste, true);
    return () => element.removeEventListener('paste', paste, true);
}

export function pasteBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    return bytes < 1024 * 1024 ? `${(bytes / 1024).toFixed(1)} KiB` : `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

export function pasteText(state: PasteState, zh: boolean): string {
    if (state.status === 'error') return zh ? pasteError(state.error || '') : state.error || 'Paste failed';
    if (state.status === 'cancelled') return state.confirmed ? (zh ? '已停止；已经写入的文本无法撤回。' : 'Stopped. Already written text cannot be undone.') : (zh ? '已停止发送；之前发送的文本仍可能写入终端。' : 'Sending stopped. Previously sent text may still reach the terminal.');
    if (state.status === 'stopping') return zh ? '正在停止…' : 'Stopping…';
    if (state.status === 'done') return zh ? '已写入终端' : 'Written to terminal';
    if (state.status === 'sent') return zh ? '已发送；当前 GoTTY 不支持写入确认。' : 'Sent. This GoTTY cannot confirm PTY writes.';
    return state.confirmed ? (zh ? '正在粘贴…' : 'Pasting…') : (zh ? '正在发送（未经远端确认）…' : 'Sending (unconfirmed)…');
}

export function pasteError(error: string): string {
    const messages: Record<string, string> = {
        'Another input transfer is running': '已有输入正在传输，请等待完成。',
        'Input queue is full; wait for the paste to finish': '输入队列已满，请等待粘贴完成。',
        'Maximum paste size: 64 MiB': '单次粘贴最大支持 64 MiB。',
        'Input timed out; queued text was discarded': '输入发送超时，待发送文本已丢弃。',
        'Invalid paste confirmation': '粘贴确认异常；已停止发送，请检查终端。',
        'Paste interrupted; already written text cannot be undone. Nothing will be resent': '粘贴已中断，已写入的文本无法撤回；不会自动重发。',
        'Paste timed out; delivery is uncertain. Nothing will be resent': '粘贴超时，无法确认最终写入量；不会自动重发。',
        'Paste write failed': '终端写入失败；已停止粘贴。',
        'Paste permission denied': '没有终端写入权限。',
        'Terminal is busy': '终端正在粘贴或执行 API 命令，请稍后重试。',
        'Invalid paste request': '粘贴请求无效，已停止发送。',
        'Paste queue is full': '远端粘贴队列已满，已停止发送。',
        'Paste input timed out': '远端等待粘贴数据超时，已停止。',
        'Paste stopped; bracket cleanup failed': '粘贴已停止，终端暂时无法接收结束标记，请检查终端状态。',
    };
    return messages[error] || error;
}
