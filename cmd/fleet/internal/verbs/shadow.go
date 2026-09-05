package verbs

// Shadow: the day's numbers from `fleet hook <harness> --shadow`, which ran beside
// the installed hook on every event, computed its verdict from the live store, and
// wrote one line of shadow.jsonl. The report is what a switch decision reads:
// how many events, how fast, what the shadow would have refused, and where the two
// hooks disagreed. A disagreement is observable in one direction — the shadow
// refused a tool call at PreToolUse and a PostToolUse for the same tool_use_id
// followed, so the installed hook allowed it. The other direction (installed hook
// refused, shadow allowed) leaves no PostToolUse and is not distinguishable here
// from a tool that errored; the installed hook's own refusals say so on stderr.

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func shadowRows(since float64) []fleet.Rec {
	b, err := os.ReadFile(fleet.Path("shadow.jsonl"))
	if err != nil {
		return nil
	}
	var rows []fleet.Rec
	for _, l := range strings.Split(string(b), "\n") {
		r := fleet.ReadJSONBytes([]byte(l))
		if r == nil || (since > 0 && fleet.F(r, "at") < since) {
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	i := int(p * float64(len(xs)-1))
	return xs[i]
}

// ShadowReport is the summary as data: totals, per-event counts, latency, the
// refusals the shadow would have issued, and the divergences it can prove.
func ShadowReport(since float64) map[string]any {
	rows := shadowRows(since)
	byEvent := map[string]int{}
	var ms []float64
	var denies []fleet.Rec
	posted := map[string]bool{}
	for _, r := range rows {
		byEvent[fleet.S(r, "event")]++
		ms = append(ms, fleet.F(r, "ms"))
		if fleet.S(r, "event") == "PostToolUse" && fleet.S(r, "tool_use_id") != "" {
			posted[fleet.S(r, "session")+"|"+fleet.S(r, "tool_use_id")] = true
		}
		if fleet.F(r, "code") != 0 {
			denies = append(denies, r)
		}
	}
	var divergences []fleet.Rec
	for _, d := range denies {
		if fleet.S(d, "event") == "PreToolUse" && posted[fleet.S(d, "session")+"|"+fleet.S(d, "tool_use_id")] {
			divergences = append(divergences, d)
		}
	}
	if denies == nil {
		denies = []fleet.Rec{}
	}
	if divergences == nil {
		divergences = []fleet.Rec{}
	}
	return map[string]any{"events": len(rows), "by_event": byEvent, "ms_p50": percentile(ms, 0.5), "ms_p95": percentile(ms, 0.95), "ms_max": percentile(ms, 1),
		"denies": denies, "divergences": divergences}
}

func cmdShadowReport(since float64, asJSON bool) error {
	rep := ShadowReport(since)
	if asJSON {
		say("%s", jsonIndent(rep))
		return nil
	}
	n := rep["events"].(int)
	if n == 0 {
		say("no shadow events recorded (is `fleet hook claude --shadow` wired beside the installed hook? `install.sh --shadow`)")
		return nil
	}
	by := rep["by_event"].(map[string]int)
	var parts []string
	for _, k := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		if by[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", by[k], k))
		}
	}
	say("shadow: %d events (%s); verdict in %.1fms p50, %.1fms p95, %.1fms max", n, strings.Join(parts, ", "), rep["ms_p50"], rep["ms_p95"], rep["ms_max"])
	denies := rep["denies"].([]fleet.Rec)
	div := rep["divergences"].([]fleet.Rec)
	say("would have refused: %d; proven divergences (shadow refused, installed hook allowed): %d", len(denies), len(div))
	for _, d := range div {
		say("  DIVERGENCE %s %s %s: %s", fleet.Short(fleet.S(d, "session")), fleet.S(d, "tool"), fleet.S(d, "tool_use_id"), cut(fleet.S(d, "reason"), 160))
	}
	for _, d := range denies {
		say("  refuse %s %s %s: %s", fleet.Short(fleet.S(d, "session")), fleet.S(d, "event"), fleet.S(d, "tool"), cut(fleet.S(d, "reason"), 160))
	}
	return nil
}
