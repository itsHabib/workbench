import { test, expect } from "@playwright/test";
import { startConsole, type Console } from "../helpers/harness";

// The docket must render the parked run — its subject, title, and escalation
// question — and its paste-ready judge/explain commands must carry -state. That
// last part is the regression guard: a console that forgot to thread the state
// dir prints commands the operator pastes and that then read the WRONG log.
test.describe("docket", () => {
  let con: Console;
  test.beforeAll(async () => {
    con = await startConsole("good");
  });
  test.afterAll(() => con?.stop());

  test("renders the parked run with subject, title, and question", async ({ page }) => {
    await page.goto(con.baseURL + "/");

    const matter = page.locator(".matter[data-run]");
    await expect(matter).toHaveCount(1);

    // Subject repo#number (rendered as the PR link).
    await expect(matter.locator(".pr-link")).toContainText("example/console-e2e#42");
    // Title from the run's evidence.
    await expect(matter.locator(".matter-title")).toContainText(
      "Add a retry budget to the dispatcher loop",
    );
    // The escalation question.
    await expect(matter.locator(".question")).toContainText(
      "confirm the budget caps at 5 attempts",
    );
    // Head sha surfaces on the run line.
    await expect(matter.locator(".rid")).toContainText("9f2c1a7bd4e6");

    // Section header counts the one parked run.
    await expect(page.locator(".section-label").first()).toContainText("awaiting judgment");
    await expect(page.locator(".section-label .count").first()).toHaveText("(1)");
  });

  test("judge and explain commands carry -state (regression guard)", async ({ page }) => {
    await page.goto(con.baseURL + "/");
    const cmds = page.locator(".matter[data-run] .cmd");
    await expect(cmds).toHaveCount(2);

    const texts = await cmds.allInnerTexts();
    // Every printed command must thread the state dir; a command that omits it
    // would read the wrong log when pasted. Fail loudly if any lacks -state.
    for (const t of texts) {
      expect(t, `command must include -state: ${t}`).toContain("-state");
    }
    // One is the judge command, one the explain command.
    expect(texts.some((t) => t.includes("gate judge") && t.includes("-state"))).toBeTruthy();
    expect(texts.some((t) => t.includes("gate explain") && t.includes("-state"))).toBeTruthy();
  });

  test("the grant ledger renders at least one grant", async ({ page }) => {
    await page.goto(con.baseURL + "/");
    await expect(page.locator(".grant").first()).toBeVisible();
    await expect(page.locator(".grant .meta").first()).toContainText("example/console-e2e");
  });
});
