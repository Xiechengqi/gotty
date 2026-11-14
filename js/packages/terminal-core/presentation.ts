import type {TerminalStatePayload} from './webtty';

// Hold only the first drawing while replay and an adaptive desktop acquisition
// settle. Existing renderers stay visible during ordinary switches/reconnects.
export class InitialPresentation {
    private presented = false;
    private parsed = false;
    private replaying = false;
    private expired = false;
    private state?: TerminalStatePayload;
    private local?: {columns: number; rows: number};
    private timer?: ReturnType<typeof setTimeout>;
    private frame = 0;
    private disposed = false;

    constructor(private host: HTMLElement, private writable: boolean, private beforeShow: () => void) {
        host.dataset.terminalPresented = 'false';
    }

    measured(columns: number, rows: number) {
        this.local = {columns, rows}; this.check();
    }

    geometry(state: TerminalStatePayload) {this.state = state; this.check();}
    outputParsed = () => {if (!this.replaying) {this.parsed = true; this.check();}};
    replay(active: boolean) {
        this.replaying = active;
        if (!active) this.parsed = true;
        this.check();
    }

    private check() {
        if (this.disposed || this.presented || !this.parsed || this.replaying || this.host.ownerDocument.hidden || !this.host.clientHeight || !this.host.clientWidth) return;
        const s = this.state, local = this.local;
        const fine = this.host.ownerDocument.defaultView?.matchMedia?.('(any-pointer: fine) and (any-hover: hover)').matches;
        const different = !local || !s || s.activeCols !== local.columns || s.activeRows !== local.rows;
        const pending = this.writable && fine && (!s?.policy || (s.policy === 'adaptive' && different &&
            (s.leaderDevice !== 'desktop' || s.sourceClientId !== s.leaderClientId)));
        if (pending && !this.expired) {
            // Bound the wait for old servers, explicit limits or failed ioctl.
            // This is a presentation fallback; it never invents a local grid.
            this.timer ??= setTimeout(() => {
                this.timer = undefined;
                this.expired = !this.host.ownerDocument.hidden && !!this.host.clientHeight && !!this.host.clientWidth;
                this.check();
            }, 1500);
            return;
        }
        if (this.frame) return;
        this.beforeShow();
        this.frame = requestAnimationFrame(() => {
            this.frame = 0;
            if (this.disposed || this.replaying || !this.host.clientHeight || !this.host.clientWidth) return;
            this.presented = true; clearTimeout(this.timer);
            this.host.dataset.terminalPresented = 'true';
        });
    }

    dispose() {this.disposed = true; clearTimeout(this.timer); cancelAnimationFrame(this.frame);}
}
