**Status**: accepted
**Owner**: Workbench maintainers
**Date**: 2026-07-13
**Related**: dossier task `control-room-vertical-demo-ui` (id: `tsk_01KXDJYJYXATHZ5ACZB5DGHABT`); [`../spec.md`](../spec.md); Phase 2 PR #18 (`e04d61d024b8508847be10952e84a9ccc1b6cf49`)

# Build the Control Room vertical demo UI and browser story

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Demo command and HTTP seam | `cmd/controlroom/main.go`, `cmd/controlroom/internal/web/*.go` | ~230 | ~230 |
| Embedded application shell | `cmd/controlroom/internal/web/static/{index.html,app.js,styles.css}` | ~520 | ~520 |
| Go and browser-contract tests | `cmd/controlroom/**/*_test.go` | ~360 | ~180 |
| Command documentation | `cmd/controlroom/README.md` | ~60 | ~60 |
| **Total** | | **~1,170** | **~990** |

Band: **stretch** per the repository PR-sizing convention and within the accepted TDD's Phase 3 budget of 700–1000 weighted LOC. This is the intentionally serialized highest-risk visual slice.

## Goal

Prove that the locked Phase 2 model can tell the complete healthy-to-on-fire portfolio story through one dense but calm local surface. The implementation must be demo-useful in a real browser, responsive at laptop and narrow widths, honest about partial sources, and structurally reusable by the real adapters and publication coordinator without importing producer code or weakening the read-only boundary.

## Architecture and boundaries

Add a `main` package at `cmd/controlroom` and a command-private `cmd/controlroom/internal/web` package. `web` may import `internal/model` and accept a snapshot supplier; `main` may import `internal/demo` and `internal/web`. Neither package imports Ship, Dossier, GitHub, Tracelens, toolhealth, Tower, or their stores.

Production code remains standard-library-only: `net/http`, `embed`, `encoding/json`, `flag`, and normal synchronization primitives are allowed. The shell loads one JavaScript ES module (`/static/app.js`) and one stylesheet (`/static/styles.css`) as separate same-origin requests; both assets are embedded in the binary. Do not add React, Vite, npm, a CDN, inline script/style, a database, filesystem asset serving, or a generic UI framework.

Phase 3 owns the demo command, static presentation, and the minimum versioned HTTP seam needed to exercise it. Phase 5 will replace the synchronous demo publisher with real collection/coalescing and immutable-generation orchestration. Keep that seam narrow: the browser consumes a snapshot supplier and a refresh callback, not global demo state.

The `web` package owns these seams so Phase 5 can substitute its publisher without changing handlers or browser code:

```go
type SnapshotSupplier func() model.Snapshot

type RefreshRequest struct {
    Mode    string
    Trigger string
}

type RefreshReceipt struct {
    BaselineVersion uint64
    Status          string // started | joined
}

type RefreshFunc func(context.Context, RefreshRequest) (RefreshReceipt, error)
```

The Phase 3 demo callback synchronously republishes before returning `status = "started"`; Phase 5 may start or join background collection behind the same receipt contract. The handler obtains snapshots only through `SnapshotSupplier` and performs refresh work only through `RefreshFunc`.

## Command contract

Implement:

```text
controlroom serve --mode demo --addr 127.0.0.1:4317
controlroom snapshot --mode demo --json
```

Only `demo` is accepted in Phase 3. A missing/unknown subcommand, non-demo mode, malformed address, or non-loopback address is a usage error with a nonzero exit. `serve` listens on IPv4 loopback, prints the canonical `http://127.0.0.1:<port>` URL after binding (including the selected port for `:0`), and exits cleanly when the server returns. `snapshot` writes the same policy-applied `demo.Snapshot()` contract used by the browser, as indented JSON with no log prefix.

Keep command parsing behind a testable `run(args, stdout, stderr)`-style seam. Do not call `os.Exit` below `main`, open a browser automatically, discover sibling executables, or accept real-mode config yet.

