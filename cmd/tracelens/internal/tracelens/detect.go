package tracelens

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Detector inspects a trajectory and reports zero or more findings. Detectors
// are single-responsibility and swappable; Analyze composes them into a
// pipeline. Adding a new pathology means adding a Detector, nothing else.
type Detector interface {
	Detect(t Trajectory) []Finding
}

// RunFailureDetector surfaces a producer-declared terminal failure (codex
// turn.failed, claude result is_error) as a Critical finding. The producer's
// own verdict that the run failed must reach the gate even when every
// individual tool step succeeded.
type RunFailureDetector struct{}

// Detect implements Detector.
func (RunFailureDetector) Detect(t Trajectory) []Finding {
	if t.DeclaredFailure == "" {
		return nil
	}
	return []Finding{{
		Kind:     "run_failure",
		Severity: Critical,
		Summary:  fmt.Sprintf("producer declared the run failed: %s", t.DeclaredFailure),
		Repair:   "inspect the terminal failure event; the producer aborted this run regardless of per-step outcomes",
	}}
}

// LoopDetector finds a repeating cycle of tool-call signatures — the agent
// mechanically re-executing the same sequence (the classic non-terminating
// loop) — and reports the strongest tandem repeat.
type LoopDetector struct {
	MinRepeats int
	// KeepVolatileArgs disables the volatile-argument normalization pass;
	// the zero value keeps it on so a zero-value detector matches defaults.
	KeepVolatileArgs bool
}

// Detect implements Detector.
func (d LoopDetector) Detect(t Trajectory) []Finding {
	minRepeats := d.MinRepeats
	if minRepeats < 2 {
		minRepeats = 2
	}
	var exact []string
	var idx []int
	var tools []Step
	for _, s := range t.Steps {
		if !s.IsTool() {
			continue
		}
		exact = append(exact, s.callSig())
		idx = append(idx, s.Index)
		tools = append(tools, s)
	}
	if span, ok := bestTandem(exact, minRepeats); ok {
		return []Finding{loopFinding(span, exact, idx, "loop", "advanced no state before repeating")}
	}
	if d.KeepVolatileArgs {
		return nil
	}
	normalized := make([]string, len(tools))
	for i, step := range tools {
		normalized[i] = d.signature(step)
	}
	span, ok := bestTandem(normalized, minRepeats)
	if !ok || !semanticOutcomesRepeat(tools, span) {
		return nil
	}
	return []Finding{loopFinding(span, normalized, idx, "semantic loop", "repeated the same confirmed outcomes after volatile trace metadata was ignored")}
}

func loopFinding(span span, sigs []string, idx []int, prefix, reason string) Finding {
	steps := idx[span.start : span.start+span.period*span.repeats]
	cycle := shortCycle(sigs[span.start : span.start+span.period])
	return Finding{
		Kind:     "loop",
		Severity: Critical,
		Summary:  fmt.Sprintf("%s: %s repeated %d× (period %d) across %d steps", prefix, cycle, span.repeats, span.period, len(steps)),
		Steps:    steps,
		Repair:   fmt.Sprintf("cap iterations or add a cycle guard: %s %s", cycle, reason),
	}
}

// semanticOutcomesRepeat requires the same confirmed outcome at each cycle
// position. Argument similarity alone is insufficient to block a run: a
// timestamp cursor with changing observations is visible progress.
func semanticOutcomesRepeat(steps []Step, span span) bool {
	for offset := 0; offset < span.period; offset++ {
		first := steps[span.start+offset]
		if first.OK == nil {
			return false
		}
		want := first.obsSig()
		for repeat := 1; repeat < span.repeats; repeat++ {
			step := steps[span.start+offset+repeat*span.period]
			if step.OK == nil || step.obsSig() != want {
				return false
			}
		}
	}
	return true
}

func (d LoopDetector) signature(s Step) string {
	if d.KeepVolatileArgs || len(s.Args) == 0 {
		return s.callSig()
	}
	raw, err := json.Marshal(normalizeLoopValue(s.Args))
	if err != nil {
		return s.callSig()
	}
	return s.Tool + string(raw)
}

// volatileLoopKeys are producer metadata, not task progress. Deliberately
// absent: page numbers, offsets, paths, commands, and arbitrary IDs. Those can
// encode real forward movement and must remain exact.
var volatileLoopKeys = map[string]bool{
	"call_id": true, "request_id": true, "run_id": true, "session_id": true,
	"timestamp": true, "trace_id": true, "ts": true,
}

func normalizeLoopValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if volatileLoopKeys[strings.ToLower(key)] {
				out[key] = "__volatile__"
				continue
			}
			out[key] = normalizeLoopValue(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeLoopValue(child)
		}
		return out
	default:
		return value
	}
}

// RedundancyDetector finds successful tool calls that ran more than once with
// identical arguments AND an identical result — pure recomputation the agent
// could have cached. Failed repeats are left to RetryStormDetector so wasted
// cost is never double-counted.
type RedundancyDetector struct{}

// Detect implements Detector.
func (RedundancyDetector) Detect(t Trajectory) []Finding {
	type grp struct {
		idxs  []int
		cost  float64
		first float64
	}
	groups := map[string]*grp{}
	var order []string
	for _, s := range t.Steps {
		if !s.IsTool() || s.OK == nil || !*s.OK {
			continue
		}
		key := s.callSig() + "\x00" + s.obsSig()
		g := groups[key]
		if g == nil {
			g = &grp{first: s.CostUSD}
			groups[key] = g
			order = append(order, key)
		}
		g.idxs = append(g.idxs, s.Index)
		g.cost += s.CostUSD
	}
	sort.Strings(order)
	var out []Finding
	for _, key := range order {
		g := groups[key]
		if len(g.idxs) < 2 {
			continue
		}
		call := short(strings.SplitN(key, "\x00", 2)[0], 48)
		wasted := g.cost - g.first
		out = append(out, Finding{
			Kind:      "redundancy",
			Severity:  Warn,
			Summary:   fmt.Sprintf("redundant: %s ran %d× for the same result ($%.4f wasted)", call, len(g.idxs), wasted),
			Steps:     g.idxs,
			WastedUSD: wasted,
			Repair:    fmt.Sprintf("memoize %s by (tool,args) and reuse the cached result instead of recomputing %d×", call, len(g.idxs)),
		})
	}
	return out
}

// RetryStormDetector finds a run of consecutive failed calls to the same tool —
// the agent hammering a broken dependency instead of backing off or bailing.
type RetryStormDetector struct{ Threshold int }

// Detect implements Detector.
func (d RetryStormDetector) Detect(t Trajectory) []Finding {
	thr := d.Threshold
	if thr < 2 {
		thr = 2
	}
	var out []Finding
	tool, errMsg := "", ""
	var idxs []int
	var cost float64
	flush := func() {
		if len(idxs) >= thr {
			out = append(out, retryFinding(tool, idxs, cost, errMsg))
		}
		tool, errMsg, idxs, cost = "", "", nil, 0
	}
	for _, s := range t.Steps {
		if !s.IsTool() {
			continue
		}
		if s.Failed() && s.Tool == tool {
			idxs = append(idxs, s.Index)
			cost += s.CostUSD
			continue
		}
		flush()
		if s.Failed() {
			tool, errMsg, idxs, cost = s.Tool, s.Error, []int{s.Index}, s.CostUSD
		}
	}
	flush()
	return out
}

func retryFinding(tool string, idxs []int, cost float64, errMsg string) Finding {
	if errMsg == "" {
		errMsg = "no error text"
	}
	return Finding{
		Kind:      "retry_storm",
		Severity:  Critical,
		Summary:   fmt.Sprintf("retry storm: %s failed %d× in a row (%q)", tool, len(idxs), short(errMsg, 40)),
		Steps:     idxs,
		WastedUSD: cost,
		Repair:    fmt.Sprintf("cap retries on %s (e.g. max 2 with backoff) and surface %q instead of looping on the failure", tool, short(errMsg, 40)),
	}
}

// CostHotspotDetector attributes spend by tool and flags any tool that consumes
// at least Frac of the total — where the budget actually went.
type CostHotspotDetector struct{ Frac float64 }

