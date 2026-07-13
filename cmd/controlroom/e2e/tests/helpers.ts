import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { demoURL } from "../playwright.config";

export type Snapshot = Record<string, any>;

export async function demoSnapshot(request: APIRequestContext): Promise<Snapshot> {
  const response = await request.get(`${demoURL}/api/v1/snapshot`);
  expect(response.ok()).toBeTruthy();
  return response.json();
}

export function copy<T>(value: T): T {
  return structuredClone(value);
}

export async function mockSnapshots(page: Page, mode: "demo" | "real", snapshots: Snapshot[]) {
  let reads = 0;
  const refreshes: Array<Record<string, string>> = [];
  await page.route("**/api/v1/refresh", async (route) => {
    const request = route.request();
    refreshes.push(request.postDataJSON());
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      json: { baseline_version: Math.max(0, Number(snapshots[0].version) - 1), status: "started" },
    });
  });
  await page.route("**/api/v1/snapshot", async (route) => {
    const snapshot = snapshots[Math.min(reads, snapshots.length - 1)];
    reads += 1;
    await route.fulfill({ status: 200, contentType: "application/json", json: snapshot });
  });
  return {
    reads: () => reads,
    refreshes: () => refreshes,
    assertRequests: () => {
      expect(refreshes.length).toBeGreaterThan(0);
      expect(refreshes[0]).toEqual({ mode, trigger: "manual" });
    },
  };
}

export async function waitForLoaded(page: Page, version?: number) {
  await expect(page.locator("#main-content")).toHaveAttribute("aria-busy", "false");
  if (version !== undefined) await expect(page.locator("#snapshot-version")).toHaveText(`Version ${version}`);
}