## Demo HTTP seam

`main` wires an injected snapshot supplier and a tiny demo-only monotonic publisher into the server. The initial immutable demo snapshot is version 1. Each accepted refresh republishes the same fixed-clock story at the next version; it does not mutate producer state or read the wall clock.

Required routes:

| Route | Behavior |
|---|---|
| `GET /` | Embedded shell; set the CSRF cookie and security headers. |
| `GET /static/app.js` | Embedded ES module with an explicit JavaScript content type. |
| `GET /static/styles.css` | Embedded stylesheet with an explicit CSS content type. |
| `GET /api/v1/snapshot` | Latest full demo snapshot JSON. Presentation query parameters are rejected in Phase 3 rather than silently changing the story. |
| `POST /api/v1/refresh` | Validate the demo/manual JSON body and CSRF seam; synchronously bump the demo version; return `202` with `baseline_version` and `status: started`. |
| `GET /healthz` | Plain process-liveness response only. |

Every other path returns `404`; unsupported methods return `405` with `Allow`. HEAD behaves like GET without a body for the shell, static assets, snapshot, and health routes. Set `Cache-Control: no-store` for HTML/API and `nosniff` on every response. Do not expose arbitrary files or a generic static prefix.

Implement the accepted shell security posture now because the browser contract depends on it:

- reject any HTTP `Host` other than the bound `127.0.0.1:<port>`;
- set `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`;
- create a process-random 256-bit `controlroom_csrf` cookie with `SameSite=Strict; Path=/` and no `HttpOnly`;
- validate POSTs in this order: exact JSON media type, exact loopback `Origin`, matching cookie/header using constant-time comparison, then body `{"mode":"demo","trigger":"manual"}`; return `403` at the first failure and do not read or validate later layers;
- emit no CORS headers and execute no callback on a rejected request.

Tests inject the token source. Runtime token generation failure is fatal at construction. Phase 6 will adversarially expand this coverage; Phase 3 must not knowingly introduce an insecure temporary route.

The fixed cookie name is the accepted v1 contract and cookies are host-scoped rather than port-scoped. Manual/browser validation therefore runs one Control Room server per browser profile; concurrent `:0` instances use separate temporary profiles or private contexts. Go handler tests remain independently cookie-scoped through separate clients.

## Application shell

Use semantic HTML and progressive rendering. The page must remain understandable before data arrives and must never interpolate producer strings through `innerHTML`; create nodes and assign `textContent`. External HTTPS links use `target="_blank"` plus `rel="noreferrer noopener"`. Repository-relative paths are copyable text only in this phase; no `file://` or `vscode://` links.

### Visual hierarchy

The shell has four persistent regions:

1. A compact masthead: product name, `DEMO` badge, generated time/version, source-health summary, refresh button, and live-region refresh status.
2. A filter rail: repository, status/liveness, severity/category, and a clear-filters action. Filters are client-side presentation filters over the current immutable snapshot and never alter API queries.
3. A prioritized attention band: counts for urgent/actionable/waiting/informational and up to three current consequence items (urgent, actionable, then waiting) with score, policy label, reason, repository/project, and safe evidence links. Show every qualifying consequence item when fewer than three exist. When no urgent/actionable/waiting item exists, keep the count strip visible and render “Nothing urgent in this snapshot”; informational items remain discoverable in their source panels rather than being promoted into the consequence band.
4. A six-panel grid, in this order: Runs, Tasks, Pull requests, Reliability, Tool health, Sources.

At desktop width the grid uses two columns with attention spanning both. At widths below 760px it becomes one column; the masthead/filter controls wrap without horizontal page scrolling, and tabular rows must render as labeled row cards. Use a restrained dark observability palette, high-contrast type, tabular numerals for scores/times, and category color as a redundant accent. Status is conveyed by a text label first; no color-only or icon-only encoding is permitted. Honor `prefers-reduced-motion`.

### Panel content

