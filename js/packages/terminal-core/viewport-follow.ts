// A shared PTY can be taller than this browser. Xterm's own scrollback and
// the outer element's pixel scroll are separate; neither changes PTY geometry.
interface Disposable { dispose(): void }
interface FollowTerminal {
    rows: number;
    cols: number;
    buffer: {
        active: {type: string; cursorX: number; cursorY: number; baseY: number; viewportY: number;
            getLine?(line: number): {translateToString(trimRight?: boolean): string} | undefined};
        onBufferChange(listener: () => void): Disposable;
    };
    onScroll(listener: () => void): Disposable;
    onCursorMove(listener: () => void): Disposable;
    onWriteParsed(listener: () => void): Disposable;
    onRender(listener: () => void): Disposable;
    onSelectionChange(listener: () => void): Disposable;
    onKey(listener: () => void): Disposable;
    hasSelection(): boolean;
    clearSelection(): void;
    scrollToBottom(): void;
    scrollToLine(line: number): void;
    registerMarker?(offset: number): {line: number; dispose(): void} | undefined;
    parser: {
        registerCsiHandler(id: {prefix: string; final: string}, handler: (params: (number | number[])[]) => boolean): Disposable;
        registerEscHandler(id: {final: string}, handler: () => boolean): Disposable;
    };
}

interface Position {distance: number; top: number; left: number}

export class ViewportFollow {
    private following = true;
    private pauseReason = 'live';
    private replaying = false;
    private cursorVisible = true;
    private changing = false;
    private disposed = false;
    private frame = 0;
    private settling = 0;
    private settleFrame = 0;
    private horizontal = false;
    private lastTop: number;
    private lastBase: number;
    private intentUntil = 0;
    private touch?: {x: number; y: number};
    private liveAnchor?: {row: number; rows: number; type: string};
    private saved?: Position;
    private anchor?: {line: number; top: number; marker?: {line: number; dispose(): void}};
    private disposables: Disposable[] = [];
    private cleanups: (() => void)[] = [];

