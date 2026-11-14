import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {InitialPresentation} from '../../packages/terminal-core/presentation';
import type {TerminalStatePayload} from '../webtty';

let frames: Map<number, FrameRequestCallback>, sequence: number, cleanup: (() => void)[];
beforeEach(() => {
    frames = new Map(); sequence = 0; cleanup = []; vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {frames.set(++sequence, callback); return sequence;});
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
    Object.defineProperty(document, 'hidden', {value: false, configurable: true});
    vi.stubGlobal('matchMedia', () => ({matches: true}));
});
afterEach(() => {cleanup.forEach(fn => fn()); document.body.innerHTML = ''; vi.useRealTimers(); vi.unstubAllGlobals();});
const render = () => {const pending = [...frames.values()]; frames.clear(); pending.forEach(callback => callback(0));};
const state = (extra: Partial<TerminalStatePayload> = {}): TerminalStatePayload => ({activeCols: 60, activeRows: 20,
    policy: 'adaptive', leaderDevice: 'mobile', leaderClientId: 'phone', sourceClientId: 'phone',
    reason: 'client-connect', connectedClients: 2, resizeEnabled: true, ...extra});
function setup(writable = true) {
    const host = document.createElement('div'); document.body.append(host);
    Object.defineProperties(host, {clientHeight: {value: 600, configurable: true}, clientWidth: {value: 1200}});
    const align = vi.fn(), value = new InitialPresentation(host, writable, align);
    cleanup.push(() => value.dispose()); value.measured(150, 40);
    return {host, value, align};
}

it('waits for replay and confirmed desktop geometry before the first drawing', () => {
    const {host, value, align} = setup();
    value.replay(true); value.geometry(state()); value.outputParsed(); render();
    expect(host.dataset.terminalPresented).toBe('false');
    value.replay(false); render(); expect(host.dataset.terminalPresented).toBe('false');
    value.geometry(state({leaderDevice: 'desktop', leaderClientId: 'desktop', reason: 'size-control'}));
    render(); expect(host.dataset.terminalPresented).toBe('false'); // Election is not a resize acknowledgement.
    value.geometry(state({leaderDevice: 'desktop', leaderClientId: 'desktop', reason: 'visibility'}));
    render(); expect(host.dataset.terminalPresented).toBe('false'); // Other state notifications are not an acknowledgement either.
    value.geometry(state({activeCols: 150, activeRows: 40, leaderDevice: 'desktop', leaderClientId: 'desktop', sourceClientId: 'desktop', reason: 'client-resize'}));
    expect(align).toHaveBeenCalledOnce(); render(); expect(host.dataset.terminalPresented).toBe('true');
    value.replay(true); value.geometry(state()); render(); expect(host.dataset.terminalPresented).toBe('true');
});

it('presents mobile and read-only viewers after parsing without waiting to own geometry', () => {
    const observer = setup(false); observer.value.outputParsed(); render();
    expect(observer.host.dataset.terminalPresented).toBe('true');
    vi.stubGlobal('matchMedia', () => ({matches: false}));
    const mobile = setup(); mobile.value.geometry(state()); mobile.value.replay(false); render();
    expect(mobile.host.dataset.terminalPresented).toBe('true');
});

it('bounds compatibility waits and clears timers and frames on disposal', () => {
    const {host, value} = setup(); value.outputParsed(); render();
    expect(host.dataset.terminalPresented).toBe('false');
    vi.advanceTimersByTime(1500); render(); expect(host.dataset.terminalPresented).toBe('true');
    const pending = setup(); pending.value.outputParsed(); pending.value.dispose();
    vi.advanceTimersByTime(2000); render(); expect(pending.host.dataset.terminalPresented).toBe('false');
    expect(vi.getTimerCount()).toBe(0); expect(frames.size).toBe(0);
});

it('does not spend a hidden pane fallback or reveal while replay is in progress', () => {
    const {host, value} = setup(); value.outputParsed();
    Object.defineProperty(host, 'clientHeight', {value: 0, configurable: true});
    vi.advanceTimersByTime(1500); render(); expect(host.dataset.terminalPresented).toBe('false');
    Object.defineProperty(host, 'clientHeight', {value: 600, configurable: true});
    value.replay(true); value.measured(150, 40); render(); expect(host.dataset.terminalPresented).toBe('false');
    value.replay(false); render(); expect(host.dataset.terminalPresented).toBe('false');
    value.geometry(state({policy: 'fixed'})); render(); expect(host.dataset.terminalPresented).toBe('true');
});