// Detect implements Detector.
func (d CostHotspotDetector) Detect(t Trajectory) []Finding {
	total := t.TotalCost()
	if total <= 0 {
		return nil
	}
	frac := d.Frac
	if frac <= 0 {
		frac = 0.4
	}
	byTool := map[string]float64{}
	idxByTool := map[string][]int{}
	var order []string
	for _, s := range t.Steps {
		if !s.IsTool() || s.CostUSD == 0 {
			continue
		}
		if _, ok := byTool[s.Tool]; !ok {
			order = append(order, s.Tool)
		}
		byTool[s.Tool] += s.CostUSD
		idxByTool[s.Tool] = append(idxByTool[s.Tool], s.Index)
	}
	sort.Slice(order, func(i, j int) bool { return byTool[order[i]] > byTool[order[j]] })
	var out []Finding
	for _, tool := range order {
		share := byTool[tool] / total
		if share < frac {
			continue
		}
		out = append(out, Finding{
			Kind:     "cost_hotspot",
			Severity: Info,
			Summary:  fmt.Sprintf("cost hotspot: %s = %.0f%% of spend ($%.4f of $%.4f)", tool, share*100, byTool[tool], total),
			Steps:    idxByTool[tool],
			Repair:   fmt.Sprintf("%s dominates spend; try a cheaper model/tool, batch its calls, or cut how often it runs", tool),
		})
	}
	return out
}

// StuckDetector runs a progress model over the trajectory: a step makes progress
// when it reaches a state signature never seen before. A trailing run of steps
// that only revisit known states means the agent ended churning without gaining
// information — distinct from a loop, which needs an exact repeating cycle.
type StuckDetector struct{ Window int }

// Detect implements Detector.
func (d StuckDetector) Detect(t Trajectory) []Finding {
	win := d.Window
	if win < 2 {
		win = 2
	}
	seen := map[string]bool{}
	var idxs []int
	var progress []bool
	for _, s := range t.Steps {
		sig := s.stateSig()
		if sig == "" {
			continue
		}
		idxs = append(idxs, s.Index)
		progress = append(progress, !seen[sig])
		seen[sig] = true
	}
	run := trailingStall(progress)
	if run < win {
		return nil
	}
	return []Finding{{
		Kind:     "stuck",
		Severity: Warn,
		Summary:  fmt.Sprintf("stuck: no new state for the last %d steps", run),
		Steps:    idxs[len(idxs)-run:],
		Repair:   "inject a progress check or force a replan; the agent keeps revisiting states it has already seen",
	}}
}

// trailingStall counts consecutive non-progress steps at the tail of the run.
func trailingStall(progress []bool) int {
	run := 0
	for i := len(progress) - 1; i >= 0; i-- {
		if progress[i] {
			break
		}
		run++
	}
	return run
}

// span is a detected tandem repeat: repeats copies of a period-length pattern
// beginning at start (all in signature-sequence coordinates).
type span struct{ start, period, repeats int }

// bestTandem scans every period for the strongest tandem repeat (most repeats,
// then longest span, then earliest). It catches period-1 spins (A A A) and
// longer cycles (A B A B A B) alike. O(n^2) in the number of tool steps, which
// is negligible for real traces.
func bestTandem(seq []string, minRepeats int) (span, bool) {
	var best span
	found := false
	for p := 1; p <= len(seq)/minRepeats; p++ {
		i := 0
		for i < len(seq) {
			j := runEnd(seq, i, p)
			r := (j - i) / p
			if r >= minRepeats {
				if better(span{i, p, r}, best, found) {
					best, found = span{i, p, r}, true
				}
				// A confirmed run is maximal for this period from its
				// earliest start (no period-p run crosses j), so skipping to
				// its end is safe and avoids rescanning it.
				i = j
				continue
			}
			// No valid run here: advance by one, never skip to j. A stray
			// periodic match (seq[i+p] == seq[i]) can push j one past a real
			// run start sitting just inside (i, j) — jumping to j leaps over
			// it and undercounts the repeats. Cost stays low: unmatched scans
			// don't extend far.
			i++
		}
	}
	return best, found
}

// runEnd reports how far a period-p tandem match starting at i extends: the
// first index where seq stops repeating its value from one period back.
func runEnd(seq []string, i, p int) int {
	j := i + p
	for j < len(seq) && seq[j] == seq[j-p] {
		j++
	}
	return j
}

func better(a, b span, haveB bool) bool {
	if !haveB {
		return true
	}
	if a.repeats != b.repeats {
		return a.repeats > b.repeats
	}
	if a.period*a.repeats != b.period*b.repeats {
		return a.period*a.repeats > b.period*b.repeats
	}
	return a.start < b.start
}

func short(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func shortCycle(sigs []string) string {
	parts := make([]string, len(sigs))
	for i, s := range sigs {
		parts[i] = short(s, 40)
	}
	return strings.Join(parts, " → ")
}
