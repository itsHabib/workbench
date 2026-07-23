import { test, expect } from "@playwright/test";
import { startConsole, type Console } from "../helpers/harness";

// The tampered fixture is the good log with one escalation body mutated and its
// hash left stale; the harness still binds an anchor over it, so gate's chain
// replay reports a body-hash mismatch. The console MUST flip to a loud tampered
// state — the red banner + the CHAIN TAMPERED status. If it ever rendered
// "intact" over this broken chain, every assertion below fails, which is exactly
// the tooth this spec is here to keep: a silent audit is worse than none.
test.describe("audit — tampered", () => {
  let con: Console;
  test.beforeAll(async () => {
    con = await startConsole("tampered");
  });
  test.afterAll(() => con?.stop());

  test("statusline flips to CHAIN TAMPERED and the banner appears", async ({ page }) => {
    await page.goto(con.baseURL + "/");

    // The teeth: chain-bad must be present, chain-ok must be absent.
    await expect(page.locator("#statusline .chain-bad")).toHaveText(/CHAIN TAMPERED/);
    await expect(page.locator("#statusline .chain-ok")).toHaveCount(0);

    const banner = page.locator(".tamper-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toContainText("tampered");
    // "body hash mismatch" couples to gate's audit error wording (see
    // cmd/gate's chain-replay reason). If gate rephrases that reason, update
    // this assertion in lockstep.
    await expect(banner).toContainText("body hash mismatch");
  });

  test("the /api/audit response reports not-ok with a reason", async ({ request }) => {
    const res = await request.get(con.baseURL + "/api/audit");
    expect(res.ok()).toBeTruthy(); // console proxies gate's finding as a 200 body
    const body = await res.json();
    expect(body.ok).toBe(false);
    expect(body.reason).toContain("TAMPERED");
  });
});
