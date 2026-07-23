import { execFileSync, spawn, ChildProcess } from "node:child_process";
import { cpSync, mkdtempSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const e2eDir = join(here, "..");
const fixturesDir = join(e2eDir, "fixtures");
const binExt = process.platform === "win32" ? ".exe" : "";
const gateBin = join(e2eDir, ".bin", "gate" + binExt);
const consoleBin = join(e2eDir, ".bin", "console" + binExt);

export interface Console {
  baseURL: string;
  keyDir: string;
  stateDir: string;
  stop: () => void;
}

// startConsole materializes a fixture into a fresh temp gate-state dir, binds a
// real anchor over it by minting one grant through the real gate binary (an
// append rebinds the anchor at THIS dir's absolute path, so audit resolves it
// wherever the repo lives), then launches the console against it on an ephemeral
// loopback port.
//
// The anchor step is why the committed fixture is path-independent: the log ships
// unanchored, and the anchor — keyed and path-tagged — is (re)bound here at run
// time. For the tampered fixture the anchor is bound too, so a would-be "intact"
// render over its broken chain is a loud test failure, not a skipped check.
export function startConsole(fixture: string): Promise<Console> {
  const work = mkdtempSync(join(tmpdir(), `console-e2e-${fixture}-`));
  const stateDir = join(work, "state");
  const keyDir = join(work, "keys"); // sibling of state (gate requires key dir outside state)
  mkdirSync(stateDir, { recursive: true });
  mkdirSync(keyDir, { recursive: true });
  cpSync(join(fixturesDir, fixture, "state"), stateDir, { recursive: true });

  // Mint one grant: this append rebinds the keyed anchor over the whole prior log
  // at stateDir's real absolute path. Succeeds even over the tampered chain (the
  // append only reads the tail hash); audit still replays the mutation as broken.
  execFileSync(
    gateBin,
    [
      "grant",
      "-state",
      stateDir,
      "-key",
      keyDir,
      "-repo",
      "example/console-e2e",
      "-max-tier",
      "T1",
      "-ttl",
      "8760h",
    ],
    { stdio: "pipe" },
  );

  const child = spawn(
    consoleBin,
    ["serve", "-addr", "127.0.0.1:0", "-state", stateDir, "-gate", gateBin],
    {
      // GATE_KEY reaches gate so `audit` resolves the anchor key. GATE_STATE is
      // deliberately UNSET so `gate next` splices -state into the paste-ready
      // judge/explain commands (the regression guard the docket spec asserts).
      env: { ...process.env, GATE_KEY: keyDir, GATE_STATE: "" },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );

  return waitForAddr(child).then((baseURL) => ({
    baseURL,
    keyDir,
    stateDir,
    stop: () => {
      killTree(child);
      rmSync(work, { recursive: true, force: true });
    },
  }));
}

// waitForAddr resolves with the http base URL once the console prints its bound
// address ("console: http://127.0.0.1:PORT  (gate=... state=...)").
function waitForAddr(child: ChildProcess): Promise<string> {
  return new Promise((resolve, reject) => {
    let out = "";
    let done = false;
    const to = setTimeout(() => {
      if (done) return;
      done = true;
      reject(new Error(`console did not report its address in time; stdout so far:\n${out}`));
    }, 20_000);
    const onData = (buf: Buffer) => {
      out += buf.toString();
      const m = out.match(/http:\/\/(127\.0\.0\.1:\d+)/);
      if (!m || done) return;
      done = true;
      clearTimeout(to);
      resolve(`http://${m[1]}`);
    };
    child.stdout?.on("data", onData);
    child.stderr?.on("data", onData);
    child.on("exit", (code) => {
      if (done) return;
      done = true;
      clearTimeout(to);
      reject(new Error(`console exited early (code ${code}); output:\n${out}`));
    });
  });
}

function killTree(child: ChildProcess) {
  if (child.pid == null) return;
  if (process.platform === "win32") {
    try {
      execFileSync("taskkill", ["/pid", String(child.pid), "/T", "/F"], { stdio: "ignore" });
    } catch {
      /* already gone */
    }
    return;
  }
  try {
    child.kill("SIGKILL");
  } catch {
    /* already gone */
  }
}

// spawnConsoleExpectFail launches the console with the given args and resolves
// with its combined output + exit code — for asserting a refused bind. It never
// reaches a serving state, so there is nothing to tear down beyond the exit.
export function spawnConsoleExpectFail(args: string[]): Promise<{ code: number | null; out: string }> {
  return new Promise((resolve) => {
    const child = spawn(consoleBin, args, { stdio: ["ignore", "pipe", "pipe"] });
    let out = "";
    child.stdout?.on("data", (b: Buffer) => (out += b.toString()));
    child.stderr?.on("data", (b: Buffer) => (out += b.toString()));
    child.on("exit", (code) => resolve({ code, out }));
  });
}
