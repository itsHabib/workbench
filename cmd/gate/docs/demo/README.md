# Decision-trace demo

Static, offline viewer for a frozen `gate explain -json` document.

## View

Open `trace-view.html` in a browser (double-click or `file://` URL). No server or network required.

The embedded fixture shows the ceiling-park story: judgment passes, but the grant ceiling (`max_tier: T1`) refuses the reduced verdict tier (`T2`) and parks the run.

## Render a real run — one command

The gate binary embeds this page; `-html` swaps the demo fixture for the run's
own projection and writes a self-contained file (no server, no paste):

```bash
gate explain -state /path/to/state -run run_XXXXX -html          # writes gate-explain-<run>.html, prints the path
gate explain -state /path/to/state -run run_XXXXX -html -out t.html
```

## Freeze a run

From a gate state directory with a completed run:

```bash
gate explain -state /path/to/state -run run_XXXXX -json > docs/demo/fixture.json
```

Re-embed the frozen JSON in `trace-view.html` by updating the `EMBEDDED_FIXTURE` constant near the top of the script block (search for `EMBEDDED_FIXTURE`).

Alternatively, paste or drop any `explain -json` document anywhere on the page (or onto the drop zone).

## Manual check

1. Open `trace-view.html` offline (disable network in devtools).
2. Confirm the graph renders with parent edges and a highlighted terminal park node.
3. Paste JSON from another run and confirm it re-renders.
