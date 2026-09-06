package verbs

// Test-support verbs, all prefixed `x-`. They expose substrate primitives to the
// suite so its concurrency scenarios can drive the REAL binary instead of importing
// a Python module: eight processes racing `x-acquire`, a paused `x-check-lease`
// against a rival, a lock held by `x-hold` while a verb is refused. Nothing here is a
// user verb, nothing is in the usage text, and nothing here bypasses a lock — each is
// the same call the hook or a verb makes.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func xtest(verb string, a []string) error {
	arg := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	switch verb {
	case "x-id":
		say("%s", fleet.RepoID(arg(0)))
	case "x-key":
		say("%s", fleet.Scope(arg(0), arg(1)))
	case "x-safe":
		say("%s", fleet.Safe(arg(0)))
	case "x-role":
		role, tenant, slot := fleet.MapRowsFor(arg(0))
		say("%s", jsonCompact(map[string]any{"role": nilIfEmpty(role), "tenant": nilIfEmpty(tenant), "slot": nilIfEmpty(slot)}))
	case "x-lease":
		say("%s", jsonCompact(fleet.Lease(arg(0))))
	case "x-acquire":
		note := noteArg(arg(4))
		ok, err := fleet.AcquireLease(arg(0), fleet.LeaseRecord(arg(0), arg(1), arg(2), arg(3), note))
		if err != nil {
			return err
		}
		if !ok {
			return exitCode(1, "")
		}
	case "x-take":
		note := noteArg(arg(5))
		was, err := fleet.TakeLease(arg(0), arg(1), arg(2), arg(3), arg(4), note)
		if err != nil {
			return err
		}
		say("%s", jsonCompact(was))
	case "x-drop":
		ok, err := fleet.DropLease(arg(0), arg(1))
		if err != nil {
			return err
		}
		if !ok {
			return exitCode(1, "")
		}
	case "x-check-lease":
		// exit 0 allowed; exit 1 denied with the reason on stdout, as the hook's stderr would carry it.
		if reason := fleet.CheckLease(arg(0), arg(1), arg(2), arg(3), arg(4)); reason != "" {
			say("%s", reason)
			return exitCode(1, "")
		}
	case "x-hold":
		// Hold a key's lock for N seconds, printing `held` once it is taken so a caller can
		// synchronise on it. Killed by the caller when the window should close.
		secs, err := strconv.ParseFloat(arg(1), 64)
		if err != nil {
			return refuse("usage: fleet x-hold <key> <seconds>")
		}
		return fleet.KeyLock(arg(0), func() error {
			fmt.Fprintln(Out, "held")
			if f, ok := Out.(*os.File); ok {
				_ = f.Sync()
			}
			time.Sleep(time.Duration(secs * float64(time.Second)))
			return nil
		})
	case "x-stop-flag":
		say("%s", jsonCompact(fleet.StopFlag(arg(0))))
	case "x-migrate":
		fleet.MigrateLegacyKeys()
	case "x-remove-owned":
		field := arg(2)
		if field == "" {
			field = "session"
		}
		fleet.RemoveOwned(arg(0), arg(1), field)
	case "x-check-requires":
		// The session record on disk is the `rec` the hook would have read.
		if reason := fleet.CheckRequires(fleet.SessionRecord(arg(0)), arg(0), arg(1), arg(2)); reason != "" {
			say("%s", reason)
			return exitCode(1, "")
		}
	case "x-switch":
		out := fleet.SwitchTargets(strings.Join(a[1:], " "), arg(0))
		if out == nil {
			out = []string{}
		}
		say("%s", jsonCompact(out))
	default:
		return exitCode(2, "fleet: unknown test-support verb "+verb)
	}
	return nil
}

func noteArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
