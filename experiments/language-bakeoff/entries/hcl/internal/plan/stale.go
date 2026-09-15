package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
)

// staleReasons is the task replay policy. A task runs when:
//  1. it has no successful receipt: never ran, last attempt failed, or the
//     last attempt was interrupted (started, no result recorded);
//  2. its evaluated configuration differs from the receipt's;
//  3. a block it references has a planned change, or that block's observed
//     state differs from the state the receipt recorded it ran against;
//  4. a declared input's content differs from the receipt's;
//  5. a declared output is missing or differs from what the last run wrote.
//
// Nothing else makes a result stale: not time, not the program's own
// binary, not undeclared files the command happens to read.
func staleReasons(n *intent.Node, v taskView, changing map[string]bool) []string {
	if r := receiptReason(v); r != "" {
		return []string{r}
	}
	var reasons []string
	reasons = append(reasons, configReasons(n.Attrs, v.last.Config)...)
	reasons = append(reasons, upstreamReasons(n.Deps, changing)...)
	reasons = append(reasons, upstreamFactReasons(v.upstream, v.last.Upstream, changing)...)
	reasons = append(reasons, digestReasons("input", v.inputs, v.last.Inputs)...)
	reasons = append(reasons, outputReasons(v.outputs, v.last.Outputs)...)
	return reasons
}

// StaleNow evaluates the replay policy against observations made now,
// without the plan-time projection in rule 3 (a plan cannot know what an
// upstream will contain after apply). Apply calls it once a task's upstream
// steps have run: a task planned only because an upstream would change is
// skipped when every upstream, input and output turned out to match its
// receipt. Rule 3's recorded half still applies, so an upstream that really
// changed always re-runs the task.
func StaleNow(n *intent.Node, in *intent.Intent, env Env) ([]string, error) {
	v, err := observeTask(n, in, env)
	if err != nil {
		return nil, err
	}
	return staleReasons(n, v, nil), nil
}

func receiptReason(v taskView) string {
	if !v.hasLast {
		return "never run"
	}
	switch v.last.Event {
	case evidence.Started:
		return fmt.Sprintf("run %d was interrupted (started, no result recorded); outcome unknown", v.last.Run)
	case evidence.Failed:
		return fmt.Sprintf("last attempt failed (run %d): %s", v.last.Run, v.last.Error)
	}
	return ""
}

func configReasons(now adapter.Values, then map[string]any) []string {
	keys := map[string]bool{}
	for k := range now {
		keys[k] = true
	}
	for k := range then {
		keys[k] = true
	}
	var reasons []string
	for _, k := range sortedKeys(keys) {
		a, b := canon(then[k]), canon(now[k])
		if a != b {
			reasons = append(reasons, fmt.Sprintf("config %s: %s -> %s", k, a, b))
		}
	}
	return reasons
}

func upstreamReasons(deps []string, changing map[string]bool) []string {
	var reasons []string
	for _, d := range deps {
		if changing[d] {
			reasons = append(reasons, "upstream "+d+" changes")
		}
	}
	return reasons
}

// upstreamFactReasons compares each referenced block's state now with the
// state the receipt recorded. An upstream this plan already changes is
// covered by upstreamReasons; the recorded comparison matters when the
// upstream converged without the task running (a crash, a skipped step).
func upstreamFactReasons(now, then map[string]string, changing map[string]bool) []string {
	var reasons []string
	for _, d := range sortedKeys(now) {
		if then[d] != now[d] && !changing[d] {
			reasons = append(reasons, fmt.Sprintf("upstream %s changed since last run (%s)", d, factChange(then[d], now[d])))
		}
	}
	return reasons
}

func factChange(then, now string) string {
	switch {
	case then == "":
		return "not recorded by the last run"
	case strings.HasPrefix(now, "outputs "):
		return "its outputs differ"
	}
	return Short(then) + " -> " + Short(now)
}

func digestReasons(label string, now, then map[string]string) []string {
	var reasons []string
	for _, p := range sortedKeys(now) {
		if then[p] != now[p] {
			reasons = append(reasons, fmt.Sprintf("%s %s changed since last run (%s -> %s)", label, p, Short(then[p]), Short(now[p])))
		}
	}
	return reasons
}

func outputReasons(now, then map[string]string) []string {
	var reasons []string
	for _, p := range sortedKeys(now) {
		switch {
		case now[p] == then[p]:
		case now[p] == adapter.Absent:
			reasons = append(reasons, "output "+p+" is missing")
		case then[p] == "":
			reasons = append(reasons, "output "+p+" was not written by the last run")
		default:
			reasons = append(reasons, "output "+p+" was modified after the last run")
		}
	}
	return reasons
}

// abandoned notes outputs the last successful run wrote that the task no
// longer declares. wb leaves them in place; they are simply no longer owned.
func abandoned(n *intent.Node, v taskView) []string {
	owned := folded(n.Owns)
	var notes []string
	for _, p := range sortedKeys(v.lastOK.Outputs) {
		if !owned[strings.ToLower(p)] {
			notes = append(notes, "note: "+p+" is no longer declared; left in place, no longer owned")
		}
	}
	return notes
}

// unrecorded notes declared outputs that already exist although no
// successful run of this task wrote them: running replaces a file wb did not
// (knowingly) produce, and the plan should say so.
func unrecorded(v taskView) []string {
	written := folded(sortedKeys(v.lastOK.Outputs))
	var notes []string
	for _, p := range sortedKeys(v.outputs) {
		if v.outputs[p] != adapter.Absent && !written[strings.ToLower(p)] {
			notes = append(notes, "note: replaces existing "+p+", which no earlier successful run of this task wrote")
		}
	}
	return notes
}

// folded indexes paths case-insensitively, matching the compiler's path
// identity.
func folded(paths []string) map[string]bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[strings.ToLower(p)] = true
	}
	return set
}

func canon(v any) string {
	if v == nil {
		return "(unset)"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Stale lists why a saved plan no longer describes the workspace. Empty
// means a fresh plan made now has the same source, steps and observations,
// so applying it is applying what was reviewed.
func Stale(saved, fresh *Plan) []string {
	var why []string
	if saved.SourceDigest != fresh.SourceDigest {
		why = append(why, fmt.Sprintf("source changed since plan (%s -> %s)", Short(saved.SourceDigest), Short(fresh.SourceDigest)))
	}
	freshBy := map[string]Step{}
	for _, s := range fresh.Steps {
		freshBy[s.Address] = s
	}
	savedBy := map[string]bool{}
	for _, s := range saved.Steps {
		savedBy[s.Address] = true
		f, ok := freshBy[s.Address]
		if !ok {
			why = append(why, s.Address+": no longer declared")
			continue
		}
		why = append(why, drift(s, f)...)
	}
	for _, f := range fresh.Steps {
		if !savedBy[f.Address] {
			why = append(why, f.Address+": declared since plan")
		}
	}
	return why
}

func drift(saved, fresh Step) []string {
	keys := map[string]bool{}
	for k := range saved.Observed {
		keys[k] = true
	}
	for k := range fresh.Observed {
		keys[k] = true
	}
	var why []string
	for _, k := range sortedKeys(keys) {
		a, b := saved.Observed[k], fresh.Observed[k]
		if a != b {
			why = append(why, fmt.Sprintf("%s: %s was %s at plan time, now %s", saved.Address, k, Short(a), Short(b)))
		}
	}
	if len(why) == 0 && saved.Action != fresh.Action {
		why = append(why, fmt.Sprintf("%s: planned %s, a fresh plan says %s", saved.Address, saved.Action, fresh.Action))
	}
	return why
}
