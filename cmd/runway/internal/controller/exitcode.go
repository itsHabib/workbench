package controller

import "github.com/itsHabib/workbench/contracts/execution"

// Exit codes from TDD §6. The result schema is authoritative; these map the
// receipt (or read/cancel outcome) onto process status for shell callers.
const (
	ExitOK                   = 0
	ExitUsage                = 2
	ExitFailed               = 3
	ExitPlacementUnavailable = 4
	ExitTimedOut             = 124
	ExitCancelled            = 130
)

// ExitFromResult maps a validated terminal receipt to the §6 process exit.
func ExitFromResult(r execution.Result) int {
	switch r.Status {
	case execution.StatusSucceeded:
		return ExitOK
	case execution.StatusTimedOut:
		return ExitTimedOut
	case execution.StatusCancelled:
		return ExitCancelled
	case execution.StatusFailed:
		if r.ReasonCode == execution.ReasonPlacementUnavailable {
			return ExitPlacementUnavailable
		}
		return ExitFailed
	}
	return ExitFailed
}
