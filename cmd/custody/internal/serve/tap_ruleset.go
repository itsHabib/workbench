package serve

import "strings"

// Firewall-ruleset matching, kept portable and free of the linux build tag so
// it is unit-testable on every platform. The linux prober (tap_linux.go) owns
// the exec mechanism; these functions own the policy — what counts as the
// source-restriction rule being in force.
//
// A rule that merely names the port is NOT sufficient: a bare `dport <port>`
// match happily covers a `drop` rule, or a source-unrestricted `accept`, either
// of which would let the preflight pass while the tap listener is wide open.
// The tap listener's safety rests on the firewall pinning traffic to the room
// source, so the preflight must see an ACCEPT rule that carries a source
// restriction on the same rule/line.

// nftAllowsPort reports whether an `nft list ruleset` dump contains a single
// rule that accepts port AND restricts by source address (`ip saddr` /
// `ip6 saddr` — the `saddr` token covers both). Matching is per-line so the
// three tokens must co-occur on one rule, never spread across unrelated ones.
func nftAllowsPort(ruleset, port string) bool {
	for _, line := range strings.Split(ruleset, "\n") {
		if strings.Contains(line, "dport "+port) &&
			strings.Contains(line, "accept") &&
			strings.Contains(line, "saddr") {
			return true
		}
	}
	return false
}

// iptablesAllowsPort reports the same for `iptables-save` (or `ip6tables-save`)
// output: an `-j ACCEPT` rule for port carrying a source restriction (`-s`) on
// the same line. A `-j DROP` line, or an ACCEPT with no `-s`, is not enough.
func iptablesAllowsPort(save, port string) bool {
	for _, line := range strings.Split(save, "\n") {
		if strings.Contains(line, "--dport "+port) &&
			strings.Contains(line, "-j ACCEPT") &&
			strings.Contains(line, "-s ") {
			return true
		}
	}
	return false
}
