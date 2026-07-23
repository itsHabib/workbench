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

// InForce checks that the custody port is restricted to the room source.
func (linuxProber) InForce(tapAddr string) (bool, error) {
	_, port, err := net.SplitHostPort(tapAddr)
	if err != nil {
		return false, fmt.Errorf("parse tap addr %q: %w", tapAddr, err)
	}
	if _, err := exec.LookPath("nft"); err == nil {
		return probeNftables(port)
	}
	return probeIPTables(port)
}

// probeNftables runs `nft list ruleset` and reports whether it proves the
// custody port is restricted (source-restricted accept AND a drop; see
// nftInForce).
func probeNftables(port string) (bool, error) {
	out, err := exec.Command("nft", "list", "ruleset").Output()
	if err != nil {
		return false, fmt.Errorf("nft: %w", err)
	}
	return nftInForce(string(out), port), nil
}

// probeIPTables runs `iptables-save` and, for IPv6 room subnets, `ip6tables-save`,
// reporting whether either proves the custody port is restricted. A missing
// `ip6tables-save` is not fatal — the IPv4 result already stands; only a failing
// `iptables-save` fails closed.
func probeIPTables(port string) (bool, error) {
	out4, err := exec.Command("iptables-save").Output()
	if err != nil {
		return false, fmt.Errorf("iptables-save: %w", err)
	}
	if iptablesInForce(string(out4), port) {
		return true, nil
	}
	out6, err := exec.Command("ip6tables-save").Output()
	if err != nil {
		return false, nil
	}
	return iptablesInForce(string(out6), port), nil
}
