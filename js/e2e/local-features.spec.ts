import { existsSync, readFileSync, mkdirSync, rmSync } from "node:fs";
import { expect, Page, test } from "@playwright/test";

const SCRATCH = "/tmp/gotty-local-e2e";
async function command(page: Page, text: string) {
  await page.keyboard.type(text);
  await page.keyboard.press("Enter");
}
async function open(page: Page) {
  mkdirSync(SCRATCH, { recursive: true });
  rmSync(`${SCRATCH}/ready`, { force: true });
  await page.goto("/");
  await page.locator(".xterm").click();
  await expect.poll(async () => {
    await command(page, `cd ${SCRATCH}; touch ready`);
    return existsSync(`${SCRATCH}/ready`);
  }).toBe(true);
}

test("keeps the local toolbar, voice entry and file upload working", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await open(page);
  await expect(page.locator("#gotty-theme-btn")).toBeVisible();
  await expect(page.locator(".restart-btn")).toBeVisible();
  await expect(page.locator(".gotty-voice-btn")).toBeVisible();
  const target = `${SCRATCH}/local-upload.txt`;
  rmSync(target, { force: true });
  await page.locator(".upload-btn").click();
  await page.setInputFiles('input[type="file"]', {
    name: "local-upload.txt", mimeType: "text/plain", buffer: Buffer.from("local upload preserved\n"),
  });
  await expect.poll(() => existsSync(target) ? readFileSync(target, "utf8") : "").toBe("local upload preserved\n");
  expect(errors).toEqual([]);
});

test("executes API commands after the frontend and compiler upgrade", async ({ page, request }) => {
  await open(page);
  // Let the user's last keystroke leave the API's idle exclusion window.
  await expect.poll(async () => {
    const response = await request.post("/api/v1/exec", {
      headers: { Authorization: "Bearer user:pass" },
      data: { command: `printf api-ok > ${SCRATCH}/api-result`, timeout: 5 },
    });
    return response.status();
  }, { timeout: 15_000 }).toBe(200);
  expect(readFileSync(`${SCRATCH}/api-result`, "utf8")).toBe("api-ok");
});

test("reconnects and accepts terminal input after a network interruption", async ({ page, context }) => {
  let dropConnection!: () => void;
  await page.routeWebSocket("**/ws", socket => {
    socket.connectToServer();
    dropConnection = () => socket.close({ code: 1012, reason: "test network interruption" });
  });
  await open(page);
  await context.setOffline(true);
  // Deliver the close explicitly: offline emulation alone may leave an
  // established WebSocket in CLOSING until the network timeout.
  dropConnection();
  await expect(page.locator(".xterm-overlay")).toContainText(/closed|reconnect/i);
  await context.setOffline(false);
  const target = `${SCRATCH}/reconnected`;
  rmSync(target, { force: true });
  await page.locator(".xterm").click();
  await expect.poll(async () => {
    await command(page, `echo recovered > ${target}`);
    return existsSync(target) ? readFileSync(target, "utf8").trim() : "";
  }, { timeout: 20_000 }).toBe("recovered");
});

test("hides writable controls for a read-only share even when mobile keys are enabled", async ({ page }) => {
  await page.route("**/config.js*", async route => {
    const response = await route.fetch();
    const body = await response.text();
    await route.fulfill({ response, body: body + "\nvar gotty_share_readonly = true; var gotty_mobile_keys = true;" });
  });
  await page.goto("/");
  await expect(page.locator(".xterm")).toBeVisible();
  for (const selector of ["#gotty-theme-btn", ".upload-btn", ".restart-btn", ".gotty-voice-btn", "#gotty-mobile-keys"]) {
    await expect(page.locator(selector)).toHaveCount(0);
  }
});
