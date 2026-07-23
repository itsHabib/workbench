// paths.ts — the single source of truth for where the built binaries and the
// gh shim live. global-setup.ts (which builds the binaries + writes the shim)
// and helpers/harness.ts (which launches them) both import from here, so the
// .bin/ layout is computed exactly once instead of recomputed per file.

import { mkdirSync, writeFileSync, chmodSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

export const here = dirname(fileURLToPath(import.meta.url));
export const repoRoot = join(here, "..", "..", "..");
export const binDir = join(here, ".bin");

const exe = process.platform === "win32" ? ".exe" : "";
export const gateBin = join(binDir, "gate" + exe);
export const consoleBin = join(binDir, "console" + exe);

// shimDir holds a FAKE `gh` that the harness prepends to the spawned console's
// PATH. The console shells `gate next -json -live`, and gate's -live reconcile
// runs `gh pr view <n> -R <repo>` against real GitHub for each parked run — so a
// machine with an authenticated `gh` (like CI or a dev box) would let external
// GitHub state overwrite the fixture title/head or drop the row, making the
// "deterministic" fixtures non-hermetic. The shim exits non-zero, so gate's
// lookup fails and the row stays visible as "unknown" with the fixture data
// intact — a deterministic no-op regardless of any ambient gh auth.
export const shimDir = join(binDir, "gh-shim");

// writeGhShim materializes the fake gh into shimDir (both a POSIX `gh` script and
// a Windows `gh.cmd` resolved via PATHEXT), so the shadow works cross-platform.
export function writeGhShim(): void {
  mkdirSync(shimDir, { recursive: true });

  const sh =
    "#!/bin/sh\n" +
    "# fake gh for the console e2e suite: gate's -live reconcile must be a\n" +
    "# deterministic no-op here. Exit non-zero so the PR lookup fails and the\n" +
    "# parked row stays visible as unknown with the fixture data intact.\n" +
    "exit 1\n";
  const ghPosix = join(shimDir, "gh");
  writeFileSync(ghPosix, sh);
  try {
    chmodSync(ghPosix, 0o755);
  } catch {
    /* chmod is a no-op / unsupported on Windows */
  }

  const cmd =
    "@echo off\r\n" +
    "rem fake gh for the console e2e suite (see paths.ts). Deterministic no-op.\r\n" +
    "exit /b 1\r\n";
  writeFileSync(join(shimDir, "gh.cmd"), cmd);
}
