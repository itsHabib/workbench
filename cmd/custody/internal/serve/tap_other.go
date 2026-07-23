//go:build !linux

package serve

import "fmt"

// DefaultProber returns a prober that always refuses on non-Linux hosts.
// The tap listener is a Linux-only feature; the preflight guard prevents
// startup on unsupported platforms with a diagnostic error.
func DefaultProber() RulesetProber { return unsupportedProber{} }

type unsupportedProber struct{}

func (unsupportedProber) InForce(_ string) (bool, error) {
	return false, fmt.Errorf("tap listener firewall preflight is not supported on this platform (Linux only)")
}
