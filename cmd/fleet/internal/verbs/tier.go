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
	// -z: NUL-delimited, so a path with whitespace stays one path.
	raw, err := gitOut("diff", "--name-only", "-z", base+"...HEAD")
	if err != nil {
		return err
	}
	var files []string
	for _, p := range strings.Split(raw, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	diff, err := gitOut("diff", "--unified=0", base+"...HEAD")
	if err != nil {
		return err
	}
	var added []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			added = append(added, l)
		}
	}
	addedText := strings.Join(added, "\n")
	type row struct {
		p   string
		cls []string
	}
	var rows []row
	var unmatched []string
	for _, p := range files {
		cls := classify(p, cfg)
		if cls == nil {
			unmatched = append(unmatched, p)
			continue
		}
		rows = append(rows, row{p, cls})
	}
	if len(unmatched) > 0 {
		return refuse("fleet tier: no rule matches %s — add it to tier.json; there is no default placement", strings.Join(unmatched, ", "))
	}
	runtime, critical, wire := false, false, false
	for _, r := range rows {
		if contains(r.cls, "runtime") {
			runtime = true
		}
		if contains(r.cls, "critical") {
			critical = true
		}
		if contains(r.cls, "wire") {
			wire = true
		}
	}
	failmode := false
	if runtime {
		pat := fleet.S(cfg, "failmode_diff")
		if pat == "" {
			pat = `$^`
		}
		if re, err := regexp.Compile("(?m)" + pat); err == nil && re.MatchString(addedText) {
			failmode = true
		}
	}
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

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
