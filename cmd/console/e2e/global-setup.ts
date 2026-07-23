import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// Build the gate + console binaries from source once, before any spec runs. A
// stale gate binary once made the docket error out — building fresh here is the
// point, not a convenience. Binaries land in .bin/ (gitignored); the harness
// references them by fixed path.
export const here = dirname(fileURLToPath(import.meta.url));
export const repoRoot = join(here, "..", "..", "..");
export const binDir = join(here, ".bin");
const exe = process.platform === "win32" ? ".exe" : "";
export const gateBin = join(binDir, "gate" + exe);
export const consoleBin = join(binDir, "console" + exe);

export default function globalSetup() {
  mkdirSync(binDir, { recursive: true });
  const go = (out: string, pkg: string) =>
    execFileSync("go", ["build", "-o", out, pkg], {
      cwd: repoRoot,
      stdio: "inherit",
    });
  go(gateBin, "./cmd/gate");
  go(consoleBin, "./cmd/console");
}
