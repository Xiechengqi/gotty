import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/preact";
import { initThemePicker } from "../theme-picker";

vi.mock("../../packages/terminal-core/fonts", async importOriginal => {
  const actual = await importOriginal<typeof import("../../packages/terminal-core/fonts")>();
  return {...actual, ensureFont: vi.fn(async (id: string) => ({fontFamily: actual.fontById(id)!.value, cached: true}))};
});

// The picker probes for a font by measuring a sample against two different
// fallbacks. jsdom has no canvas, so emulate that rule: a font listed in
// `available` overrides both fallbacks (equal widths); anything else falls
// back to each separately (differing widths).
function mockCanvas(available: string[]) {
  const ctx = {
    font: "",
    measureText() {
      const font = this.font;
      const overridden = available.some((f) => font.includes(`'${f}'`));
      return { width: !overridden && font.includes("serif") ? 100 : 50 };
    },
  };
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as any);
}

function makeTerm() {
  return { options: { fontSize: 14, fontFamily: "monospace" } } as any;
}

// The refit is scheduled across animation frames; wait long enough for two
// frames whether the environment uses rAF or the setTimeout fallback.
const flushFrames = () => new Promise((resolve) => setTimeout(resolve, 60));

const THEMES = { default: { background: "#101010" }, nord: { background: "#2e3440" } };

function familyItem(name: string): HTMLElement {
  return [...document.querySelectorAll<HTMLElement>(".font-family-item")].find(
    (el) => el.querySelector(".font-family-preview")?.textContent === name,
  )!;
}

beforeEach(() => {
  document.body.innerHTML = "";
  document.head.querySelectorAll("style").forEach((s) => s.remove());
  localStorage.clear();
  vi.restoreAllMocks();
  (window as any).gotty_themes = THEMES;
});

describe("font family availability", () => {
  it("offers bundled fonts for download and disables unavailable system fonts", () => {
    mockCanvas(["DejaVu Sans Mono"]);
    const term = makeTerm();
    initThemePicker(term);

    expect(familyItem("DejaVu Sans Mono").classList.contains("unavailable")).toBe(false);
    expect(familyItem("JetBrains Mono").classList.contains("unavailable")).toBe(false);
    expect(familyItem("JetBrains Mono").textContent).toContain("download");
    const missing = familyItem("Monaco");
    expect(missing.classList.contains("unavailable")).toBe(true);
    expect(missing.textContent).toContain("unavailable on this device");

    fireEvent.click(missing);

    expect(term.options.fontFamily).toBe("monospace");
    expect(localStorage.getItem("gotty-font-family")).toBeNull();
  });

  it("applies an installed font and re-fits", async () => {
    mockCanvas(["DejaVu Sans Mono"]);
    const term = makeTerm();
    const refit = vi.fn();
    initThemePicker(term, { refit });

    fireEvent.click(familyItem("DejaVu Sans Mono"));
    await flushFrames();

    expect(term.options.fontFamily).toBe("'DejaVu Sans Mono', monospace");
    expect(localStorage.getItem("gotty-font-family")).toBe("'DejaVu Sans Mono', monospace");

    await flushFrames();
    expect(refit).toHaveBeenCalled();
  });

  it("treats the always-available generic keywords as selectable", () => {
    mockCanvas([]);
    const term = makeTerm();
    initThemePicker(term);

    expect(familyItem("monospace").classList.contains("unavailable")).toBe(false);
  });
});

describe("re-fit after a font change", () => {
  it("re-fits after a font size change", async () => {
    mockCanvas([]);
    const term = makeTerm();
    const refit = vi.fn();
    initThemePicker(term, { refit });

    const size24 = [...document.querySelectorAll<HTMLElement>(".size-btn")].find(
      (el) => el.textContent === "24",
    )!;
    fireEvent.click(size24);

    expect(term.options.fontSize).toBe(24);
    expect(localStorage.getItem("gotty-font-size")).toBe("24");

    await flushFrames();
    expect(refit).toHaveBeenCalled();
  });

  it("re-fits on load when a saved preference is restored", async () => {
    mockCanvas([]);
    localStorage.setItem("gotty-font-size", "20");
    const term = makeTerm();
    const refit = vi.fn();
    initThemePicker(term, { refit });

    expect(term.options.fontSize).toBe(20);

    await flushFrames();
    expect(refit).toHaveBeenCalled();
  });

  it("re-fits when the default web font becomes ready", async () => {
    mockCanvas([]);
    const refit = vi.fn();
    initThemePicker(makeTerm(), { refit });

    await flushFrames();
    expect(refit).toHaveBeenCalled();
  });

  it("preserves an explicit Source Code Pro preference after upgrading the default", async () => {
    localStorage.setItem("gotty-font-family", "'Source Code Pro', monospace");
    const term = makeTerm();
    initThemePicker(term);
    await flushFrames();
    expect(term.options.fontFamily).toBe("'Source Code Pro', monospace");
    expect(localStorage.getItem("gotty-font-family")).toBe("'Source Code Pro', monospace");
  });
});

describe("placement", () => {
  it("keeps the local toolbar placement", () => {
    mockCanvas([]);
    initThemePicker(makeTerm());

    const css = [...document.head.querySelectorAll("style")].map((s) => s.textContent).join("");
    expect(css).toContain("top: 42px");
    expect(css).toContain("right: 46px");
  });
});

it("restores preferences and refits without exposing controls to a read-only share", async () => {
  localStorage.setItem("gotty-font-size", "24");
  const term = makeTerm();
  const refit = vi.fn();
  initThemePicker(term, { controls: false, refit });
  expect(term.options.fontSize).toBe(24);
  expect(document.querySelector("#gotty-theme-btn")).toBeNull();
  await flushFrames();
  expect(refit).toHaveBeenCalled();
});
