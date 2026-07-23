//go:build linux

package serve

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// DefaultProber returns the Linux RulesetProber. It tries nftables first and
// falls back to iptables-save, looking for a rule that restricts inbound
// traffic to the tap listener's port. Both probes must fail for the preflight
// to give up; an exec failure from the first probe is not surfaced unless the
// fallback also fails.
func DefaultProber() RulesetProber { return linuxProber{} }

type linuxProber struct{}

// InForce checks for a source-restriction rule covering tapAddr's port.
// It probes nftables first; on any exec error it falls back to iptables-save.
// A successful probe returning false means the rule is absent.
func (linuxProber) InForce(tapAddr string) (bool, error) {
	_, port, err := net.SplitHostPort(tapAddr)
	if err != nil {
		return false, fmt.Errorf("parse tap addr %q: %w", tapAddr, err)
	}
	// Try nftables first.
	if ok, err := probeNftables(port); err == nil {
		return ok, nil
	}
	// Fall back to iptables-save.
	return probeIPTables(port)
}

// probeNftables runs `nft list ruleset` and checks for the custody port. A
// rule of the form `dport <port> accept` (or equivalent) is considered
// sufficient evidence that the restricting rule is in place.
func probeNftables(port string) (bool, error) {
	out, err := exec.Command("nft", "list", "ruleset").Output()
	if err != nil {
		return false, fmt.Errorf("nft: %w", err)
	}
	return bytes.Contains(out, []byte("dport "+port)), nil
}

// probeIPTables runs `iptables-save` and checks for the custody port.
func probeIPTables(port string) (bool, error) {
	out, err := exec.Command("iptables-save").Output()
	if err != nil {
		return false, fmt.Errorf("iptables-save: %w", err)
	}
	return strings.Contains(string(out), "--dport "+port), nil
}
