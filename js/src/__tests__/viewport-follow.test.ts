import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {ViewportFollow} from '../../packages/terminal-core/viewport-follow';

let frames: Map<number, FrameRequestCallback>, sequence: number;
let cleanup: (() => void)[];
beforeEach(() => {
    frames = new Map(); sequence = 0; cleanup = [];
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {frames.set(++sequence, callback); return sequence;});
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
    vi.stubGlobal('ResizeObserver', class {observe() {} disconnect() {}});
});
afterEach(() => {cleanup.forEach(fn => fn()); document.body.innerHTML = ''; vi.unstubAllGlobals();});
const render = () => {const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(fn => fn(0));};
function touch(host: HTMLElement, type: string, x = 100, y = 100) {
    const event = new Event(type, {bubbles: true});
    Object.defineProperty(event, 'touches', {value: type === 'touchend' ? [] : [{clientX: x, clientY: y}]});
    host.dispatchEvent(event);
}

function setup() {
    const host = document.createElement('div'), screen = document.createElement('div');
    screen.className = 'xterm-screen'; host.append(screen); document.body.append(host);
    Object.defineProperties(host, {clientHeight: {value: 480, configurable: true}, clientWidth: {value: 390}});
    host.getBoundingClientRect = () => ({top: 0, left: 0, width: 390, height: 480}) as DOMRect;
    screen.getBoundingClientRect = () => ({top: -host.scrollTop, left: -host.scrollLeft, width: 1600, height: 1200}) as DOMRect;
    const events = new Map<string, Set<() => void>>();
    const emit = (name: string) => events.get(name)?.forEach(callback => callback());
    const on = (name: string) => (callback: () => void) => {
        if (!events.has(name)) events.set(name, new Set());
        events.get(name)!.add(callback);
        return {dispose: () => events.get(name)!.delete(callback)};
    };
    const active = {type: 'normal', cursorX: 0, cursorY: 59, baseY: 100, viewportY: 100};
    const csi = new Map<string, (params: (number | number[])[]) => boolean>();
    let selected = false;
    const term = {
        rows: 60, cols: 160, buffer: {active, onBufferChange: on('buffer')},
        onScroll: on('scroll'), onKey: on('key'), onCursorMove: on('cursor'),
        onWriteParsed: on('parsed'), onRender: on('render'), onSelectionChange: on('selection'),
        hasSelection: () => selected, clearSelection: () => {selected = false; emit('selection');},
        scrollToBottom: vi.fn(() => {active.viewportY = active.baseY; emit('scroll');}),
        scrollToLine: vi.fn((line: number) => {active.viewportY = line; emit('scroll');}),
        parser: {
            registerCsiHandler: (id: {final: string}, handler: (params: (number | number[])[]) => boolean) => {csi.set(id.final, handler); return {dispose() {}};},
            registerEscHandler: () => ({dispose() {}}),
        },
    };
    const follow = new ViewportFollow(term, host);
    cleanup.push(() => follow.dispose());
    const browse = (top = 300) => {
        host.dispatchEvent(new WheelEvent('wheel', {deltaY: -100}));
        host.scrollTop = top; host.dispatchEvent(new Event('scroll'));
    };
    return {term, active, host, emit, follow, browse, csi, select: () => {selected = true; emit('selection');}};
}

it('reveals the last shared-grid row and follows a cleared screen back to its actual cursor', () => {
    const {host, active, emit, term} = setup();
    render(); expect(host.scrollTop).toBe(720);
    active.cursorY = 0; emit('cursor'); emit('parsed'); emit('render');
    expect(frames.size).toBe(1); render();
    expect(host.scrollTop).toBe(0);
    expect([term.cols, term.rows]).toEqual([160, 60]);
});

it('aligns normal live output to the browser bottom even when the cursor was already visible', () => {
    const {host, active, emit} = setup(); render();
    active.cursorY = 40; emit('parsed'); render();
    expect(host.scrollTop).toBe(340);
    active.type = 'alternate'; active.cursorY = 35; emit('parsed'); render();
    expect(host.scrollTop).toBe(340); // Editors use minimal cursor revelation.
});

