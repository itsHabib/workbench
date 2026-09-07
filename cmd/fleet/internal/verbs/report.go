package verbs

import (
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/report"
	"time"
)

func cmdReport(args []string) error {
	span := 24 * time.Hour
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--since" {
			return refuse("usage: fleet report [--since 24h]")
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
