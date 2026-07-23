//go:build linux

package serve

import (
	"fmt"
	"net"
	"os/exec"
)

// DefaultProber returns the Linux RulesetProber. It uses nftables when `nft` is
// installed and treats its verdict — or its error — as authoritative; only a
// host without nft falls back to iptables-save / ip6tables-save. An nft that is
// present but fails (permissions, a transient error) is NOT masked by probing a
// different firewall's tables: the error propagates and the preflight fails
// closed.
func DefaultProber() RulesetProber { return linuxProber{} }

type linuxProber struct{}

// InForce checks that the custody port is restricted to the room source for the
// tap listener's OWN address family. The family is load-bearing: an IPv4 rule
// does not protect an IPv6 listener (ip6tables/nft ip6 are separate), so a
// family-blind check would let an IPv6 tap pass on a coincidental IPv4 rule
// while its listener sits unprotected.
func (linuxProber) InForce(tapAddr string) (bool, error) {
	host, port, err := net.SplitHostPort(tapAddr)
	if err != nil {
		return false, fmt.Errorf("parse tap addr %q: %w", tapAddr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, fmt.Errorf("tap addr host %q is not an IP", host)
	}
	v6 := ip.To4() == nil
	if _, err := exec.LookPath("nft"); err == nil {
		return probeNftables(port, v6)
	}
	return probeIPTables(port, v6)
}

// probeNftables runs `nft list ruleset` and reports whether it proves the
// custody port is restricted for the tap's family (family-matching
// source-restricted accept AND a drop/reject; see nftInForce).
func probeNftables(port string, v6 bool) (bool, error) {
	out, err := exec.Command("nft", "list", "ruleset").Output()
	if err != nil {
		return false, fmt.Errorf("nft: %w", err)
	}
	return nftInForce(string(out), port, v6), nil
}

// probeIPTables runs the family-matching save tool — `ip6tables-save` for an
// IPv6 tap, `iptables-save` for IPv4 — so an IPv6 listener is never judged
// against IPv4 rules or vice versa. A failing save tool fails closed.
func probeIPTables(port string, v6 bool) (bool, error) {
	tool := "iptables-save"
	if v6 {
		tool = "ip6tables-save"
	}
	out, err := exec.Command(tool).Output()
	if err != nil {
		return false, fmt.Errorf("%s: %w", tool, err)
	}
	return iptablesInForce(string(out), port), nil
}
