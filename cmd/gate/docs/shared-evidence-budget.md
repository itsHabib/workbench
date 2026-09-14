# Share evidence capacity across required sections

Rooms #121 at `7db241e2cf714fec6aa02ce0b40f5dc89e0ed6f0` needs five complete
exact-head source files totaling 562,233 bytes. Its 65 active review comments
contain 184,419 bytes of recorded JSON. The previous renderer rejected both
categories at their separate limits while required-diff capacity remained unused.
Unknown-head historical reviews still contain claims to evaluate; a newer clean
review does not establish their resolution.

Supplemental source, additional required reviews, and required complete diff
sections now share their existing combined allowance: 512 + 64 + 256 = 832 KiB.
The renderer counts full rendered entries, including headers and neutralized
artifact markers. It writes additional required reviews first, then collected
source, then required diff sections. A review already fully shown in the initial
history section is counted once. No required review or source is clipped.

A diff section that cannot fit requests exact-head source, which can be smaller
than a large deletion or replacement diff. Missing source/review capacity is
reported explicitly and keeps judgment incomplete. The evidence collector
evaluates the candidate packet under its existing audited append lock. That check
includes prior supplements and reviews, and an impossible supplement appends
nothing. Each recorded path and blob renders and counts once, and the collector
skips paths the run already holds, so two collectors racing on the same required
file do not consume its space twice. Collected source may displace a required
diff while the packet is still incomplete, because smaller source can then
repair it; a supplement whose sources would leave a complete packet incomplete
is refused, counting the packet as complete when the supplement's own file index
would complete it.

The initial recorded-history and diff rendering remain separate. This change
does not establish an overall context or token limit. It does not change review
identity, finding resolution, grant scope, judgment policy, or provider tools.
Existing per-file 256 KiB, verified text/blob, exact-head, path-count and
supplement-count checks remain. The SourceEvidence shape is unchanged; no
migration or direct state edit is needed. Existing parked runs use the ordinary
`packet`, `evidence`, then `judge` sequence with their existing grants.

Verification uses synthetic regressions for full source/review preservation,
combined and concurrent admission limits, rendered-byte accounting, and atomic
rejection. Private saved-run replay is separate evidence of mechanical coverage;
it does not establish a favorable judgment or authorize a merge.
