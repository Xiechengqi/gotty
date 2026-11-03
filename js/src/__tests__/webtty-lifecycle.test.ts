import { afterEach, expect, it, vi } from "vitest";
import { WebTTY, Connection, Terminal } from "../webtty";
import { createConfirmClose } from "../confirm-close";

const cleanups: (() => void)[] = [];
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.useRealTimers(); });

it("disarms on disconnect, re-arms on reconnect, and ignores stale socket events", () => {
  vi.useFakeTimers();
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
  const sockets: ReturnType<typeof socket>[] = [];
  function socket() {
    let state: number = WebSocket.CONNECTING;
    let onOpen = () => {};
    let onClose = (_event: CloseEvent) => {};
    return {
      open: vi.fn(), send: vi.fn(),
      close: () => { state = WebSocket.CLOSED; onClose(new CloseEvent("close")); },
      isOpen: () => state === WebSocket.OPEN,
      readyState: () => state,
      onOpen: (cb: () => void) => { onOpen = cb; },
      onReceive: (_cb: (data: string) => void) => {},
      onClose: (cb: (event: CloseEvent) => void) => { onClose = cb; },
      connected: () => { state = WebSocket.OPEN; onOpen(); },
    } as Connection & { connected(): void };
  }
  const term = {
    info: () => ({ columns: 80, rows: 24 }),
    removeMessage() {}, showMessage() {}, onResize() {}, onInput() {}, deactivate() {},
  } as unknown as Terminal;
  const wt = new WebTTY(term, { create: () => { const next = socket(); sockets.push(next); return next; } }, "", "");
  const guard = createConfirmClose(true);
  wt.onConnect = () => guard.setConnected(true);
  wt.onDisconnect = () => guard.setConnected(false);
  const stopped = wt.open();
  cleanups.push(stopped, guard.dispose);
  const prevented = () => {
    const event = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  };
  sockets[0].connected();
  expect(prevented()).toBe(true);
  sockets[0].close();
  expect(prevented()).toBe(false);
  vi.advanceTimersByTime(1000);
  sockets[1].connected();
  expect(prevented()).toBe(true);
  sockets[0].close();
  expect(prevented()).toBe(true);
  stopped();
  expect(prevented()).toBe(false);
});
