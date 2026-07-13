import { mkdir } from "node:fs/promises";
import path from "node:path";
import { test } from "@playwright/test";
import { demoURL, repoRoot } from "../playwright.config";
import { copy, demoSnapshot, mockSnapshots, waitForLoaded, type Snapshot } from "./helpers";

const output = path.join(repoRoot, "docs/features/portfolio-control-room/screenshots");

test.beforeEach(({}, testInfo) => {
  test.skip(testInfo.project.name !== "chromium-laptop", "release screenshots use the canonical laptop viewport");
});

test("capture deterministic healthy, degraded, and on-fire states", async ({ browser, request }) => {
  await mkdir(output, { recursive: true });
  const seed = await demoSnapshot(request);
  const variants: Array<[string, Snapshot]> = [
    ["healthy", healthy(seed)],
    ["degraded", degraded(seed)],
    ["on-fire", onFire(seed)],
  ];

  for (const [name, snapshot] of variants) {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: "dark", reducedMotion: "reduce" });
    await mockSnapshots(page, "demo", [snapshot]);
    await page.goto(`${demoURL}/`);
    await waitForLoaded(page, snapshot.version);
    await page.screenshot({ path: path.join(output, `${name}.png`), fullPage: true });
    await page.close();
  }
});

function healthy(seed: Snapshot): Snapshot {
  const value = copy(seed);
  value.version = 101;
  value.attention = [];
  value.sources = value.sources.map((source: Record<string, any>) => ({ ...source, state: "ok", error_code: "", message: "" }));
  value.runs = value.runs.filter((run: Record<string, any>) => run.id === "wf_demo_live");
  value.tasks = value.tasks.filter((task: Record<string, any>) => task.status === "done" || task.slug === "ready-task");
  value.pull_requests = value.pull_requests.filter((pr: Record<string, any>) => pr.number === 43).map((pr: Record<string, any>) => ({
    ...pr, review_decision: "APPROVED", next_condition: "Merge when authorized",
  }));
  value.reliability = [];
  value.tool_health = value.tool_health.map((tool: Record<string, any>) => ({ ...tool, worst_severity: "none", stale: false, pain: [] }));
  return value;
}

function degraded(seed: Snapshot): Snapshot {
  const value = copy(seed);
  value.version = 102;
  value.attention = value.attention.filter((item: Record<string, any>) => item.category === "informational");
  value.runs = value.runs.filter((run: Record<string, any>) => ["wf_demo_live", "drv_demo_waiting"].includes(run.id));
  return value;
}

function onFire(seed: Snapshot): Snapshot {
  const value = copy(seed);
  value.version = 103;
  return value;
}
