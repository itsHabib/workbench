//go:build linux

package serve

import (
	"fmt"
	"net"
	"os/exec"
)

// DefaultProber returns the Linux RulesetProber. It probes nftables first and
// falls back to iptables-save only when nft fails to exec (tool absent) — a
// clean nft result of "rule absent" is authoritative and is NOT second-guessed
// by iptables, because if nft is the active firewall its verdict is the truth.
func DefaultProber() RulesetProber { return linuxProber{} }

type linuxProber struct{}

// InForce checks for a source-restriction accept rule covering tapAddr's port.
// It probes nftables first; only an nft exec error (not a clean "rule absent")
// falls through to iptables-save. A successful probe returning false means the
// rule is absent.
func (linuxProber) InForce(tapAddr string) (bool, error) {
	_, port, err := net.SplitHostPort(tapAddr)
	if err != nil {
		return false, fmt.Errorf("parse tap addr %q: %w", tapAddr, err)
	}
	if ok, err := probeNftables(port); err == nil {
		return ok, nil
	}
	return probeIPTables(port)
}

// probeNftables runs `nft list ruleset` and reports whether it carries a
// source-restricted accept rule for the custody port (see nftAllowsPort). A
// bare port mention — a drop rule, or a source-unrestricted accept — is not
// sufficient.
func probeNftables(port string) (bool, error) {
	out, err := exec.Command("nft", "list", "ruleset").Output()
	if err != nil {
		return false, fmt.Errorf("nft: %w", err)
	}
	return nftAllowsPort(string(out), port), nil
}

// probeIPTables runs `iptables-save` and reports whether it carries a
// source-restricted ACCEPT rule for the custody port (see iptablesAllowsPort).
func probeIPTables(port string) (bool, error) {
	out, err := exec.Command("iptables-save").Output()
	if err != nil {
		return false, fmt.Errorf("iptables-save: %w", err)
	}
	return iptablesAllowsPort(string(out), port), nil
}
