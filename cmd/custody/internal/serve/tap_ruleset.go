package serve

import "strings"

// Firewall-ruleset matching, kept portable and free of the linux build tag so
// it is unit-testable on every platform. The linux prober (tap_linux.go) owns
// the exec mechanism; these functions own the policy — what counts as the
// port being restricted to the room source.
//
// "In force" needs BOTH halves on the custody port:
//   - a source-restricted ACCEPT — the room may reach the listener, and
//   - a DROP or REJECT — every other source is denied (reject-with-tcp-reset
//     is a legitimate deny too, so both verdicts count).
//
// The deny is the security-critical half. The runbook's chains are
// policy-ACCEPT (a host-wide policy-DROP risks locking the host out), so an
// accept rule alone proves nothing: absent the deny, non-room traffic to the
// port simply falls through to `policy accept`. Matching a bare `dport <port>`
// is likewise insufficient — it covers a drop-only rule or a source-
// unrestricted accept. Tokens are compared field-exact so port `8127` never
// matches a rule for `81270`.
//
// The match is also ADDRESS-FAMILY aware: the accept must restrict a source of
// the SAME family as the tap listener's bind address. An IPv4 `ip saddr` rule
// does not protect an IPv6 listener (and vice versa) — treating it as evidence
// would let an IPv6 tap pass preflight while its listener sits unprotected.
// (For iptables the family is chosen by the tool — iptables-save vs
// ip6tables-save — in tap_linux.go.)

// nftInForce reports whether an `nft list ruleset` dump proves the port is
// restricted for the tap's address family: a source-restricted accept whose
// `saddr` matches that family AND a drop/reject, each on a rule naming the
// port. v6 selects `ip6 saddr` over `ip saddr`.
func nftInForce(ruleset, port string, v6 bool) bool {
	srcToken := "ip"
	if v6 {
		srcToken = "ip6"
	}
	var accept, deny bool
	for _, line := range strings.Split(ruleset, "\n") {
		f := strings.Fields(line)
		if !tokenFollowedBy(f, "dport", port) {
			continue
		}
		if containsField(f, "accept") && tokenFollowedBy(f, srcToken, "saddr") {
			accept = true
		}
		if containsField(f, "drop") || containsField(f, "reject") {
			deny = true
		}
	}
	return accept && deny
}

// iptablesInForce reports the same for a single family's `iptables-save` /
// `ip6tables-save` output: an `-j ACCEPT` rule carrying a source restriction
// (`-s`) AND a `-j DROP`/`-j REJECT` rule, each on a line naming the port via
// `--dport`. The family is the caller's choice of which save output to pass.
func iptablesInForce(save, port string) bool {
	var accept, deny bool
	for _, line := range strings.Split(save, "\n") {
		f := strings.Fields(line)
		if !tokenFollowedBy(f, "--dport", port) {
			continue
		}
		if containsField(f, "ACCEPT") && containsField(f, "-s") {
			accept = true
		}
		if containsField(f, "DROP") || containsField(f, "REJECT") {
			deny = true
		}
	}
	return accept && deny
}

// tokenFollowedBy reports whether token appears in fields immediately followed
// by want — a field-exact "flag value" check (e.g. `dport 8127`, `ip6 saddr`).
func tokenFollowedBy(fields []string, token, want string) bool {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == token && fields[i+1] == want {
			return true
		}
	}
	return false
}

func containsField(fields []string, s string) bool {
	for _, f := range fields {
		if f == s {
			return true
		}
	}
	return false
}
