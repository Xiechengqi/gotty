import { afterEach, expect, it } from "vitest";
import { createConfirmClose } from "../confirm-close";

const cleanups: (() => void)[] = [];
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); });
function guard(enabled = true) {
  const result = createConfirmClose(enabled);
  cleanups.push(result.dispose);
  return result;
}
function prevented() {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}
it("arms only while connected, including after a reconnect", () => {
  const close = guard();
  expect(prevented()).toBe(false);
  close.setConnected(true);
  close.setConnected(true);
  expect(prevented()).toBe(true);
  close.setConnected(false);
  expect(prevented()).toBe(false);
  close.setConnected(true);
  expect(prevented()).toBe(true);
  close.dispose();
  expect(prevented()).toBe(false);
});
it("honors the disabled option", () => {
  guard(false).setConnected(true);
  expect(prevented()).toBe(false);
});
