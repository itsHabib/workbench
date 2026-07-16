// This example is the old cmd/demo, folded in during the workbench migration:
// it runs tracelens on a built-in messy trajectory and prints the full trace
// plus the diagnostic verdict — no arguments, no files, no network. It
// exercises the real detector pipeline end-to-end; the output comment pins
// the rendered trace, report, and verdict byte-for-byte.
package tracelens_test

import (
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

// sample is a coding agent that flails: a flaky doc fetch retries into the
// ground, it re-reads the same file, then it thrashes an edit/re-test cycle
// that never changes the failing result. Every pathology below is detected
// from this trace, not hardcoded.
const sample = `
{"role":"assistant","thought":"TestSum fails; pull the docs first"}
{"tool":"fetch_docs","args":{"topic":"sum-api"},"ok":false,"error":"network timeout","cost_usd":0.01}
{"tool":"fetch_docs","args":{"topic":"sum-api"},"ok":false,"error":"network timeout","cost_usd":0.01}
{"tool":"fetch_docs","args":{"topic":"sum-api"},"ok":false,"error":"network timeout","cost_usd":0.01}
{"role":"assistant","thought":"forget the docs, just read the code"}
{"tool":"read_file","args":{"path":"sum.go"},"ok":true,"observation":"func Sum(a,b int) int { return a }","cost_usd":0.02}
{"tool":"read_file","args":{"path":"sum.go"},"ok":true,"observation":"func Sum(a,b int) int { return a }","cost_usd":0.02}
{"tool":"edit_file","args":{"path":"sum.go","patch":"nudge"},"ok":true,"observation":"applied","cost_usd":0.015}
{"tool":"run_tests","args":{"pkg":"./..."},"ok":false,"error":"TestSum: want 5 got 2","cost_usd":0.05}
{"tool":"edit_file","args":{"path":"sum.go","patch":"nudge"},"ok":true,"observation":"applied","cost_usd":0.015}
{"tool":"run_tests","args":{"pkg":"./..."},"ok":false,"error":"TestSum: want 5 got 2","cost_usd":0.05}
{"tool":"edit_file","args":{"path":"sum.go","patch":"nudge"},"ok":true,"observation":"applied","cost_usd":0.015}
{"tool":"run_tests","args":{"pkg":"./..."},"ok":false,"error":"TestSum: want 5 got 2","cost_usd":0.05}
`

func Example() {
	tr, err := tracelens.ParseJSONL(strings.NewReader(sample))
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}
	report := tracelens.Analyze(tr, tracelens.DefaultConfig())

	fmt.Println("tracelens demo — analyzing a built-in agent run")
	fmt.Println(strings.Repeat("─", 68))
	fmt.Print(tracelens.RenderTrace(tr))
	fmt.Println(strings.Repeat("─", 68))
	fmt.Print(tracelens.RenderReport(report))
	fmt.Println(strings.Repeat("─", 68))
	fmt.Printf("diagnosis: %s (%s) — %d findings, $%.4f of $%.4f wasted.\n",
		report.Decision, report.Tier, len(report.Findings), report.WastedUSD, report.TotalUSD)

	// Output:
	// tracelens demo — analyzing a built-in agent run
	// ────────────────────────────────────────────────────────────────────
	// TRACE  13 steps  $0.2650 total
	//   #0  · TestSum fails; pull the docs first
	//   #1  fetch_docs{"topic":"sum-api"}                  FAIL  $0.0100
	//   #2  fetch_docs{"topic":"sum-api"}                  FAIL  $0.0100
	//   #3  fetch_docs{"topic":"sum-api"}                  FAIL  $0.0100
	//   #4  · forget the docs, just read the code
	//   #5  read_file{"path":"sum.go"}                      ok   $0.0200
	//   #6  read_file{"path":"sum.go"}                      ok   $0.0200
	//   #7  edit_file{"patch":"nudge","path":"sum.go"}      ok   $0.0150
	//   #8  run_tests{"pkg":"./..."}                       FAIL  $0.0500
	//   #9  edit_file{"patch":"nudge","path":"sum.go"}      ok   $0.0150
	//   #10 run_tests{"pkg":"./..."}                       FAIL  $0.0500
	//   #11 edit_file{"patch":"nudge","path":"sum.go"}      ok   $0.0150
	//   #12 run_tests{"pkg":"./..."}                       FAIL  $0.0500
	// ────────────────────────────────────────────────────────────────────
	// VERDICT  BLOCK · T3
	//   retry storm: fetch_docs failed 3× in a row ("network timeout")
	//   13 steps · $0.2650 spent · $0.0800 wasted · 6 findings
	//
	// FINDINGS
	//   [CRITICAL] retry storm: fetch_docs failed 3× in a row ("network timeout")
	//              steps: 1,2,3
	//              fix:   cap retries on fetch_docs (e.g. max 2 with backoff) and surface "network timeout" instead of looping on the failure
	//   [CRITICAL] loop: edit_file{"patch":"nudge","path":"sum.g… → run_tests{"pkg":"./..."} repeated 3× (period 2) across 6 steps
	//              steps: 7,8,9,10,11,12
	//              fix:   cap iterations or add a cycle guard: edit_file{"patch":"nudge","path":"sum.g… → run_tests{"pkg":"./..."} advanced no state before repeating
	//   [WARN    ] redundant: edit_file{"patch":"nudge","path":"sum.go"} ran 3× for the same result ($0.0300 wasted)
	//              steps: 7,9,11
	//              fix:   memoize edit_file{"patch":"nudge","path":"sum.go"} by (tool,args) and reuse the cached result instead of recomputing 3×
	//   [WARN    ] redundant: read_file{"path":"sum.go"} ran 2× for the same result ($0.0200 wasted)
	//              steps: 5,6
	//              fix:   memoize read_file{"path":"sum.go"} by (tool,args) and reuse the cached result instead of recomputing 2×
	//   [WARN    ] stuck: no new state for the last 4 steps
	//              steps: 9,10,11,12
	//              fix:   inject a progress check or force a replan; the agent keeps revisiting states it has already seen
	//   [INFO    ] cost hotspot: run_tests = 57% of spend ($0.1500 of $0.2650)
	//              steps: 8,10,12
	//              fix:   run_tests dominates spend; try a cheaper model/tool, batch its calls, or cut how often it runs
	// ────────────────────────────────────────────────────────────────────
	// diagnosis: block (T3) — 6 findings, $0.0800 of $0.2650 wasted.
}
