package watch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
)

// AllStatus joins live local records without a watcher tick or store mutation.
func AllStatus() fleet.Rec {
	before := fleet.ReadOnly
	fleet.ReadOnly = true
	defer func() { fleet.ReadOnly = before }()
	status := RuntimeStatus()
	workers := status["workers"].([]fleet.Rec)
	sessions, work := sessionRecords(), verbs.WorkRows("")
	rows := []fleet.Rec{}
	seen := map[string]bool{}
	configured := map[string]fleet.Rec{}
	for _, row := range workers {
		configured[statusKey(row)] = row
	}
	for _, board := range verbs.BoardRows() {
		path := fleet.CanonPath(fleet.S(board, "path"))
		address := fleet.S(board, "slot")
		if address == "" {
			address = fleet.S(board, "role")
		}
		row := configured[path+"\x00"+address]
		if row == nil {
			row = runtimeRow(deliverTarget{address: address, cwd: path}, sessions)
			boardActivity(row, board, sessions)
		}
		row["role"], row["slot"], row["tenant"], row["occupancy"] = board["role"], board["slot"], board["tenant"], board["state"]
		enrichStatus(row, work)
		rows = append(rows, row)
		seen[statusKey(row)] = true
	}
	for _, row := range workers {
		if seen[statusKey(row)] {
			continue
		}
		enrichStatus(row, work)
		rows = append(rows, row)
	}
	status["workers"] = rows
	status["scope"] = "local role bindings and configured headless targets"
	status["notification_configured"] = fleet.M(status, "heartbeat")["notification_configured"]
	return status
}

func enrichStatus(row fleet.Rec, work []verbs.WorkRow) {
	path := fleet.S(row, "cwd")
	row["work"] = []verbs.WorkRow{}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		row["branch"], row["head"], row["assignment"] = nil, nil, nil
		row["head_error"] = "checkout directory is missing or unreadable; no checkout-derived joins"
		return
	}
	row["branch"] = fleet.BranchOf(path)
	assignment := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(fleet.S(row, "slot"))+".json"))
	if currentAssignment(row, assignment) {
		row["assignment"] = assignment
	}
	rows := []verbs.WorkRow{}
	for _, w := range work {
		sameSeat := fleet.S(row, "slot") != "" && fleet.S(w, "slot") == fleet.S(row, "slot")
		sameBranch := fleet.S(row, "branch") != "" && fleet.S(w, "repo") == fleet.RepoID(path) && fleet.S(w, "change") == fleet.S(row, "branch")
		if sameSeat || sameBranch {
			rows = append(rows, w)
		}
	}
	row["work"] = rows
	tenant := fleet.S(row, "tenant")
	if tenant == "" {
		tenant, _ = fleet.MailRoleTenant(fleet.S(row, "address"))
	}
	mail, partial, err := fleet.MailSnapshot(tenant, fleet.S(row, "address"))
	if err != nil {
		row["mail_error"] = err.Error()
	}
	if partial {
		row["mail_error"] = "bounded mailbox scan is partial; unacknowledged count unknown"
	}
	if err == nil && !partial {
		unacked := 0
		for _, m := range mail {
			if !fleet.Has(m, "acked_at") {
				unacked++
			}
		}
		row["unacked_mail"] = unacked
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		row["head_error"] = "could not read checkout HEAD"
		return
	}
	row["head"] = strings.TrimSpace(string(out))
}

// AllStatusText keeps process, hook activity and receipt completion separate.
func AllStatusText(status fleet.Rec) string {
	var b strings.Builder
	notifier := "unknown"
	if configured, ok := status["notification_configured"].(bool); ok {
		notifier = fmt.Sprintf("%t", configured)
	}
	fmt.Fprintf(&b, "Watcher: %s; notifications configured: %s\n", fleet.S(status, "watcher"), notifier)
	if err := fleet.S(status, "configuration_error"); err != "" {
		fmt.Fprintf(&b, "Configuration: %s\n", err)
	}
	for _, row := range status["workers"].([]fleet.Rec) {
		renderRuntimeRow(&b, row, fleet.F(status, "at"))
		fmt.Fprintf(&b, "  Role: %s · head %s · unacked mail: %v\n", fleet.S(row, "role"), fleet.S(row, "head"), row["unacked_mail"])
		if a := fleet.M(row, "assignment"); a != nil {
			fmt.Fprintf(&b, "  Assignment: %s · %s\n", fleet.S(a, "branch"), fleet.S(a, "brief"))
		}
		for _, w := range row["work"].([]verbs.WorkRow) {
			fmt.Fprintf(&b, "  Work: %s\n", verbs.WorkLine(w, fleet.F(status, "at")))
		}
		for _, k := range []string{"mail_error", "head_error"} {
			if e := fleet.S(row, k); e != "" {
				fmt.Fprintf(&b, "  %s: %s\n", k, e)
			}
		}
	}
	return b.String()
}

func statusKey(row fleet.Rec) string {
	return fleet.CanonPath(fleet.S(row, "cwd")) + "\x00" + fleet.S(row, "address")
}

// BoardRows selects the observed occupant, including sessions in subdirectories.
// Only fill an unconfigured row with no launch history; keep process evidence separate.
func boardActivity(row fleet.Rec, board verbs.BoardRow, sessions []fleet.Rec) {
	if fleet.S(row, "state") != "not_started" || fleet.S(board, "session") == "" {
		return
	}
	for _, s := range sessions {
		if fleet.S(s, "session") != fleet.S(board, "session") {
			continue
		}
		for _, k := range []string{"session", "last_event", "last_event_at", "last_tool", "turn_open", "transcript_path", "branch"} {
			row[k] = s[k]
		}
		row["state"] = "observed_session"
		return
	}
}

func currentAssignment(row, a fleet.Rec) bool {
	path := fleet.S(row, "cwd")
	if a == nil || fleet.S(row, "slot") == "" || fleet.S(a, "path") == "" || fleet.CanonPath(fleet.S(a, "path")) != fleet.CanonPath(path) {
		return false
	}
	for _, key := range []string{"slot", "role", "tenant", "branch"} {
		if fleet.S(row, key) == "" || fleet.S(row, key) != fleet.S(a, key) {
			return false
		}
	}
	repo := fleet.RepoID(path)
	return repo != "" && fleet.S(a, "repo") == repo
}
