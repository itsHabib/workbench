// Package tier defines the shared risk-tier ordering used by both verdict
// composition and grant ceilings, so the two can never drift apart.
package tier

// Valid reports whether t names a defined tier.
func Valid(t string) bool {
	return t == "T0" || t == "T1" || t == "T2" || t == "T3"
}

// Rank orders tiers T0 < T1 < T2 < T3. Anything unknown ranks highest —
// fail closed.
func Rank(t string) int {
	switch t {
	case "T0":
		return 0
	case "T1":
		return 1
	case "T2":
		return 2
	default:
		return 3
	}
}
