import { expect, test } from "@playwright/test";
import { realURL } from "../playwright.config";
import { copy, demoSnapshot, mockSnapshots, waitForLoaded } from "./helpers";

test("loads the deterministic demo and reports qualified source freshness", async ({ page }) => {
  await page.goto("/");
  await waitForLoaded(page);

  await expect(page.locator("html")).toHaveAttribute("data-control-room-mode", "demo");
  await expect(page.getByRole("heading", { name: /Control Room DEMO/ })).toBeVisible();
  await expect(page.locator("#snapshot-time")).toHaveText("Generated 2026-07-13T12:00:00Z");
  await expect(page.locator("#source-summary")).toHaveText("6 sources · 3 qualifications");
  await expect(page.locator("#sources")).toContainText("Retained rows remain visible with stale qualification");
  await expect(page.locator("#sources")).toContainText("Other source panels remain usable");
  await expect(page.locator("#tasks .cell").filter({ hasText: "Control Room policy" }).locator(".status-blocked")).toHaveCount(1);
  await expect(page.locator("#tool-health .cell").filter({ hasText: "Freshness" }).locator(".status-stale")).toHaveCount(1);
  await expect(page.locator("#sources .cell").filter({ hasText: "State" }).locator(".status-stale")).toHaveCount(1);
});

test("manual demo refresh publishes exactly one newer version", async ({ page }) => {
  await page.goto("/");
  await waitForLoaded(page);
  const before = Number((await page.locator("#snapshot-version").textContent())?.replace("Version ", ""));

  await page.getByRole("button", { name: "Refresh snapshot" }).click();
  await expect(page.locator("#snapshot-version")).toHaveText(`Version ${before + 1}`);
  await expect(page.locator("#refresh-status")).toHaveText(`Snapshot version ${before + 1} loaded`);
});

test("filters and drill-downs preserve failed CI, review, reliability, and wait evidence", async ({ page }) => {
  await page.goto("/");
  await waitForLoaded(page);

  await page.locator("#filter-status").selectOption("waiting");
  await expect(page.locator("#runs > .row")).toHaveCount(1);
  await expect(page.locator("#runs")).toContainText("awaiting_judgment");
  await page.getByRole("button", { name: "Clear filters" }).click();

  await page.locator("#filter-severity").selectOption("high");
  await expect(page.locator("#reliability > .row")).toHaveCount(1);
  await expect(page.locator("#reliability")).toContainText("highest high");
  await page.getByRole("button", { name: "Clear filters" }).click();

  await page.getByRole("button", { name: "Open pull request example-repo number 42" }).click();
  await expect(page.locator("#drawer[open]")).toContainText("ci: FAILURE");
  await expect(page.locator("#drawer[open]")).toContainText("REVIEW_REQUIRED");
  await page.getByRole("button", { name: "Close details" }).click();

  await page.getByRole("button", { name: "Open run wf_demo_fail_3" }).click();
  await expect(page.locator("#drawer[open]")).toContainText("Current diagnosis");
  await expect(page.locator("#drawer[open]")).toContainText("high: Retry loop detected — three related failures");
  await expect(page.locator("#drawer[open]")).toContainText("Cost USD");
  await expect(page.locator("#drawer[open]")).toContainText("Unavailable");
  await page.getByRole("button", { name: "Close details" }).click();

  await page.getByRole("button", { name: "Open run drv_demo_waiting" }).click();
  await expect(page.locator("#drawer[open]")).toContainText("waiting");
  await expect(page.locator("#drawer[open]")).toContainText("Inspect the named wait boundary and its owner");
});

