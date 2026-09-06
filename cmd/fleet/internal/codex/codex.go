// Package codex translates Codex hook events to the fleet hook contract.
//
// Codex's `apply_patch` carries several files in one call. Each becomes one Edit
// event, evaluated in order; a denial on a later path rolls back the leases the
// earlier paths took, through RestoreLease under the key's lock — the Python once
// re-read and wrote by pathname here, which is the defect the substrate spent three
// rounds deleting, on every multi-path patch denied on a later path.
//
// The Python adapter spawned hook.py per mapped event. Here each is Run in-process:
// the same verdicts, one process instead of N.
package codex

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

var patchPathRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$|^\*\*\* Move to: (.+)$`)

// mappedEvents is the event as the hook wants it: one per patched file for
// apply_patch, else the event itself.
func mappedEvents(ev fleet.Event) []fleet.Event {
	if fleet.S(ev, "tool_name") != "apply_patch" {
		return []fleet.Event{ev}
	}
	inp := fleet.M(ev, "tool_input")
	cmd := fleet.S(inp, "command")
	var paths []string
	seen := map[string]bool{}
	for _, m := range patchPathRe.FindAllStringSubmatch(cmd, -1) {
		p := strings.TrimSpace(m[1])
		if p == "" {
			p = strings.TrimSpace(m[2])
		}
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		cwd := fleet.S(ev, "cwd")
		if cwd == "" {
			cwd = "."
		}
		paths = []string{cwd}
	}
	var out []fleet.Event
	for _, p := range paths {
		mapped := fleet.Rec{}
		for k, v := range ev {
			mapped[k] = v
		}
		ti := fleet.Rec{}
		for k, v := range inp {
			ti[k] = v
		}
		if !filepath.IsAbs(p) {
			cwd := fleet.S(ev, "cwd")
			if cwd == "" {
				cwd = "."
			}
			p = filepath.Join(cwd, p)
		}
		ti["file_path"] = filepath.Clean(p)
		mapped["tool_name"] = "Edit"
		mapped["tool_input"] = ti
		out = append(out, mapped)
	}
	return out
}

// leaseSnapshot is the leases as they stood before a multi-path patch, keyed exactly
// as the hook keys them.
func leaseSnapshot(events []fleet.Event) map[string]fleet.Rec {
	before := map[string]fleet.Rec{}
	for _, ev := range events {
		target := fleet.S(fleet.M(ev, "tool_input"), "file_path")
		if target == "" {
			continue
		}
		branch := fleet.BranchOf(target)
		if branch == "" {
			continue
		}
		key := fleet.Scope(target, branch)
		if key == "" {
			continue
		}
		if _, ok := before[key]; !ok {
			cur := fleet.Lease(key)
			if fleet.IsMalformed(cur) {
				cur = nil
			}
			before[key] = cur
		}
	}
	return before
}

func rollbackLeases(before map[string]fleet.Rec, sid string) {
	for key, old := range before {
		if _, err := fleet.RestoreLease(key, sid, old); err != nil {
			_ = fleet.AppendJSONL(fleet.Path("hook-errors.jsonl"), fleet.Rec{"at": fleet.Now(), "error": "rollback " + key + ": " + err.Error(), "session": sid})
		}
	}
}

// Run evaluates one Codex event.
func Run(ev fleet.Event) *fleet.Verdict {
	mapped := mappedEvents(ev)
	var before map[string]fleet.Rec
	if len(mapped) > 1 {
		before = leaseSnapshot(mapped)
	}
	var contexts []string
	for _, item := range mapped {
		v := fleet.Run(item)
		if v.Code == 2 && strings.TrimSpace(v.Err) != "" {
			rollbackLeases(before, fleet.S(ev, "session_id"))
			return &fleet.Verdict{Code: 2, Err: v.Err}
		}
		if v.Code != 0 || strings.TrimSpace(v.Out) == "" {
			continue
		}
		out := fleet.ReadJSONBytes([]byte(v.Out))
		if c := fleet.S(fleet.M(out, "hookSpecificOutput"), "additionalContext"); c != "" {
			contexts = append(contexts, c)
		}
	}
	if len(contexts) == 0 {
		return &fleet.Verdict{}
	}
	out := fleet.DumpJSON(fleet.Rec{"hookSpecificOutput": fleet.Rec{
		"hookEventName": fleet.S(ev, "hook_event_name"), "additionalContext": strings.Join(contexts, "\n")}})
	return &fleet.Verdict{Out: string(out) + "\n"}
}