it('does not pause on a boundary touch and resumes after touch/inertial scrolling returns to live', () => {
    const {host, emit, active} = setup(); render();
    touch(host, 'touchstart'); touch(host, 'touchmove', 100, 40); touch(host, 'touchend');
    emit('parsed'); render(); expect(host.dataset.viewportFollowing).toBe('true');
    touch(host, 'touchstart'); touch(host, 'touchmove', 100, 180);
    host.scrollTop = 300; host.dispatchEvent(new Event('scroll')); touch(host, 'touchend');
    expect(host.dataset.viewportFollowing).toBe('false');
    // Inertia continues after touchend; returning to live restores following.
    host.scrollTop = 720; host.dispatchEvent(new Event('scroll'));
    expect(host.dataset.viewportFollowing).toBe('true');
    active.cursorY = 58; emit('parsed'); render(); expect(host.scrollTop).toBe(700);
});

it('does not interpret delayed resize/parser scroll events as history intent', () => {
    const {host, active, emit, follow} = setup(); render();
    touch(host, 'touchstart'); touch(host, 'touchmove', 100, 40); touch(host, 'touchend');
    follow.changeLayout(() => {});
    active.viewportY = 90; emit('scroll');
    host.scrollTop = 650; host.dispatchEvent(new Event('scroll'));
    expect(host.dataset.viewportFollowing).toBe('true');
    render(); render(); expect(active.viewportY).toBe(active.baseY);
    expect(host.scrollTop).toBe(720);
});

it('does not turn a hidden pane pointer release into history navigation', () => {
    const {host, emit} = setup(); render();
    touch(host, 'touchstart'); touch(host, 'touchmove', 100, 40);
    Object.defineProperty(host, 'clientHeight', {value: 0, configurable: true});
    document.dispatchEvent(new Event('pointerup')); touch(host, 'touchend'); render();
    expect(host.dataset.viewportFollowing).toBe('true');
    Object.defineProperty(host, 'clientHeight', {value: 480, configurable: true});
    emit('render'); render(); expect(host.scrollTop).toBe(720);
    expect(host.dataset.viewportFollowing).toBe('true');
});

it('uses visible content on initial hidden-cursor replay and publishes content-free diagnostics', () => {
    const {host, active, term, follow, csi} = setup();
    active.cursorY = 0;
    (active as any).getLine = (line: number) => ({translateToString: () => line === active.baseY + 50 ? 'private terminal output' : ''});
    csi.get('l')!([25]); render();
    expect(host.scrollTop).toBe(540);
    expect(follow.diagnostics()).toMatchObject({following: true, pauseReason: 'live', cursorVisible: false, buffer: 'normal', rows: 60, columns: 160});
    expect(host.dataset.viewportState).not.toContain('private terminal output');
    term.cols = 200; follow.changeLayout(() => {}); render();
    expect(host.scrollTop).toBe(540);
});

it('holds manual history/selection positions while output arrives and resumes on deliberate input', () => {
    const {host, active, emit, follow, browse, select} = setup(); render();
    browse(); emit('parsed'); render();
    expect(host.scrollTop).toBe(300); expect(host.dataset.viewportFollowing).toBe('false');
    select(); active.cursorX = 100; follow.userInput(); render();
    expect(host.dataset.viewportFollowing).toBe('true'); expect(host.scrollTop).toBe(720);
    expect(host.scrollLeft).toBe(620);
    select(); emit('cursor'); render(); expect(host.dataset.viewportFollowing).toBe('false');
});

it('preserves both scroll layers across snapshot replay, layout changes and tab rendering', () => {
    const {host, active, emit, follow, browse} = setup(); render();
    browse(); active.viewportY = 70; emit('scroll'); host.scrollLeft = 80;
    follow.setReplayMode(true); active.baseY = 0; active.viewportY = 0; emit('scroll');
    follow.reset(); render(); expect(host.scrollTop).toBe(300);
    active.baseY = 200; active.viewportY = 200; follow.setReplayMode(false);
    // WebTTY's post-replay scroll hook must not override intentional browsing.
    follow.layout(); render();
    expect(active.viewportY).toBe(170); expect(host.scrollTop).toBe(300); expect(host.scrollLeft).toBe(80);
    follow.changeLayout(() => {active.viewportY = active.baseY; emit('scroll');});
    emit('render'); render();
    expect(active.viewportY).toBe(170); expect(host.dataset.viewportFollowing).toBe('false');
});

