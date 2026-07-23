import { test, expect } from "@playwright/test";
import { startConsole, type Console } from "../helpers/harness";

// Against the good fixture (a real hash-chained log with an anchor bound over it)
// the console must show the chain as intact and raise NO tamper banner. This is
// the counterpart to audit-tampered: together they prove the console reports the
// audit verdict, not a hardcoded state — the original TAMPERED banner was pure
// misconfiguration this pins against.
test.describe("audit — intact", () => {
  let con: Console;
  test.beforeAll(async () => {
    con = await startConsole("good");
  });
  test.afterAll(() => con?.stop());

  test("statusline shows chain intact, no tamper banner", async ({ page }) => {
    await page.goto(con.baseURL + "/");

    await expect(page.locator("#statusline .chain-ok")).toHaveText(/chain intact/);
    await expect(page.locator(".tamper-banner")).toHaveCount(0);
    await expect(page.locator("#statusline .chain-bad")).toHaveCount(0);
  });

  test("the /api/audit response is ok", async ({ request }) => {
    const res = await request.get(con.baseURL + "/api/audit");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
    expect(body.reason).toContain("chain intact");
  });
});
