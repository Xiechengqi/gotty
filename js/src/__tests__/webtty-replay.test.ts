import {afterEach, expect, it, vi} from 'vitest';
import {WebTTY, type Terminal, type Connection} from '../webtty';

const stops: (() => void)[] = [];
afterEach(() => {stops.splice(0).forEach(stop => stop()); vi.useRealTimers();});
function setup() {
  vi.useFakeTimers();
  Object.defineProperty(document, 'hidden', {configurable: true, value: false});
  let input: (value: string | Uint8Array) => void = () => {};
  let receive: (data: string) => void = () => {};
  let open = () => {};
  const drains: (() => void)[] = [];
  const send = vi.fn();
  const terminal: Terminal = {
    info: () => ({columns: 80, rows: 24}), output: vi.fn(), reset() {}, deactivate() {}, close() {},
    showMessage() {}, removeMessage() {}, setWindowTitle() {}, setPreferences() {}, onResize() {},
    onInput: callback => {input = callback;}, flushOutput: callback => drains.push(callback), setReplayMode: vi.fn(),
  };
  const connection: Connection = {open: () => open(), close() {}, send, isOpen: () => true, readyState: () => WebSocket.OPEN,
    onOpen: callback => {open = callback;}, onReceive: callback => {receive = callback;}, onClose() {}};
  stops.push(new WebTTY(terminal, {create: () => connection}, '', '').open());
  send.mockClear();
  return {input: (value: string | Uint8Array) => input(value), receive: (value: string) => receive(value), drains, send, terminal};
}
const begin = 'a' + JSON.stringify({epoch: 'one', mode: 'tail', fromOffset: 0});
const end = 'b' + JSON.stringify({endOffset: 10});
it('suppresses replay replies until the terminal parser drains, then allows live replies and UTF-8 input', () => {
  const p = setup();
  p.receive(begin);p.receive('1' + btoa('\x1b[?1$p'));p.receive(end);
  p.input('\x1b[?1;2$y');
  expect(p.send).not.toHaveBeenCalled();
  expect(p.terminal.output).toHaveBeenCalledWith(expect.any(Uint8Array), true);
  p.receive('1' + btoa('live'));
  expect(p.terminal.output).toHaveBeenLastCalledWith(expect.any(Uint8Array), false);
  p.drains.shift()!();
  p.input('\x1b[?1;2$y');p.input('中文🙂');
  expect(p.send.mock.calls.map(([frame]) => new TextDecoder().decode(Uint8Array.from(atob(frame.slice(1)), c => c.charCodeAt(0))))).toEqual(['\x1b[?1;2$y', '中文🙂']);
});
it('does not release a newer replay when an older parser drain finishes', () => {
  const p = setup();p.receive(begin);p.receive(end);p.receive(begin);p.receive(end);
  p.drains.shift()!();p.input('blocked');expect(p.send).not.toHaveBeenCalled();
  p.drains.shift()!();p.input('ready');expect(p.send).toHaveBeenCalledOnce();
});
