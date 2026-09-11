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
	status["notification_configured"] = os.Getenv("FLEET_NOTIFY") != ""
	return status
}

func enrichStatus(row fleet.Rec, work []verbs.WorkRow) {
	path := fleet.S(row, "cwd")
	row["branch"] = fleet.BranchOf(path)
	assignment := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(fleet.S(row, "slot"))+".json"))
	if fleet.S(row, "slot") != "" {
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
	mail, err := fleet.MailFor(tenant, fleet.S(row, "address"), true)
	if err != nil {
		row["mail_error"] = err.Error()
	}
	if err == nil {
		row["unacked_mail"] = len(mail)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		row["head_error"] = "could not read checkout HEAD"
		return
	}
	row["head"] = strings.TrimSpace(string(out))
}

// AllStatusText keeps process, hook activity and receipt completion separate.
func AllStatusText(status fleet.Rec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Watcher: %s; notifications configured: %t\n", fleet.S(status, "watcher"), fleet.B(status, "notification_configured"))
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
