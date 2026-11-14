import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {WebTTY, type ConnectionCloseEvent, type Terminal} from '../webtty';

const stops: (() => void)[] = [];
beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(0.5);
    Object.defineProperty(document, 'hidden', {configurable: true, value: false});
});
afterEach(() => {stops.splice(0).forEach(stop => stop()); vi.restoreAllMocks(); vi.useRealTimers();});

function fixture() {
    const sockets: ReturnType<typeof socket>[] = [];
    function socket() {
        let state: number = WebSocket.CONNECTING;
        let opened = () => {}, received = (_data: string) => {}, closed = (_event: ConnectionCloseEvent) => {};
        const value = {
            open() {}, send: vi.fn((_data: string) => true), close: vi.fn(() => {state = WebSocket.CLOSED; closed(new CloseEvent('close'));}),
            isOpen: () => state <= WebSocket.OPEN, readyState: () => state,
            onOpen: (callback: () => void) => {opened = callback;},
            onReceive: (callback: (data: string) => void) => {received = callback;},
            onClose: (callback: (event: ConnectionCloseEvent) => void) => {closed = callback;},
            transportOpen: () => {state = WebSocket.OPEN;},
            connected: () => {state = WebSocket.OPEN; opened();},
            receive: (data: string) => received(data),
            peerClose: (code = 1006, reason = '', extra = {}) => {state = WebSocket.CLOSED; closed(Object.assign(new CloseEvent('close', {code, reason}), extra));},
        };
        return value;
    }
    const term = {info: () => ({columns: 80, rows: 24}), output() {}, reset() {}, setPreferences() {},
        removeMessage() {}, showMessage: vi.fn(), onResize() {}, onInput() {}, deactivate() {},
    } as unknown as Terminal;
    const wt = new WebTTY(term, {create: () => {const next = socket(); sockets.push(next); return next;}}, '', '');
    stops.push(wt.open());
    return {wt, term, sockets};
}

it('does not probe a transport whose application initialization is still pending', () => {
    const {sockets} = fixture();
    sockets[0].transportOpen();
    window.dispatchEvent(new Event('pageshow'));
    window.dispatchEvent(new Event('online'));
    vi.advanceTimersByTime(4000);
    expect(sockets[0].close).not.toHaveBeenCalled();
    expect(sockets[0].send).not.toHaveBeenCalled();
    sockets[0].connected();
    expect(JSON.parse(sockets[0].send.mock.calls[0][0])).toMatchObject({ReplayVersion: 2});
});

it('opens the replacement only after committing the previous parser resume position', () => {
    const {term, sockets} = fixture();
    let parsed!: () => void;
    term.flushOutput = callback => {parsed = callback;};
    sockets[0].connected();
    sockets[0].receive('c' + JSON.stringify({sequence: 1, data: '1' + btoa('previous output')}));
    sockets[0].peerClose();
    vi.advanceTimersByTime(1000);
    expect(sockets).toHaveLength(1);
    parsed();
    expect(sockets).toHaveLength(2);
    sockets[1].connected();
    expect(JSON.parse(sockets[1].send.mock.calls[0][0])).toMatchObject({LastSequence: 1});
});

it('creates only one replacement when refresh is repeated during a parser drain', () => {
    const {wt, term, sockets} = fixture();
    let parsed!: () => void; term.flushOutput = callback => {parsed = callback;};
    sockets[0].connected(); sockets[0].receive('1' + btoa('pending output'));
    wt.reconnectNow(); wt.reconnectNow();
    expect(sockets).toHaveLength(1);
    parsed(); expect(sockets).toHaveLength(2);
});

it('never opens a queued replacement after the terminal is disposed', () => {
    const {wt, term, sockets} = fixture();
    let parsed!: () => void; term.flushOutput = callback => {parsed = callback;};
    sockets[0].connected(); sockets[0].receive('1' + btoa('pending output'));
    wt.reconnectNow(); stops.at(-1)!();
    parsed(); vi.advanceTimersByTime(60000); expect(sockets).toHaveLength(1);
});

