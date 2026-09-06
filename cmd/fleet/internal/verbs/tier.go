package verbs

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// tierEvidence is what a tier must show before it merges. Domain prose, so it lives
// in tier.json (`evidence`, keyed "0".."3"), never here.
func tierEvidence(cfg fleet.Rec, tier int) []string {
	ev := fleet.Strs(fleet.M(cfg, "evidence"), string(rune('0'+tier)))
	if len(ev) == 0 {
		return []string{"tier.json has no `evidence` list for T" + string(rune('0'+tier)) + "; add one"}
	}
	return ev
}

func classify(p string, cfg fleet.Rec) []string {
	var cls []string
	for _, key := range []string{"non_runtime", "runtime", "critical", "wire"} {
		for _, rx := range fleet.Strs(cfg, key) {
			if re, err := regexp.Compile(rx); err == nil && re.MatchString(p) {
				cls = append(cls, key)
				break
			}
		}
	}
	if len(cls) == 1 && cls[0] == "non_runtime" {
		return []string{"non_runtime"}
	}
	has := func(k string) bool { return contains(cls, k) }
	if !has("runtime") && (has("critical") || has("wire")) {
		cls = append(cls, "runtime")
	}
	out := without(cls, "non_runtime")
	if len(out) == 0 {
		return nil
	}
	return out
}

func cmdTier(base string, asJSON bool) error {
	cfg := fleet.ReadJSON(fleet.Path("tier.json"))
	if cfg == nil {
		return refuse("fleet tier: ~/.fleet/tier.json missing (see tier.example.json)")
	}
	files, addedText, err := changedAgainst(base)
	if err != nil {
		return err
	}
	rows, unmatched := classifyAll(files, cfg)
	if len(unmatched) > 0 {
		return refuse("fleet tier: no rule matches %s — add it to tier.json; there is no default placement", strings.Join(unmatched, ", "))
	}
	runtime, critical, wire := hasClass(rows, "runtime"), hasClass(rows, "critical"), hasClass(rows, "wire")
	failmode := runtime && failmodeIn(cfg, addedText)
	tier := 0
	if runtime {
		tier = 1
	}
	if critical || wire || failmode {
		tier = 2
	}
	if critical && failmode {
		tier = 3
	}
	if asJSON {
		filesOut := map[string]any{}
		for _, r := range rows {
			filesOut[r.p] = r.cls
		}
		out := map[string]any{"tier": tier, "files": filesOut, "critical": critical, "wire": wire, "failmode": failmode,
			"evidence": tierEvidence(cfg, tier), "live_run_required": tier >= 2}
		b, _ := json.MarshalIndent(out, "", " ")
		say("%s", b)
		return nil
	}
	say("tier T%d  critical=%s wire=%s failmode=%s", tier, pyBool(critical), pyBool(wire), pyBool(failmode))
	for _, r := range rows {
		say("  %-60s %s", r.p, strings.Join(r.cls, ","))
	}
	say("evidence required:")
	for _, e := range tierEvidence(cfg, tier) {
		say("  - %s", e)
	}
	return nil
}

type tierRow struct {
	p   string
	cls []string
}

// changedAgainst is the paths changed since base and the text of every added line.
// -z: NUL-delimited, so a path with whitespace stays one path.
func changedAgainst(base string) ([]string, string, error) {
	raw, err := gitOut("diff", "--name-only", "-z", base+"...HEAD")
	if err != nil {
		return nil, "", err
	}
	var files []string
	for _, p := range strings.Split(raw, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	diff, err := gitOut("diff", "--unified=0", base+"...HEAD")
	if err != nil {
		return nil, "", err
	}
	var added []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			added = append(added, l)
		}
	}
	return files, strings.Join(added, "\n"), nil
}

// classifyAll is every path with its classes, and the paths no rule matches.
func classifyAll(files []string, cfg fleet.Rec) ([]tierRow, []string) {
	var rows []tierRow
	var unmatched []string
	for _, p := range files {
		cls := classify(p, cfg)
		if cls == nil {
			unmatched = append(unmatched, p)
			continue
		}
		rows = append(rows, tierRow{p, cls})
	}
	return rows, unmatched
}

func hasClass(rows []tierRow, class string) bool {
	for _, r := range rows {
		if contains(r.cls, class) {
			return true
		}
	}
	return false
}

// failmodeIn reports whether the added lines match the config's failmode pattern.
func failmodeIn(cfg fleet.Rec, addedText string) bool {
	pat := fleet.S(cfg, "failmode_diff")
	if pat == "" {
		pat = `$^`
	}
	re, err := regexp.Compile("(?m)" + pat)
	return err == nil && re.MatchString(addedText)
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
