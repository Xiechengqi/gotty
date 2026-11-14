import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {TouchScroll} from '../../packages/terminal-core/touch-scroll';

let at: number, frames: Map<number, FrameRequestCallback>, sequence: number;
const instances: TouchScroll[] = [];
beforeEach(() => {
    at = 0; sequence = 0; frames = new Map();
    vi.spyOn(performance, 'now').mockImplementation(() => at);
    vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => {frames.set(++sequence, fn); return sequence;});
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
});
afterEach(() => {instances.splice(0).forEach(instance => instance.dispose()); document.body.innerHTML = ''; vi.restoreAllMocks(); vi.unstubAllGlobals();});
function frame() {at += 16; const pending = [...frames.values()]; frames.clear(); pending.forEach(fn => fn(at));}
function event(host: HTMLElement, type: string, y: number, count = 1) {
    at += 16;
    const point = {identifier: 1, clientX: 100, clientY: y};
    const e = new Event(type, {bubbles: true, cancelable: true});
    Object.defineProperties(e, {touches: {value: type === 'touchend' ? [] : Array.from({length: count}, (_, i) => ({...point, identifier: i + 1}))},
        changedTouches: {value: [point]}});
    host.dispatchEvent(e);
    return e;
}
function setup() {
    const host = document.createElement('div'); document.body.append(host);
    const target = {enabled: () => true, start: vi.fn(), intent: vi.fn(), cancel: vi.fn(), end: vi.fn(),
        scroll: vi.fn(() => true), momentum: () => true};
    const scroll = new TouchScroll(host, target); instances.push(scroll);
    return {host, target, scroll};
}

it('coalesces dragging and stops inertial history scrolling at a boundary or a new touch', () => {
    const {host, target} = setup();
    event(host, 'touchstart', 100); event(host, 'touchmove', 140); event(host, 'touchmove', 160);
    expect(target.scroll).not.toHaveBeenCalled(); frame();
    expect(target.scroll).toHaveBeenLastCalledWith(-60, {x: 100, y: 160});
    event(host, 'touchend', 160); frame(); expect(target.scroll.mock.calls.length).toBe(2);
    target.scroll.mockReturnValue(false); frame(); expect(frames.size).toBe(0);
    target.scroll.mockReturnValue(true);
    event(host, 'touchstart', 100); event(host, 'touchmove', 140); event(host, 'touchend', 140);
    expect(frames.size).toBeGreaterThan(0);
    event(host, 'touchstart', 200); expect(frames.size).toBe(0);
});

it('drops queued movement when a pinch starts or the controller is disposed', () => {
    const {host, target, scroll} = setup();
    event(host, 'touchstart', 100); event(host, 'touchmove', 140);
    event(host, 'touchstart', 140, 2); frame(); expect(target.scroll).not.toHaveBeenCalled();
    expect(target.cancel).toHaveBeenCalledOnce();
    expect(event(host, 'touchmove', 200, 2).defaultPrevented).toBe(false);
    event(host, 'touchstart', 100); event(host, 'touchmove', 140); scroll.dispose(); frame();
    expect(target.scroll).not.toHaveBeenCalled(); expect(frames.size).toBe(0);
    event(host, 'touchstart', 100); event(host, 'touchmove', 140); frame();
    expect(target.scroll).not.toHaveBeenCalled();
});