    constructor(private term: FollowTerminal, private host: HTMLElement,
        private onFollowing: (following: boolean) => void = () => {}) {
        this.lastTop = host.scrollTop;
        this.lastBase = term.buffer.active.baseY;
        this.publish();
        this.disposables.push(
            term.onCursorMove(this.layout), term.onWriteParsed(this.layout), term.onRender(this.layout),
            term.buffer.onBufferChange(() => {this.liveAnchor = undefined; this.layout();}), term.onKey(() => this.userInput()),
            term.onSelectionChange(() => {if (!this.replaying && term.hasSelection()) this.follow(false, 'selection');}),
            term.onScroll(() => {
                if (this.changing || this.replaying) return;
                // New output can advance xterm's live viewport even when the
                // user scrolled only the outer element. Keep its buffer anchor.
                if (term.buffer.active.baseY !== this.lastBase) {
                    this.lastBase = term.buffer.active.baseY; this.layout(); return;
                }
                if (this.anchor?.marker && this.anchor.marker.line !== this.anchor.line && term.buffer.active.viewportY === this.anchor.line) {
                    // At the history limit baseY stops increasing, but markers
                    // still move when old lines are trimmed. This is output.
                    this.layout(); return;
                }
                this.navigation();
                this.layout();
            }),
            // Observe DECTCEM without consuming it; hidden redraw cursors must
            // not drag a small viewport around a full-screen application.
            ...(['h', 'l'] as const).map(final => term.parser.registerCsiHandler({prefix: '?', final}, params => {
                if (params.includes(25)) {
                    if (this.cursorVisible && final === 'l') this.rememberCursor();
                    this.cursorVisible = final === 'h'; this.layout();
                }
                return false;
            })),
            term.parser.registerEscHandler({final: 'c'}, () => {this.cursorVisible = true; this.liveAnchor = undefined; this.layout(); return false;}),
        );
        this.listen(host, 'scroll', () => {
            const top = host.scrollTop;
            if (Math.abs(top - this.lastTop) < 1) return;
            this.lastTop = top;
            this.navigation();
        });
        this.listen(host, 'wheel', event => {
            const e = event as WheelEvent;
            if (e.ctrlKey || Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
            this.markIntent();
        }, true);
        // Scrollbar dragging produces no wheel events.
        this.listen(host, 'pointerdown', event => {
            const target = event.target as HTMLElement;
            if (target === host || target.closest?.('.xterm-viewport, .xterm-scrollable-element')) this.markIntent(60000);
        }, true);
        this.listen(host.ownerDocument, 'pointerup', () => {
            if (this.intentUntil > Date.now()) this.intentUntil = Date.now() + 250;
            this.navigation();
        });
        this.listen(host, 'touchstart', event => {
            const touches = (event as TouchEvent).touches;
            this.touch = touches.length === 1 ? {x: touches[0].clientX, y: touches[0].clientY} : undefined;
            if (touches.length !== 1) this.intentUntil = 0;
        }, true);
        this.listen(host, 'touchmove', event => {
            const touches = (event as TouchEvent).touches;
            if (!this.touch || touches.length !== 1) return;
            const dx = touches[0].clientX - this.touch.x, dy = touches[0].clientY - this.touch.y;
            if (Math.abs(dy) > 8 && Math.abs(dy) > Math.abs(dx)) {
                // A gesture at the live boundary may not scroll anything.
                // Pause only when either scroll layer actually leaves live.
                this.markIntent();
            }
        }, true);
        const touchDone = () => {
            if (this.touch && this.intentUntil > Date.now()) this.intentUntil = Date.now() + 1500;
            this.touch = undefined; this.navigation(); this.layout();
        };
        this.listen(host, 'touchend', touchDone);
        this.listen(host, 'touchcancel', touchDone);
        // IME commits have no onKey; clipboard/voice/shortcut callers also call
        // userInput explicitly. Terminal query replies never resume following.
        this.listen(host, 'beforeinput', () => this.userInput());
        this.listen(host.ownerDocument, 'visibilitychange', this.layout);
        if (typeof ResizeObserver !== 'undefined') {
            const observer = new ResizeObserver(this.layout); observer.observe(host);
            this.cleanups.push(() => observer.disconnect());
        }
        this.layout();
    }

    private listen(target: EventTarget, type: string, listener: EventListener, capture = false) {
        target.addEventListener(type, listener, {passive: true, capture});
        this.cleanups.push(() => target.removeEventListener(type, listener, capture));
    }

    private publish() {
        this.host.dataset.viewportFollowing = String(this.following);
        this.host.dataset.viewportPauseReason = this.pauseReason;
        this.onFollowing(this.following);
    }

    private follow(value: boolean, reason = 'history') {
        this.pauseReason = value ? 'live' : reason;
        if (this.following === value) {this.host.dataset.viewportPauseReason = this.pauseReason; return;}
        this.following = value;
        this.anchor?.marker?.dispose(); this.anchor = undefined;
        if (!value) this.captureAnchor();
        this.publish();
    }

    private markIntent(duration = 1000) {
        this.intentUntil = Date.now() + duration;
        // Fresh user input takes precedence over a previous layout barrier.
        this.settling = 0; cancelAnimationFrame(this.settleFrame); this.settleFrame = 0;
    }

    private navigation() {
        if (this.replaying || this.changing || this.settling || Date.now() > this.intentUntil) return;
        if (this.host.ownerDocument.hidden || !this.host.clientHeight || !this.host.clientWidth) return;
        const vv = this.host.ownerDocument.defaultView?.visualViewport;
        if (vv && Math.abs(vv.scale - 1) > 0.01) return;
        if (this.term.hasSelection()) {this.follow(false, 'selection'); return;}
        const point = this.livePoint();
        if (!point) return;
        const atLive = this.term.buffer.active.viewportY === this.term.buffer.active.baseY &&
            point.top >= this.host.scrollTop - 1 && point.bottom <= this.host.scrollTop + this.visibleHeight() + 1;
        if (atLive) {
            if (!this.following) {this.follow(true); this.layout();}
        } else {
            this.follow(false);
            this.captureAnchor();
        }
        this.diagnose();
    }

    private settleLayout() {
        this.settling = 2;
        if (this.settleFrame) return;
        const next = () => {
            this.settleFrame = 0;
            if (this.disposed) return;
            if (--this.settling > 0) this.settleFrame = requestAnimationFrame(next);
            else {this.settling = 0; this.layout();}
        };
        this.settleFrame = requestAnimationFrame(next);
    }

    private captureAnchor() {
        // display:none reports a zero scroll offset. Background geometry/font
        // updates must not replace the last visible browsing position with it.
        const top = this.host.clientHeight ? this.host.scrollTop : (this.anchor?.top ?? this.host.scrollTop);
        this.anchor?.marker?.dispose();
        const b = this.term.buffer.active;
        this.anchor = {line: b.viewportY, top,
            marker: this.term.registerMarker?.(b.viewportY - b.baseY - b.cursorY)};
    }

    private restoreAnchor() {
        if (!this.anchor) return;
        const {marker, line, top} = this.anchor;
        this.changing = true;
        const target = Math.max(0, marker ? marker.line : line);
        this.term.scrollToLine(target);
        this.anchor.line = target;
        this.host.scrollTop = top; this.lastTop = this.host.scrollTop;
        this.changing = false;
    }

    private position(): Position {
        const b = this.term.buffer.active;
        return {distance: b.baseY - b.viewportY,
            top: this.host.clientHeight ? this.host.scrollTop : (this.anchor?.top ?? this.host.scrollTop), left: this.host.scrollLeft};
    }

    private restore(position: Position) {
        this.changing = true;
        this.term.scrollToLine(Math.max(0, this.term.buffer.active.baseY - position.distance));
        this.host.scrollTop = position.top; this.host.scrollLeft = position.left;
        this.lastTop = this.host.scrollTop;
        this.lastBase = this.term.buffer.active.baseY;
        this.changing = false;
    }

    // Wrap confirmed-grid/font layout changes so xterm's resize scroll events
    // cannot be mistaken for deliberate history navigation.
    changeLayout(update: () => void) {
        const position = !this.following && !this.replaying ? this.position() : undefined;
        this.settleLayout();
        this.changing = true;
        try {update();} finally {this.changing = false;}
        if (position) {
            // Xterm adjusts markers when column reflow changes line indices.
            // Use that content anchor instead of guessing from the new base.
            if (this.anchor?.marker) this.restoreAnchor();
            else this.restore(position);
            this.captureAnchor();
        }
        this.lastBase = this.term.buffer.active.baseY;
        this.layout();
    }

    setReplayMode(active: boolean) {
        if (active === this.replaying) return;
        if (active) this.saved = this.position();
        this.replaying = active;
        if (!active) {
            if (!this.following && this.saved) {this.restore(this.saved); this.captureAnchor();}
            this.lastBase = this.term.buffer.active.baseY;
            this.saved = undefined;
            this.layout();
        }
    }

    reset() {this.cursorVisible = true; this.liveAnchor = undefined; this.layout();}

    userInput() {
        if (this.replaying || this.disposed) return;
        this.intentUntil = 0;
        this.term.clearSelection();
        this.follow(true); this.horizontal = true; this.layout();
    }

    private rememberCursor() {
        const b = this.term.buffer.active;
        this.liveAnchor = {row: b.cursorY, rows: this.term.rows, type: b.type};
    }

    private livePoint() {
        const {host, term} = this;
        const screen = host.querySelector('.xterm-screen')?.getBoundingClientRect();
        if (!screen?.height || !screen.width || !host.clientHeight || !host.clientWidth || !term.rows) return;
        const rect = host.getBoundingClientRect(), b = term.buffer.active;
        let row = b.cursorY;
        if (this.cursorVisible) this.rememberCursor();
        else {
            // Hidden TUI redraw cursors often end at row zero. Keep the last
            // input/content anchor, including on initial replay with no visible
            // cursor, instead of following that transient redraw position.
            const saved = this.liveAnchor?.type === b.type ? this.liveAnchor : undefined;
            row = saved ? (saved.row === saved.rows - 1 ? term.rows - 1 : Math.min(saved.row, term.rows - 1)) : row;
            for (let y = term.rows - 1; y >= 0; y--) {
                if (b.getLine?.(b.baseY + y)?.translateToString(true).length) {row = Math.max(row, y); break;}
            }
        }
        const height = screen.height / term.rows, width = screen.width / term.cols;
        const top = screen.top - rect.top - host.clientTop + host.scrollTop + (b.baseY + row - b.viewportY) * height;
        const left = screen.left - rect.left - host.clientLeft + host.scrollLeft + Math.min(b.cursorX, term.cols - 1) * width;
        return {top, bottom: top + height, left, right: left + width};
    }

    private visibleHeight() {
        // clientHeight includes safe-area padding; keep the input above it.
        return this.host.clientHeight - (parseFloat(getComputedStyle(this.host).paddingBottom) || 0);
    }

    // Content-free support diagnostics. Never include terminal text, browser
    // identity, URLs or authentication material in this DOM snapshot.
    diagnostics() {
        const b = this.term.buffer.active;
        const vv = typeof window !== 'undefined' ? window.visualViewport : undefined;
        return {following: this.following, pauseReason: this.pauseReason, replaying: this.replaying,
            cursorVisible: this.cursorVisible, buffer: b.type, columns: this.term.cols, rows: this.term.rows,
            inner: b.viewportY, base: b.baseY, outer: this.host.scrollTop,
            outerMax: Math.max(0, this.host.scrollHeight - this.host.clientHeight),
            pageVisible: !this.host.ownerDocument.hidden, paneVisible: !!this.host.clientHeight && !!this.host.clientWidth,
            viewportHeight: vv?.height, viewportOffsetTop: vv?.offsetTop, scale: vv?.scale};
    }

    private diagnose() {this.host.dataset.viewportState = JSON.stringify(this.diagnostics());}

    // Called after parser/render/layout readiness, never once per output byte.
    // Replay's scrollToBottom hook schedules this without overriding browsing.
    layout = () => {
        if (this.disposed || this.frame) return;
        this.frame = requestAnimationFrame(() => {
            this.frame = 0;
            if (this.disposed || this.replaying) return;
            // Pinch zoom and its native page panning belong to the browser.
            if (typeof window !== 'undefined' && window.visualViewport && Math.abs(window.visualViewport.scale - 1) > 0.01) return;
            if (!this.following) {this.restoreAnchor(); this.diagnose(); return;}
            if (this.term.hasSelection()) return;
            if (!this.host.clientHeight || !this.host.clientWidth) return;
            this.changing = true; this.term.scrollToBottom(); this.changing = false;
            const cursor = this.livePoint();
            if (!cursor) return;
            const top = this.host.scrollTop, left = this.host.scrollLeft;
            this.changing = true;
            // Normal output follows the live input/content row at the bottom
            // of this browser. Alternate-screen editors reveal their cursor
            // with minimal panning rather than anchoring to blank PTY rows.
            if (this.term.buffer.active.type === 'normal' || !this.cursorVisible) this.host.scrollTop = Math.max(0, cursor.bottom - this.visibleHeight());
            else if (cursor.top < top) this.host.scrollTop = cursor.top;
            else if (cursor.bottom > top + this.visibleHeight()) this.host.scrollTop = cursor.bottom - this.visibleHeight();
            this.lastTop = this.host.scrollTop;
            if (this.horizontal && this.cursorVisible) {
                if (cursor.left < left) this.host.scrollLeft = cursor.left;
                else if (cursor.right > left + this.host.clientWidth) this.host.scrollLeft = cursor.right - this.host.clientWidth;
                this.horizontal = false;
            }
            this.changing = false;
            this.diagnose();
        });
    };

    dispose() {
        this.disposed = true; cancelAnimationFrame(this.frame); cancelAnimationFrame(this.settleFrame);
        this.anchor?.marker?.dispose();
        this.disposables.forEach(value => value.dispose());
        this.cleanups.forEach(cleanup => cleanup());
    }
}
