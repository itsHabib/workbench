import { test, expect } from "@playwright/test";
import http from "node:http";
import { startConsole, spawnConsoleExpectFail, type Console } from "../helpers/harness";

// The console is an unauthenticated on-machine instrument, so its whole security
// story is structural: loopback-only bind, Host-header pinning (DNS-rebinding
// guard), a strict CSP, and nosniff on every response. These hold on the wire,
// so this spec asserts them on real served responses, not on the source.

// rawGet issues a request with a caller-chosen Host header via node:http (the
// browser forbids overriding Host; node does not), returning status + headers.
function rawGet(
  base: string,
  path: string,
  host?: string,
): Promise<{ status: number; headers: http.IncomingHttpHeaders; body: string }> {
  const u = new URL(base);
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: u.hostname, port: u.port, path, method: "GET", headers: host ? { Host: host } : {} },
      (res) => {
        let body = "";
        res.on("data", (c) => (body += c));
        res.on("end", () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body }));
      },
    );
    req.on("error", reject);
    req.end();
  });
}

test.describe("security posture", () => {
  let con: Console;
  test.beforeAll(async () => {
    con = await startConsole("good");
  });
  test.afterAll(() => con?.stop());

  test("strict CSP + nosniff on the app page", async () => {
    const res = await rawGet(con.baseURL, "/");
    expect(res.status).toBe(200);
    expect(res.headers["x-content-type-options"]).toBe("nosniff");
    const csp = String(res.headers["content-security-policy"] ?? "");
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("base-uri 'none'");
    expect(csp).toContain("form-action 'none'");
    // No external origins may be pulled in.
    expect(csp).not.toContain("http://");
    expect(csp).not.toContain("https://");
  });

  test("nosniff on the JSON API too", async () => {
    const res = await rawGet(con.baseURL, "/api/audit");
    expect(res.status).toBe(200);
    expect(res.headers["x-content-type-options"]).toBe("nosniff");
  });

  test("a spoofed Host header is refused (DNS-rebinding guard)", async () => {
    const ok = await rawGet(con.baseURL, "/", undefined);
    expect(ok.status).toBe(200); // the real loopback Host is accepted

    const spoofed = await rawGet(con.baseURL, "/", "evil.example.com");
    expect(spoofed.status).toBe(403);
    expect(spoofed.body).toContain("forbidden host");
  });

  test("a non-loopback bind is refused at startup", async () => {
    const { code, out } = await spawnConsoleExpectFail([
      "serve",
      "-addr",
      "0.0.0.0:0",
      "-state",
      con.stateDir,
    ]);
    expect(code).not.toBe(0);
    expect(out).toContain("non-loopback");
  });
});
