interface Point {x: number; y: number}
interface ScrollTarget {
    enabled(): boolean;
    start(): void;
    intent(): void;
    cancel(): void;
    scroll(pixels: number, point: Point): boolean;
    end(): void;
    momentum(): boolean;
}

// Native touch panning cannot move xterm 6's virtual scrollback. Claim only
// vertical single-finger motion; horizontal navigation and pinch stay native.
export class TouchScroll {
    private touch?: {id: number; x: number; y: number; lastY: number; at: number;
        axis: 'pending' | 'vertical' | 'native'; velocity: number};
    private point: Point = {x: 0, y: 0};
    private pending = 0;
    private frame = 0;
    private cleanups: (() => void)[] = [];

    constructor(host: HTMLElement, private target: ScrollTarget) {
        this.listen(host, 'touchstart', event => {
            this.stop();
            const e = event as TouchEvent;
            if (e.touches.length !== 1 || !this.eligible(e)) return;
            const p = e.touches[0];
            this.touch = {id: p.identifier, x: p.clientX, y: p.clientY, lastY: p.clientY,
                at: performance.now(), axis: 'pending', velocity: 0};
            this.target.start();
        }, true);
        this.listen(host, 'touchmove', event => this.move(event as TouchEvent), false);
        this.listen(host.ownerDocument, 'touchend', event => {
            const e = event as TouchEvent, touch = this.touch;
            if (!touch || !Array.from(e.changedTouches).some(p => p.identifier === touch.id)) return;
            if (e.touches.length) {this.cancel(); return;}
            this.flush();
            this.touch = undefined;
            this.target.end();
            // A short decay keeps history browsing natural without continuing
            // application mouse input after the finger leaves the screen.
            if (touch.axis === 'vertical' && performance.now() - touch.at < 80
                && Math.abs(touch.velocity) > 0.1 && this.target.momentum()) this.coast(touch.velocity);
        }, true);
        this.listen(host.ownerDocument, 'touchcancel', () => this.cancel(), true);
        this.listen(host.ownerDocument, 'touchstart', event => {
            if ((event as TouchEvent).touches.length !== 1) this.cancel();
        }, true);
        this.listen(host.ownerDocument, 'pointerdown', () => this.stop(), true);
        this.listen(host.ownerDocument, 'visibilitychange', () => this.cancel(), true);
        const win = host.ownerDocument.defaultView;
        if (win) {
            this.listen(win, 'blur', () => this.cancel(), true);
            this.listen(win, 'gesturestart', () => this.cancel(), true);
        }
    }

    private listen(target: EventTarget, type: string, listener: EventListener, passive: boolean) {
        target.addEventListener(type, listener, {capture: true, passive});
        this.cleanups.push(() => target.removeEventListener(type, listener, true));
    }

    private eligible(event: TouchEvent) {
        if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey
            || !this.target.enabled()) return false;
        const node = event.target instanceof Element ? event.target : null;
        return !node?.closest('button, input, select, a, [contenteditable=true], textarea:not(.xterm-helper-textarea)');
    }

    private move(event: TouchEvent) {
        const touch = this.touch;
        if (!touch) return;
        const point = Array.from(event.touches).find(p => p.identifier === touch.id);
        if (event.touches.length !== 1 || !point || !this.eligible(event)) {this.cancel(); return;}
        if (touch.axis === 'native') return;
        const x = point.clientX - touch.x, y = point.clientY - touch.y, at = performance.now();
        if (touch.axis === 'pending') {
            if (Math.max(Math.abs(x), Math.abs(y)) < 8) return;
            // Leave a held gesture to selection/copy, and diagonals to native
            // panning. Hub claims clearly horizontal tab swipes above us.
            if (at - touch.at > 250 || Math.abs(y) <= 1.25 * Math.abs(x)) {touch.axis = 'native'; return;}
            touch.axis = 'vertical';
        }
        this.target.intent();
        if (!event.cancelable) {this.stop(); return;}
        event.preventDefault();
        const delta = touch.lastY - point.clientY, elapsed = at - touch.at;
        if (elapsed > 0 && elapsed < 100) touch.velocity = 0.5 * touch.velocity + 0.5 * delta / elapsed;
        else touch.velocity = 0;
        touch.lastY = point.clientY; touch.at = at;
        this.point = {x: point.clientX, y: point.clientY};
        this.pending += delta;
        if (!this.frame) this.frame = requestAnimationFrame(() => {this.frame = 0; this.flush();});
    }

    private flush() {
        cancelAnimationFrame(this.frame); this.frame = 0;
        const pixels = this.pending; this.pending = 0;
        return pixels !== 0 && this.target.enabled() && this.target.scroll(pixels, this.point);
    }

    private coast(speed: number) {
        let velocity = Math.max(-2, Math.min(2, speed)), at = performance.now();
        const step = () => {
            this.frame = 0;
            const now = performance.now(), elapsed = Math.min(40, now - at); at = now;
            if (!this.target.enabled() || !this.target.momentum() || Math.abs(velocity) < 0.02) return;
            if (elapsed > 0 && !this.target.scroll(velocity * elapsed, this.point)) return;
            velocity *= Math.exp(-elapsed / 180);
            this.frame = requestAnimationFrame(step);
        };
        this.frame = requestAnimationFrame(step);
    }

    stop() {
        cancelAnimationFrame(this.frame); this.frame = 0;
        this.touch = undefined; this.pending = 0;
    }

    private cancel() {this.stop(); this.target.cancel();}

    dispose() {this.stop(); this.cleanups.forEach(cleanup => cleanup());}
}
