import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { render } from "preact";
import type { Terminal } from "@xterm/xterm";
import { InputSender } from "../../packages/terminal-core/paste";
import { ZModemAddon } from "../zmodem";

vi.mock("bootstrap", () => ({
  Modal: { getOrCreateInstance: () => ({ show() {}, hide() {} }) },
}));

const cleanup: (() => void)[] = [];
beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  cleanup.splice(0).forEach(dispose => dispose());
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function fixture() {
  const frames: string[] = [];
  const terminalOutput = vi.fn();
  const sender = new InputSender({
    send: frame => { frames.push(frame); return true; },
    buffered: () => 0,
    chunkSize: () => 12285,
    changed: () => {},
    message: () => {},
  });
  const addon = new ZModemAddon({
    toTerminal: terminalOutput,
    toServer: bytes => sender.input(bytes),
  });
  const terminal = { options: { disableStdin: false }, focus: vi.fn() };
  addon.activate(terminal as unknown as Terminal);
  cleanup.push(() => {
    sender.dispose();
    render(null, addon.elem);
    addon.elem.remove();
  });
  return { addon, terminal, terminalOutput, frames };
}

it("passes real Sentry terminal output as bytes without UTF-8 re-encoding", () => {
  const f = fixture();
  const bytes = new Uint8Array([0, 127, 128, 255, 13, 10]);
  f.addon.consume(bytes);
  expect(f.terminalOutput).toHaveBeenCalledWith(bytes);
  expect(f.terminalOutput.mock.calls[0][0]).toBeInstanceOf(Uint8Array);
});

it("sends the real sz handshake through InputSender without dropping array bytes", () => {
  const f = fixture();
  const warning = vi.spyOn(console, "warn").mockImplementation(() => {});
  // ZRQINIT from sz: the actual library replies with a number[] ZRINIT frame.
  f.addon.consume(new TextEncoder().encode("**\x18B00000000000000\r\n\x11"));
  expect(warning).not.toHaveBeenCalled();
  expect(f.frames).toHaveLength(1);
  expect(f.frames[0][0]).toBe("1");
  expect(atob(f.frames[0].slice(1))).toMatch(/^\*\*\x18B01/);
  expect(f.terminal.options.disableStdin).toBe(true);
});

it("sends the real rz cancel sequence and restores keyboard input", () => {
  const f = fixture();
  // ZRINIT from rz, with CANFDX, CANOVIO and CANFC32 set.
  f.addon.consume(new TextEncoder().encode("**\x18B0100000023be50\r\n\x11"));
  expect(f.terminal.options.disableStdin).toBe(true);
  expect(f.addon.elem.textContent).toContain("Send file");
  f.addon.elem.querySelector(".modal")!.dispatchEvent(new Event("hide.bs.modal"));
  expect(f.frames.map(frame => atob(frame.slice(1))).join("")).toBe("\x18".repeat(5) + "\x08".repeat(5));
  expect(f.terminal.options.disableStdin).toBe(false);
  expect(f.terminal.focus).toHaveBeenCalled();
});
