package foundation

import (
	"fmt"
	"os"
	"strings"
)

// Fault injects backend failures for demos and tests. It is parsed from
// WB_FAULT by the programs that open a workspace:
//
//	fail:<label>   Exec for <label> fails before the child starts.
//	crash:<label>  the process is killed (SIGKILL) right after Exec for
//	               <label> commits its output, before the caller can
//	               record the outcome: an interrupted run.
type Fault struct {
	Fail  string
	Crash string
}

// ParseFault parses a WB_FAULT value; empty means no fault.
func ParseFault(s string) (Fault, error) {
	if s == "" {
		return Fault{}, nil
	}
	kind, label, ok := strings.Cut(s, ":")
	if !ok || label == "" {
		return Fault{}, fmt.Errorf("WB_FAULT %q: want fail:<label> or crash:<label>", s)
	}
	switch kind {
	case "fail":
		return Fault{Fail: label}, nil
	case "crash":
		return Fault{Crash: label}, nil
	}
	return Fault{}, fmt.Errorf("WB_FAULT %q: unknown fault %q", s, kind)
}

// crash kills this process the way kill -9 would: no deferred calls, no
// further writes. On Unix a signal sent to oneself is delivered before
// kill returns, so the panic is only reached if the kill itself failed.
func crash(label string) {
	p, err := os.FindProcess(os.Getpid())
	if err == nil {
		err = p.Kill()
	}
	panic(fmt.Sprintf("crash fault for %s: kill failed: %v", label, err))
}
