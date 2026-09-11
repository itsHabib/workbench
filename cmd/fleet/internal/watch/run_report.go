package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// RunReport reads retained per-attempt outputs and exits. It never schedules work.
// The window uses output modification time; missing result fields stay unknown.
func RunReport(since float64) fleet.Rec {
	rows := []fleet.Rec{}
	gaps := []string{}
	entries, err := os.ReadDir(filepath.Join(dir(), "delivery"))
	if err != nil && !os.IsNotExist(err) {
		gaps = append(gaps, err.Error())
	}
	var cost, turns float64
	knownCost, knownTurns := 0, 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			gaps = append(gaps, entry.Name()+": "+err.Error())
			continue
		}
		at := float64(info.ModTime().UnixNano()) / 1e9
		if at < since {
			continue
		}
		path := filepath.Join(dir(), "delivery", entry.Name())
		row := attemptReport(path, at)
		rows = append(rows, row)
		if fleet.Has(row, "cost_usd") {
			cost += fleet.F(row, "cost_usd")
			knownCost++
		}
		if fleet.Has(row, "turns") {
			turns += fleet.F(row, "turns")
			knownTurns++
		}
	}
	return fleet.Rec{"since": since, "at": fleet.Now(), "window": "output modification time", "attempts": rows, "attempt_count": len(rows), "reported_cost_usd": cost, "reported_turns": turns, "cost_known": knownCost, "turns_known": knownTurns, "gaps": gaps}
}

func attemptReport(path string, at float64) fleet.Rec {
	row := fleet.Rec{"output": path, "output_at": at, "state": "exit_unknown"}
	exit := fleet.ReadJSON(strings.TrimSuffix(path, ".log") + ".exit.json")
	if exit != nil {
		row["address"], row["exit_code"], row["exited_at"] = exit["address"], exit["exit_code"], exit["at"]
		row["state"] = "exited"
	}
	result := lastResult(path)
	if result == nil {
		row["result_error"] = "no result found in final 1 MiB of output"
		return row
	}
	row["session"], row["reason"], row["is_error"] = result["session_id"], result["subtype"], result["is_error"]
	for _, pair := range [][2]string{{"total_cost_usd", "cost_usd"}, {"num_turns", "turns"}} {
		if value, ok := result[pair[0]].(float64); ok {
			row[pair[1]] = value
		}
	}
	return row
}

// RunReportText prints totals only for fields actually reported by the provider.
func RunReportText(report fleet.Rec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v attempts · reported $%.4f (%v costs known) · %.0f turns (%v counts known)\n", report["attempt_count"], fleet.F(report, "reported_cost_usd"), report["cost_known"], fleet.F(report, "reported_turns"), report["turns_known"])
	for _, row := range report["attempts"].([]fleet.Rec) {
		cost, turns := "unknown", "unknown"
		if fleet.Has(row, "cost_usd") {
			cost = fmt.Sprintf("$%.4f", fleet.F(row, "cost_usd"))
		}
		if fleet.Has(row, "turns") {
			turns = fmt.Sprintf("%.0f", fleet.F(row, "turns"))
		}
		fmt.Fprintf(&b, "%s · session %s · %s exit %v · %s · %s · turns %s\n  %s\n", fleet.S(row, "address"), fleet.S(row, "session"), fleet.S(row, "state"), row["exit_code"], fleet.S(row, "reason"), cost, turns, fleet.S(row, "output"))
		if e := fleet.S(row, "result_error"); e != "" {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	for _, gap := range report["gaps"].([]string) {
		fmt.Fprintf(&b, "Gap: %s\n", gap)
	}
	fmt.Fprintln(&b, "Window: output modification time. Missing provider totals are unknown, not zero. Exits are not task receipts.")
	return b.String()
}