it('resumes when the user scrolls back to the visible live cursor', () => {
    const {host, browse} = setup(); render(); browse();
    host.dispatchEvent(new WheelEvent('wheel', {deltaY: 100}));
    host.scrollTop = 720; host.dispatchEvent(new Event('scroll'));
    expect(host.dataset.viewportFollowing).toBe('true');
});

it('anchors buffer content even when only the outer host was scrolled up', () => {
    const {host, active, emit, browse} = setup(); render(); browse();
    expect(active.viewportY).toBe(100);
    // Xterm was at its live bottom, so it initially advances with output.
    active.baseY = 110; active.viewportY = 110; emit('scroll'); emit('parsed'); render();
    expect(active.viewportY).toBe(100); expect(host.scrollTop).toBe(300);
    expect(host.dataset.viewportFollowing).toBe('false');
});

it('tracks the same history line when the bounded scrollback buffer trims old lines', () => {
    const {term, host, active, emit, browse} = setup(); render();
    let marker: {line: number; dispose: ReturnType<typeof vi.fn>};
    (term as any).registerMarker = (offset: number) => marker = {line: active.baseY + active.cursorY + offset, dispose: vi.fn()};
    browse(); marker!.line -= 10; emit('scroll'); emit('parsed'); render();
    expect(active.viewportY).toBe(90); expect(host.scrollTop).toBe(300);
});

it('uses reflowed markers to retain content when the confirmed column count changes', () => {
    const {term, active, emit, follow, browse} = setup(); render();
    let marker: {line: number; dispose: ReturnType<typeof vi.fn>};
    (term as any).registerMarker = (offset: number) => marker = {line: active.baseY + active.cursorY + offset, dispose: vi.fn()};
    browse();
    follow.changeLayout(() => {active.baseY = 150; active.viewportY = 150; marker!.line = 120; emit('scroll');});
    expect(active.viewportY).toBe(120);
});

it('retains the last visible outer position during background layout updates to hidden tabs', () => {
    const {term, host, active, follow, browse} = setup(); render();
    (term as any).registerMarker = (offset: number) => ({line: active.baseY + active.cursorY + offset, dispose() {}});
    browse();let top=300;
    Object.defineProperty(host,'scrollTop',{get:()=>host.clientHeight?top:0,set:value=>{top=host.clientHeight?value:0;}});
    Object.defineProperty(host,'clientHeight',{value:0,configurable:true});
    follow.changeLayout(()=>{active.baseY=140;active.viewportY=140;});
    Object.defineProperty(host,'clientHeight',{value:480,configurable:true});follow.layout();render();
    expect(host.scrollTop).toBe(300);expect(active.viewportY).toBe(100);
});

it('does not pan horizontally for remote output or follow a hidden TUI redraw cursor', () => {
    const {host, active, emit, csi} = setup(); render();
    host.scrollLeft = 120; active.cursorX = 150; emit('parsed'); render();
    expect(host.scrollLeft).toBe(120);
    expect(csi.get('l')!([25])).toBe(false);
    active.cursorY = 0; emit('parsed'); render(); expect(host.scrollTop).toBe(720);
    expect(csi.get('h')!([25])).toBe(false);
    render(); expect(host.scrollTop).toBe(0);
});

it('leaves native pinch/pan alone and releases every event subscription/frame on disposal', () => {
    const {host, emit, follow, active} = setup(); render();
    const vv = {scale: 2}; vi.stubGlobal('visualViewport', vv);
    active.cursorY = 0; emit('parsed'); render(); expect(host.scrollTop).toBe(720);
    vv.scale = 1; follow.layout(); render(); expect(host.scrollTop).toBe(0);
    follow.layout(); expect(frames.size).toBe(1); follow.dispose();
    expect(frames.size).toBe(0); emit('parsed'); expect(frames.size).toBe(0);
});
