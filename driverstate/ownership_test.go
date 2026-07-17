package driverstate

import (
	"errors"
	"fmt"
	"testing"
)

// OwnershipLost is the stable predicate a caching caller (the MCP session lease
// map) uses to decide eviction: definitive loss evicts, a transient failure is
// kept and retried.
func TestOwnershipLostClassifies(t *testing.T) {
	lost := []error{
		ErrLeaseExpired,
		ErrNotHolder,
		ErrLocked{Holder: "session:other"},
		fmt.Errorf("renew: %w", ErrLeaseExpired), // wrapped still counts
	}
	for _, err := range lost {
		if !OwnershipLost(err) {
			t.Fatalf("OwnershipLost(%v) = false, want true", err)
		}
	}
	kept := []error{
		nil,
		errLockContended,
		errors.New("driverstate: fsync event: disk full"),
	}
	for _, err := range kept {
		if OwnershipLost(err) {
			t.Fatalf("OwnershipLost(%v) = true, want false (transient, keep the lease)", err)
		}
	}
}
