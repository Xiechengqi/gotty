import {render} from 'preact';
import {pasteBytes, pasteText, type PasteState} from '../packages/terminal-core/paste';
import './paste-progress.css';

// Deliberately nonmodal: terminal focus, rendering and geometry stay intact.
export class PasteProgress {
    private root = document.createElement('div');
    constructor() {
        this.root.className = 'gotty-paste-layer';
        document.body.appendChild(this.root);
        window.addEventListener('resize', this.measure);
        window.visualViewport?.addEventListener('resize', this.measure);
        window.visualViewport?.addEventListener('scroll', this.measure);
        this.measure();
    }
    private measure = () => {
        const viewport = window.visualViewport;
        Object.assign(this.root.style, {left: `${viewport?.offsetLeft ?? 0}px`, top: `${viewport?.offsetTop ?? 0}px`, width: `${viewport?.width ?? window.innerWidth}px`, height: `${viewport?.height ?? window.innerHeight}px`});
    };
    update(state: PasteState | undefined, cancel: () => void, dismiss: () => void): void {
        if (!state?.visible) {render(null, this.root); return;}
        const zh = (document.documentElement.lang || navigator.language).startsWith('zh');
        const active = state.status === 'running' || state.status === 'stopping';
        render(<section className="gotty-paste-card" data-paste-status={state.status} aria-label={zh ? '粘贴进度' : 'Paste progress'}>
            <div className="gotty-paste-heading"><bdi>{document.title}</bdi><button type="button" className="btn btn-sm btn-secondary" onMouseDown={event => event.preventDefault()} disabled={state.status === 'stopping'} onClick={active ? cancel : dismiss}>{active ? (zh ? '停止' : 'Stop') : (zh ? '关闭' : 'Close')}</button></div>
            <progress max={state.total} value={state.loaded} aria-label={zh ? '粘贴进度' : 'Paste progress'}/>
            <div dir="ltr">{Math.floor(state.loaded * 100 / state.total)}% · {pasteBytes(state.loaded)} / {pasteBytes(state.total)}</div>
            <div role={state.status === 'error' ? 'alert' : 'status'}>{pasteText(state, zh)}</div>
        </section>, this.root);
    }
    destroy(): void {
        window.removeEventListener('resize', this.measure);
        window.visualViewport?.removeEventListener('resize', this.measure);
        window.visualViewport?.removeEventListener('scroll', this.measure);
        render(null, this.root); this.root.remove();
    }
}