test("unattended run rows distinguish progress and terminal timeout without promising retry", async ({ page, request }) => {
  const snapshot = await demoSnapshot(request);
  snapshot.version = 2;
  snapshot.tasks.unshift({
    id: "tsk_stale_claim",
    slug: "stale-claim",
    title: "Stale unattended claim",
    project: "workbench",
    status: "claimed",
    liveness: "stale_claim",
    updated_at: "2026-06-28T12:00:00Z",
    created_at: "2026-06-27T12:00:00Z",
    dependencies: [], blockers: [], artifacts: [],
  });
  snapshot.runs.unshift(
    {
      id: "wf_progressing",
      kind: "workflow",
      repository: "example-repo",
      status: "running",
      phase: "implement",
      operator_state: "progressing",
      liveness: "live",
      updated_at: "2026-07-13T11:59:00Z",
      created_at: "2026-07-13T11:30:00Z",
      next_action: "Monitor for the next durable update",
      requested: {}, actual: {}, evidence: [],
    },
    {
      id: "wf_timed_out",
      kind: "workflow",
      repository: "example-repo",
      status: "timed_out",
      phase: "validate",
      operator_state: "failed",
      liveness: "idle",
      updated_at: "2026-07-13T11:55:00Z",
      created_at: "2026-07-13T11:00:00Z",
      failure: "deadline_exceeded",
      next_action: "Inspect failure evidence and ownership before deciding whether retry is safe",
      requested: {}, actual: {}, evidence: [{ label: "owner log", path: "ship://wf_timed_out" }],
    },
  );
  const mock = await mockSnapshots(page, "demo", [snapshot]);

  await page.goto("/");
  await waitForLoaded(page, 2);
  mock.assertRequests();
  await expect(page.locator("#tasks .cell").filter({ hasText: "Control Room policy" }).locator(".status-stale")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Open run wf_progressing" })).toContainText("Monitor for the next durable update");
  await expect(page.getByRole("button", { name: "Open run wf_timed_out" })).toContainText("deadline_exceeded");
  await page.getByRole("button", { name: "Open run wf_timed_out" }).click();
  await expect(page.locator("#drawer[open]")).toContainText("2026-07-13T11:55:00Z · 5m ago");
  await expect(page.locator("#drawer[open]")).toContainText("before deciding whether retry is safe");
  await expect(page.locator("#drawer[open]")).not.toContainText("Retry now");
});

test("real mode renders a core generation before independently settled diagnostics", async ({ page, request }) => {
  const seed = await demoSnapshot(request);
  const core = copy(seed);
  core.mode = "real";
  core.version = 1;
  core.reliability = seed.reliability;
  core.sources = core.sources.flatMap((source: Record<string, any>) => {
    const receipt = { ...source, state: ["tracelens", "toolhealth"].includes(source.source) ? "loading" : "ok", error_code: "", message: "" };
    return source.source === "tracelens" ? [receipt, { ...source, state: "stale", message: "retained prior diagnosis" }] : [receipt];
  });
  const settled = copy(core);
  settled.version = 2;
  settled.sources = settled.sources.map((source: Record<string, any>) => ({
    ...source,
    state: source.source === "tracelens" ? "degraded" : source.source === "toolhealth" ? "stale" : "ok",
  }));
  settled.sources = settled.sources.filter((source: Record<string, any>, index: number, values: Array<Record<string, any>>) =>
    values.findIndex((candidate) => candidate.source === source.source) === index,
  );
  settled.reliability = seed.reliability;
  const mock = await mockSnapshots(page, "real", [core, settled], 750);

  await page.goto(realURL);
  await expect(page.locator("#snapshot-version")).toHaveText("Version 1");
  await page.getByRole("button", { name: "Open run wf_demo_fail_3" }).click();
  await expect(page.locator("#drawer[open]")).toContainText("Stale retained diagnosis — do not treat as current generation");
  await waitForLoaded(page, 2);
  mock.assertRequests();
  expect(mock.reads()).toBeGreaterThanOrEqual(2);
  await expect(page.locator("html")).toHaveAttribute("data-control-room-mode", "real");
  await expect(page.getByRole("heading", { name: /Control Room REAL/ })).toBeVisible();
  await expect(page.locator("#sources")).not.toContainText("loading");
  await expect(page.locator("#drawer[open]")).toContainText("Current diagnosis");
  await expect(page.locator("#drawer[open]")).toContainText("high: Retry loop detected — three related failures");
});

test("failed initial refresh exposes a disconnected state without fabricated rows", async ({ page }) => {
  await page.route("**/api/v1/refresh", (route) => route.fulfill({ status: 503, body: "unavailable" }));
  await page.goto("/");

  await expect(page.locator("#reconnect")).toBeVisible();
  await expect(page.locator("#refresh-status")).toContainText("Refresh failed");
  await expect(page.locator("#main-content")).toHaveAttribute("aria-busy", "false");
  await expect(page.locator("#runs > .row")).toHaveCount(0);
});

test("layout uses two panel columns on laptop and one on the narrow viewport", async ({ page }, testInfo) => {
  await page.goto("/");
  await waitForLoaded(page);
  const columns = await page.locator(".panel-grid").evaluate((element) => getComputedStyle(element).gridTemplateColumns.split(" ").length);
  expect(columns).toBe(testInfo.project.name === "chromium-narrow" ? 1 : 2);
});
