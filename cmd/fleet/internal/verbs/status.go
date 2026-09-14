package verbs

import (
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// CmdStatus is a read-only, local request view. It neither infers semantic worker
// acceptance nor claims process termination from a stop flag.
func CmdStatus(args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return refuse("usage: fleet status [--json]")
	}
	// CLI and MCP execute verbs serially; this scoped flag is not goroutine-local.
	before := fleet.ReadOnly
	fleet.ReadOnly = true
	defer func() { fleet.ReadOnly = before }()
	pending := statusMigrationPending()
	rows, err := strictDispatchRows()
	out := []fleet.Rec{}
	gaps := []string{}
	if err != nil {
		gaps = append(gaps, err.Error())
	}
	now := fleet.Now()
	for _, row := range rows {
		if fleet.S(row, "request_id") != "" {
			out = append(out, requestStatus(row, now))
		}
	}
	if pending || statusMigrationPending() {
		gaps = append(gaps, "legacy state needs reconciliation")
	}
	packet := fleet.Rec{"schema": "fleet-task-status-v1", "at": now, "complete": len(gaps) == 0,
		"scope": "local request-bound assignments only", "tasks": out, "gaps": gaps}
	if len(args) == 1 {
		say("%s", jsonIndent(packet))
	}
	if len(args) == 0 {
		renderStatus(out, gaps)
	}
	if len(gaps) > 0 {
		return refuse("status needs checking: assignment evidence incomplete")
	}
	return nil
}

func requestStatus(d fleet.Rec, now float64) fleet.Rec {
	sid, rid, branch := fleet.S(d, "worker"), fleet.S(d, "repo"), fleet.S(d, "change")
	key := "repo:" + rid + ":" + branch
	row := fleet.Rec{"request_id": d["request_id"], "work": branch, "repo": rid,
		"worker": sid, "lead": d["for"], "brief": d["brief"], "status": "Queued",
		"relationship": d["relationship"],
		"needs":        "Worker delivery and acceptance are unconfirmed", "next": "Deliver the brief through the worker's supported harness", "verified_at": now}
	rec, lease := fleet.SessionRecord(sid), fleet.Lease(key)
	write := fleet.M(fleet.M(rec, "last_writes"), key)
	if write == nil {
		write = fleet.M(rec, "last_write")
	}
	switch {
	case rec == nil || fleet.IsMalformed(lease):
		taskState(row, "Status needs checking", "Worker or ownership evidence is unavailable", "Inspect the evidence; do not redispatch")
	case lease != nil && fleet.S(lease, "session") != sid:
		taskState(row, "Status needs checking", "Another session holds the branch", "Resolve the ownership conflict")
	case fleet.CheckStop(key, branch, sid) != "":
		taskState(row, "Status needs checking", "A stop flag exists; process termination is unconfirmed", "Inspect the affected session before continuing")
	case !fleet.SessionAlive(rec):
		taskState(row, "Status needs checking", "Worker is no longer observably live", "Recover its work before choosing a replacement")
	case fleet.S(write, "key") == key && fleet.F(write, "at") >= fleet.F(d, "at"):
		row["activity_at"] = write["at"]
		taskState(row, "Activity observed", "Acceptance and completion remain unconfirmed", "Read the worker's result and current checks")
	}
	if entry := fleet.M(d, "entry"); entry != nil {
		row["entry"] = entry
		row["current_head"] = requestCurrentHead(rid, branch, rec)
		if fleet.S(row, "current_head") != fleet.S(entry, "head") {
			taskState(row, "Status needs checking", "Input revision changed or is unavailable; admission is historical", "Inspect the recorded packet and recheck the revision before working")
		}
	}
	return row
}

func requestCurrentHead(rid, branch string, session fleet.Rec) string {
	co := checkoutFor(rid)
	if co == "" && fleet.RepoID(fleet.S(session, "cwd")) == rid {
		co = fleet.S(session, "cwd")
	}
	if co == "" {
		return ""
	}
	rc, head := gitTry(co, gitTimeout, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if rc != 0 {
		return ""
	}
	return strings.TrimSpace(head)
}

func taskState(row fleet.Rec, state, needs, next string) {
	row["status"], row["needs"], row["next"] = state, needs, next
}

func renderStatus(rows []fleet.Rec, gaps []string) {
	if len(gaps) > 0 {
		say("Status needs checking: %s", strings.Join(gaps, "; "))
	}
	say("Local assignments — recording work is not delivery or acceptance.")
	if len(rows) == 0 {
		say("No request-bound work recorded.")
		return
	}
	for _, row := range rows {
		if at := fleet.F(row, "activity_at"); at > 0 {
			row["status"] = fleet.S(row, "status") + " (" + fleet.FmtAge(fleet.F(row, "verified_at")-at) + " ago)"
		}
		// IDs remain in JSON details. Do not guess a model/person name from a session.
		say("%s — %s\n  Needs: %s\n  Next: %s", terminalText(fleet.S(row, "work")), fleet.S(row, "status"), terminalText(fleet.S(row, "needs")), terminalText(fleet.S(row, "next")))
	}
}

func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}

func statusMigrationPending() bool {
	if _, err := os.Stat(fleet.State); os.IsNotExist(err) {
		return false
	}
	return fleet.MigrationPending()
}
