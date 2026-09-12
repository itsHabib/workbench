package verbs

import (
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/report"
	"time"
)

func cmdReport(args []string) error {
	if len(args) == 1 && args[0] == "--snapshot" {
		return reportSnapshot()
	}
	span := 24 * time.Hour
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--since" {
			return refuse("usage: fleet report [--since 24h | --snapshot]")
		}
		d, err := time.ParseDuration(args[1])
		if err != nil || d <= 0 {
			return refuse("fleet report: --since needs a positive duration like 24h")
		}
		span = d
	}
	now := fleet.Now()
	say("%s", report.Render(fleet.Path(), now-span.Seconds(), now))
	return nil
}

// reportSnapshot is a read-only observation protocol for supervisors. The schema
// tag is also a capability marker: a consumer can inspect an installed executable
// before invoking it, since older providers migrate even before rejecting a verb.
// This is observation, not an atomic ownership transaction or dispatch permission.
func reportSnapshot() error {
	before := fleet.ReadOnly
	fleet.ReadOnly = true
	defer func() { fleet.ReadOnly = before }()
	pending := fleet.MigrationPending()
	work, board, slots := WorkRows(""), BoardRows(), SlotRows("")
	// Check both sides of the read window so directory movement cannot be hidden.
	say("%s", jsonIndent(map[string]any{
		"schema": "fleet-observation-v1", "at": fleet.Now(),
		"org_state": fleet.OrgState, "fleet_state": fleet.State,
		"migration_pending": pending || fleet.MigrationPending(),
		"work":              work, "board": board, "slots": slots,
	}))
	return nil
}
