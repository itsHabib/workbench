import { defineConfig, devices } from "@playwright/test";

// The console binds loopback-only and each spec launches its own console process
// on an ephemeral port against its own temp gate-state dir (see helpers/harness.ts),
// so the specs are parallel-safe. globalSetup builds the gate + console binaries
// once from the Go source, so a stale binary (the exact regression this suite
// guards) can never be silently reused.
export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: [["list"]],
  globalSetup: "./global-setup.ts",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    ...devices["Desktop Chrome"],
    trace: "retain-on-failure",
    // baseURL is set per-test from the console the harness launches.
  },
  // Desktop Chrome already applied via the global `use` above; the project only
  // needs a name.
  projects: [{ name: "chromium" }],
});
