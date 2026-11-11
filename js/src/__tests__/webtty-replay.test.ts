import {afterEach, expect, it, vi} from 'vitest';
import {WebTTY, type Terminal, type Connection} from '../webtty';

const stops: (() => void)[] = [];
afterEach(() => {stops.splice(0).forEach(stop => stop()); vi.useRealTimers();});
function setup(scope = 'recovery-test') {
  vi.useFakeTimers();
  Object.defineProperty(document, 'hidden', {configurable: true, value: false});
  let input: (value: string | Uint8Array) => void = () => {};
  const drains: (() => void)[] = [], order: string[] = [];
  const terminal: Terminal = {
    info: () => ({columns: 80, rows: 24}),
    output: vi.fn((data, replay) => order.push(`${replay ? 'replay' : 'live'}:${new TextDecoder().decode(data)}`)),
    updateTerminalState: vi.fn(state => order.push(`size:${state.activeCols}`)),
    reset: vi.fn(), deactivate() {}, close() {},
    showMessage() {}, removeMessage() {}, setWindowTitle() {}, setPreferences() {}, onResize() {},
    onInput: callback => {input = callback;}, flushOutput: callback => drains.push(callback), setReplayMode: vi.fn(),
    onHistoryStatus: vi.fn(),
  };
  const sockets: {connection: Connection; send: ReturnType<typeof vi.fn>; receive: (data: string) => void}[] = [];
  const wt = new WebTTY(terminal, {create: () => {
    let open = () => {}, receive = (_data: string) => {}, close = (_event: CloseEvent) => {};
    let state: number = WebSocket.OPEN;
    const send = vi.fn();
    const connection: Connection = {open: () => open(), close: () => {state = WebSocket.CLOSED; close(new CloseEvent('close'));}, send, isOpen: () => state === WebSocket.OPEN, readyState: () => state,
      onOpen: callback => {open = callback;}, onReceive: callback => {receive = callback;}, onClose: callback => {close = callback;}};
    sockets.push({connection, send, receive: data => receive(data)});
    return connection;
  }}, '', '', scope);
  stops.push(wt.open());
  sockets[0].send.mockClear();
  return {wt, sockets, input: (value: string | Uint8Array) => input(value), receive: (value: string) => sockets.at(-1)!.receive(value),
    drain: () => {expect(drains.length).toBeGreaterThan(0); drains.shift()!();},
    drainAll: () => {while (drains.length) drains.shift()!();}, drains, terminal, order};
}
const begin = (mode = 'snapshot', sequence = 2, truncated = false) => 'a' + JSON.stringify({epoch: 'one', mode, fromOffset: 0, columns: 80, rows: 24, sequence, fromSequence: 2, truncated});
const end = (sequence = 2) => 'b' + JSON.stringify({endOffset: 10, sequence});
const output = (value: string) => '1' + btoa(value);
const event = (sequence: number, data: string) => 'c' + JSON.stringify({sequence, data});
const size = (cols: number) => '8' + JSON.stringify({activeCols: cols, activeRows: 24});
it('keeps live output behind replay parser completion and suppresses historical query replies', () => {
  const p = setup();
  p.receive(begin()); p.receive(output('\x1b[?1$p')); p.receive(end());
  p.receive(event(3, output('live')));
  p.input('\x1b[?1;2$y'); expect(p.sockets[0].send).not.toHaveBeenCalled();
  expect(p.terminal.output).toHaveBeenCalledOnce();
  p.drain(); // historical output has parsed; replay-end still drains.
  p.input('still blocked'); expect(p.sockets[0].send).not.toHaveBeenCalled();
  p.drain();
  expect(p.terminal.output).toHaveBeenLastCalledWith(expect.any(Uint8Array), false);
  p.input('\x1b[?1;2$y'); p.input('中文🙂');
  expect(p.sockets[0].send.mock.calls.map(([frame]) => new TextDecoder().decode(Uint8Array.from(atob(frame.slice(1)), c => c.charCodeAt(0))))).toEqual(['\x1b[?1;2$y', '中文🙂']);
});
it('orders geometry after preceding output and before following output', () => {
  const p = setup(); p.receive(begin()); p.receive(output('snapshot')); p.receive(end());
  p.receive(event(3, size(120))); p.receive(event(4, output('wide')));
  expect(p.order).toEqual(['size:80', 'replay:snapshot']);
  p.drain(); expect(p.order).toHaveLength(2);
  p.drain(); expect(p.order).toEqual(['size:80', 'replay:snapshot', 'size:120', 'live:wide']);
});
it('soft reconnect resumes only committed output and retains the renderer', () => {
  const p = setup(); p.receive(begin()); p.receive(output('history')); p.receive(end()); p.drainAll();
  p.receive(event(3, output('last packet'))); p.wt.reconnectNow();
  expect(p.sockets).toHaveLength(2); expect(p.sockets[1].send).not.toHaveBeenCalled();
  p.wt.takeSizeControl(); expect(p.sockets[1].send).not.toHaveBeenCalled();
  p.drain();
  const init = JSON.parse(p.sockets[1].send.mock.calls[0][0]);
  expect(init).toMatchObject({Epoch:'one',ReplayVersion:2,LastSequence:3});
  expect(p.terminal.reset).toHaveBeenCalledOnce();
  p.sockets[0].receive(output('stale')); expect(p.order.some(item => item.includes('stale'))).toBe(false);
});
it('falls back to a new checkpoint after an event gap or an interrupted checkpoint', () => {
  const p = setup(); p.receive(begin()); p.receive(output('history')); p.receive(end()); p.drainAll();
  p.receive(event(4, output('missing third packet')));
  expect(p.sockets).toHaveLength(2);
  expect(JSON.parse(p.sockets[1].send.mock.calls[0][0])).toMatchObject({LastSequence:0});
  expect(JSON.parse(p.sockets[1].send.mock.calls[0][0]).Epoch).toBeUndefined();
  p.receive(begin()); p.receive(output('partial snapshot')); p.wt.reconnectNow(); p.drainAll();
  expect(JSON.parse(p.sockets[2].send.mock.calls[0][0]).Epoch).toBeUndefined();
  expect(p.order.some(item => item.includes('missing third packet'))).toBe(false);
});
it('reports retained-history truncation after restoration has finished', () => {
  const p = setup(); p.receive(begin('snapshot',2,true)); p.receive(output('retained')); p.receive(end());
  expect(p.terminal.onHistoryStatus).not.toHaveBeenCalled();
  p.drainAll(); expect(p.terminal.onHistoryStatus).toHaveBeenCalledWith(true);
});
it('stores only a stable browser identity and scopes it to the terminal', () => {
  sessionStorage.clear();
  const a = setup('same'), b = setup('same'), c = setup('different');
  const key = (p: ReturnType<typeof setup>) => {p.wt.reconnectNow(); return JSON.parse(p.sockets.at(-1)!.send.mock.calls[0][0]).ClientKey;};
  expect(key(a)).toBe(key(b)); expect(key(a)).not.toBe(key(c));
  expect(Object.values(sessionStorage).every(value => /^[a-f0-9]{32}$/.test(value))).toBe(true);
});
