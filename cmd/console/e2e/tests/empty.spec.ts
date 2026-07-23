import { test, expect } from "@playwright/test";
import { startConsole, type Console } from "../helpers/harness";

// An inbox with nothing parked must render a clean empty state — not an error,
// not a blank page. The empty fixture is a valid, anchored log holding only a
// grant, so the docket is genuinely empty while audit stays intact.
test.describe("empty inbox", () => {
  let con: Console;
  test.beforeAll(async () => {
    con = await startConsole("empty");
  });
  test.afterAll(() => con?.stop());

  test("renders the empty state, no parked runs, no error", async ({ page }) => {
    await page.goto(con.baseURL + "/");

    await expect(page.locator(".matter[data-run]")).toHaveCount(0);
    await expect(page.locator(".empty")).toContainText("Nothing awaits judgment");
    // The docket loaded — not an error card, and the chain is intact.
    await expect(page.locator(".error")).toHaveCount(0);
    await expect(page.locator("#statusline .chain-ok")).toHaveText(/chain intact/);
    await expect(page.locator(".section-label .count").first()).toHaveText("(0)");
  });
});