it('keeps increasing backoff across sockets that open but immediately fail', () => {
    const {sockets} = fixture();
    sockets[0].connected(); sockets[0].peerClose();
    vi.advanceTimersByTime(500);
    sockets[1].connected(); sockets[1].peerClose();
    vi.advanceTimersByTime(999); expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1); expect(sockets).toHaveLength(3);
    sockets[2].connected(); sockets[2].peerClose();
    vi.advanceTimersByTime(1999); expect(sockets).toHaveLength(3);
    vi.advanceTimersByTime(1); expect(sockets).toHaveLength(4);
});

it('resets backoff only after a working stream has stayed connected', () => {
    const {sockets} = fixture();
    sockets[0].connected(); sockets[0].peerClose(); vi.advanceTimersByTime(500);
    sockets[1].connected(); sockets[1].receive('4{}'); vi.advanceTimersByTime(5000);
    sockets[1].peerClose(); vi.advanceTimersByTime(500);
    expect(sockets).toHaveLength(3);
});

it('keeps successful probe replies independent of a slow renderer', () => {
    const {term, sockets} = fixture();
    term.flushOutput = () => {};
    sockets[0].connected(); sockets[0].receive('1' + btoa('busy output'));
    window.dispatchEvent(new Event('pageshow'));
    sockets[0].receive('2'); vi.advanceTimersByTime(3000);
    expect(sockets[0].close).not.toHaveBeenCalled();
});

it('stops policy retries across lifecycle events and permits explicit refresh', () => {
    const term = {showMessage: vi.fn(), info: () => ({columns: 80, rows: 24})} as unknown as Terminal;
    const create = vi.fn(() => {throw new DOMException('blocked', 'SecurityError');});
    const wt = new WebTTY(term, {create}, '', ''); stops.push(wt.open());
    vi.advanceTimersByTime(60000); window.dispatchEvent(new Event('pageshow')); window.dispatchEvent(new Event('online'));
    expect(create).toHaveBeenCalledOnce(); expect(term.showMessage).toHaveBeenCalledWith(expect.stringContaining('security policy'), 0);
    term.deactivate = () => {};
    wt.reconnectNow(); expect(create).toHaveBeenCalledTimes(2);
});

it('honors permanent failures and rate-limit waiting without treating 1006 as policy denial', () => {
    const {wt, sockets} = fixture();
    sockets[0].connected(); sockets[0].peerClose(1008, 'WebSocket blocked by browser security policy', {retryable: false});
    vi.advanceTimersByTime(60000); window.dispatchEvent(new Event('online')); expect(sockets).toHaveLength(1);
    wt.reconnectNow(); sockets[1].connected(); sockets[1].peerClose(1013, 'Hub connection rate limit reached', {retryAfterMs: 60000});
    window.dispatchEvent(new Event('online')); window.dispatchEvent(new Event('pageshow'));
    vi.advanceTimersByTime(59999); expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1); expect(sockets).toHaveLength(3);
    sockets[2].connected(); sockets[2].peerClose(); vi.advanceTimersByTime(1000); expect(sockets).toHaveLength(4);
});

it('preserves a rate-limit deadline received while the page is hidden', () => {
    const {sockets} = fixture(); sockets[0].connected();
    Object.defineProperty(document, 'hidden', {configurable: true, value: true});
    sockets[0].peerClose(1013, 'Hub connection rate limit reached', {retryAfterMs: 60000});
    vi.advanceTimersByTime(10000);
    Object.defineProperty(document, 'hidden', {configurable: true, value: false});
    document.dispatchEvent(new Event('visibilitychange'));
    vi.advanceTimersByTime(49999); expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1); expect(sockets).toHaveLength(2);
});

it('renews an expired ticket once and stops repeated authentication failures', () => {
    const {sockets} = fixture();
    sockets[0].connected(); sockets[0].peerClose(1008, 'Authentication ticket expired or invalid');
    vi.advanceTimersByTime(500); expect(sockets).toHaveLength(2);
    sockets[1].connected(); sockets[1].peerClose(1008, 'Authentication ticket expired or invalid');
    vi.advanceTimersByTime(60000); window.dispatchEvent(new Event('pageshow')); expect(sockets).toHaveLength(2);
});