- **Runs**: kind, ID, repository/project, producer status, derived liveness, phase, runtime/provider availability, age/duration, and failure summary. The healthy, stalled, and retry-loop rows must all be legible.
- **Tasks**: title/slug, project/phase, Dossier status, derived liveness, assignee, dependencies/blockers, and exact artifacts. Blocked-no-path and ready are visually distinct.
- **Pull requests**: repository/number/title, draft/state, branch, age, check rollup, review decision, unresolved threads, merge state, and `NextCondition`. Failed CI and review-needed must remain separate facts.
- **Reliability**: run, verdict/tier/dialect, finding count and highest visible severity, plus explicit unavailable token/cost/latency values. Never render unavailable telemetry as zero.
- **Tool health**: tool, worst severity, recurrence, last occurrence, pain lines, accumulated-friction label, and stale badge.
- **Sources**: every receipt with state, observation time, duration, sanitized code/message, plus a concise explanation of what remains usable.

Every derived liveness/attention label is visibly prefixed or described as “Control Room policy”; producer status remains separate.

### Drawers and interaction

Clicking a run or PR row opens a keyboard-accessible native `<dialog>` drawer through `showModal()`. Enter/Space activates the row action, Escape closes, focus returns to the opener, a visible close button is first in the dialog, and the background is not keyboard-reachable while modal. Rely on the native dialog focus trap; do not layer a custom focus trap on top of it.

The run drawer shows normalized run fields, requested/actual runtime availability, evidence links, failure facts, and any matching diagnosis/findings already present in the snapshot. It never invokes Tracelens. The PR drawer is client-side-only and renders the normalized PR, checks, review/merge facts, truncation state, next factual condition, and safe HTTPS link from the snapshot entity already in browser memory. Neither drawer makes an additional API call. Do not add hidden GETs or subprocess-backed detail routes in Phase 3.

On `DOMContentLoaded`, the shell renders loading state, reads the CSRF cookie, and immediately performs the `demo/manual` refresh POST before fetching or rendering panel data. Refresh disables the button, announces progress, POSTs with the cookie/header, then polls snapshot GET after 250ms and doubles the interval to 500ms, 1s, and a 2s cap until the version exceeds the returned baseline. Phase 3 marks the shell disconnected if no higher version arrives within 5s; Phase 5 replaces that demo bound with the accepted 35-second enrichment settle rule. A failed request leaves any previous snapshot visible, marks the shell disconnected, and offers retry.

Successful refresh rebuilds repository/status/severity filter options from values present in the new snapshot. A selected filter remains valid only when its exact value still appears in that dimension; an invalid value resets to `all` and the clear action remains available. A valid filter that happens to produce no cross-dimension matches is retained and renders filtered-empty. Refresh closes a drawer only if its exact entity no longer exists.

## Loading, empty, degraded, and disconnected behavior

Rendering is state-driven and reusable; do not special-case demo entity IDs in UI code.

- **Loading**: before the first snapshot resolves, render skeleton/placeholder copy in all six panels and `aria-busy="true"` on the main region.
- **Empty**: a current source with a known empty collection shows “No … in this snapshot,” not an error. A filter that removes every row shows a filtered-empty message and clear action.
- **Degraded/partial**: `degraded`, `stale`, or `unavailable` receipts keep healthy panels visible. The source panel and masthead name the affected source; retained informational rows carry a stale badge. Do not fabricate row-level failures for absent sources.
- **Disconnected**: network/JSON failure keeps the last successfully rendered snapshot, shows a persistent reconnect banner and exact safe retry action, and never clears panels to zero.
- **Unknown/unavailable fields**: render `Unknown` or `Unavailable` from `Availability.State`; never infer a useful zero, approval, success, or ready state.

The fixed demo already proves degraded Tracelens, stale retained tool-health data, and unavailable Tower alongside healthy core panels. Add renderer branches for loading, true-empty, filtered-empty, and disconnected states even though Phase 6 Playwright will supply the final intercepted fixtures.

## Determinism and accessibility

