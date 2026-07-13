import { defineConfig } from "@playwright/test";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dirname, "../../..");
const demoURL = "http://127.0.0.1:43171";
const realURL = "http://127.0.0.1:43172";

function quoted(value: string): string {
  if (process.platform === "win32") return `"${value.replaceAll('"', '""')}"`;
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export default defineConfig({
  testDir: "./tests",
  outputDir: "./test-results",
  fullyParallel: false,
  workers: 1,
  timeout: 20_000,
  expect: { timeout: 7_000 },
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: demoURL,
    browserName: "chromium",
    colorScheme: "dark",
    reducedMotion: "reduce",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "go run ./cmd/controlroom serve --mode demo --addr 127.0.0.1:43171",
      cwd: repoRoot,
      url: `${demoURL}/healthz`,
      timeout: 120_000,
      reuseExistingServer: false,
    },
    {
      command: [
        "go run ./cmd/controlroom serve --mode real --addr 127.0.0.1:43172",
        `--workspace-root ${quoted(repoRoot)}`,
        `--dossier-corpus ${quoted(repoRoot)}`,
        "--github-scope repo:itsHabib/workbench",
        "--ship-executable __controlroom_missing_ship__",
        "--dossier-executable __controlroom_missing_dossier__",
        "--github-executable __controlroom_missing_gh__",
        "--tower-executable __controlroom_missing_tower__",
        "--tracelens-executable __controlroom_missing_tracelens__",
        "--toolhealth-executable __controlroom_missing_toolhealth__",
      ].join(" "),
      cwd: repoRoot,
      url: `${realURL}/healthz`,
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
  projects: [
    { name: "chromium-laptop", use: { viewport: { width: 1440, height: 900 } } },
    { name: "chromium-narrow", use: { viewport: { width: 390, height: 844 } } },
  ],
});

export { demoURL, realURL, repoRoot };
