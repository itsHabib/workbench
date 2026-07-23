import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { repoRoot, binDir, gateBin, consoleBin, writeGhShim } from "./paths";

// Build the gate + console binaries from source once, before any spec runs. A
// stale gate binary once made the docket error out — building fresh here is the
// point, not a convenience. Binaries land in .bin/ (gitignored); the harness
// references them by fixed path (see paths.ts, the single source of truth for
// the .bin/ layout).
export default function globalSetup() {
  mkdirSync(binDir, { recursive: true });
  const go = (out: string, pkg: string) =>
    execFileSync("go", ["build", "-o", out, pkg], {
      cwd: repoRoot,
      stdio: "inherit",
    });
  go(gateBin, "./cmd/gate");
  go(consoleBin, "./cmd/console");

  // Write the fake gh the harness shadows onto the console's PATH, so gate's
  // -live PR reconcile is a deterministic no-op regardless of ambient gh auth.
  writeGhShim();
}