All displayed relative ages are computed from `snapshot.generated_at`, not `Date.now()`, so the fixed demo is screenshot-stable. Render absolute timestamps in UTC and use deterministic stable ordering from the snapshot; the UI may filter but must not re-rank attention.

Minimum accessibility contract:

- one `h1`, ordered heading levels, landmark regions, explicit labels for every control, and a “Skip to main content” link targeting `#main-content` as the first focusable element;
- visible `:focus-visible` treatment and full keyboard operation;
- `aria-live="polite"` for refresh/disconnect status, not for whole-panel churn;
- buttons remain buttons and row actions have accessible names;
- category badges include text, and text contrast meets WCAG AA for normal text.

## Validation

Add Go tests for:

- command parsing, demo-only rejection, loopback enforcement, `:0` canonical URL, and indented snapshot JSON;
- exact route/method matrix, HEAD behavior, content types, no-store/nosniff/CSP headers, Host rejection, and no directory traversal;
- CSRF cookie shape; missing/wrong origin, content type, cookie, header, body, and token; accepted refresh version bump; rejected requests do not call refresh;
- snapshot JSON encoding failures fail safely without leaking internals;
- embedded shell contains no inline script/style, references only embedded assets, exposes all six named panels, and includes required semantic/accessibility anchors;
- source strings containing HTML are encoded in JSON and never embedded into server-rendered markup;
- an automated source scan confirms `app.js` contains no `innerHTML`, `outerHTML`, or `insertAdjacentHTML` data-rendering escape hatch;
- deterministic demo command output still equals the Phase 2 contract.

Run:

```text
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
go build ./...
git diff --check
```

Then launch `serve --mode demo --addr 127.0.0.1:0` and validate in a real browser at desktop and 390px widths:

1. healthy/current run and source facts are legible;
2. retry-loop, stalled-active, failed-CI, blocked-no-path, and ready items tell a coherent five-minute story;
3. degraded/stale/unavailable sources do not blank healthy panels;
4. repository/status/severity filters and clear action work;
5. refresh advances version and visibly settles;
6. run and PR drawers are keyboard-operable and restore focus;
7. no console errors, horizontal page scroll, raw HTML injection, raw local absolute path, or unavailable telemetry rendered as zero.

Temporary screenshots may be attached to the implementation PR for review. Committed final screenshots, pinned Playwright, CI browser installation, and reproducible screenshot automation remain Phase 6.

## Non-goals

- Real source adapters, subprocess execution, Dossier MCP lifecycle, or source discovery.
- Real-mode configuration, background auto-refresh, overlapping-generation cancellation, two-lane enrichment, stale-payload cache, or `atomic.Pointer` publication.
- On-demand diagnosis POST/GET, PR detail GET, Tracelens invocation, report generation, or arbitrary run/PR lookups outside the current snapshot.
- Persistent storage, analytics, telemetry, authentication, remote binding, CORS, WebSocket, SSE, service installation, or automatic browser launch.
- `vscode://file/`, `file://`, arbitrary filesystem paths, workspace symlink resolution, or copy-to-clipboard APIs.
- Playwright/npm/Node files, committed final screenshots, runbook, demo script, fresh-checkout proof, or retrospective. Phase 6 owns Playwright-level adversarial security flows and `filepath`/symlink path-containment integration tests; Phase 3 still owns the Go `httptest` CSRF, Host, method, and CSP coverage listed above.

## Conflict scan and runtime

This is one serialized stream. It touches the new command/server/UI surface and consumes but does not change Phase 2's `internal/model`, `internal/policy`, or `internal/demo` contracts unless a separately justified compatibility fix is required. The only dependency signal is the completed Phase 2 task.

Suggested runtime: **local**. Browser validation needs the operator's desktop browser and the task has no external service dependency. If Ship cannot dispatch because the configured Cursor credential is unavailable, retain that failed run receipt and continue in the isolated worktree rather than changing scope or fabricating a successful driver run.
