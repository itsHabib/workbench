# Recorded judgment packet coverage

Three parked runs exposed evidence requirements that the supported collector
could not satisfy. This change fixes packet construction and keeps judgment,
grant authority, and recorded evidence separate.

## Corrections

Each review has an evidence ID and recorded index. Packet construction now
counts a complete rendered review by that identity once. A review omitted from
both bounded sections still makes the packet incomplete. Unknown-head reviews
remain present; a newer clean review or a SHA written in prose cannot erase an
unresolved finding.

Structured anchors, verifier finding locations, and unambiguous file/line
references still require coverage. An exact path wins over other files with the
same basename. Ambiguous basenames and unchanged bare tokens remain visible as
source hints with candidate paths; they do not select every candidate or turn a
command name into a required executable blob. These diagnostics do not resolve
findings. Packet completeness reports mechanical coverage, not a favorable
judgment.

Complete supplemental source is bounded at 512 KiB across the run. The existing
256 KiB per-file limit, UTF-8/NUL checks, Git blob verification, exact subject
binding, 32-path limit, three-supplement limit, and atomic audit append remain.
The separate required-diff budget stays 256 KiB. Each review section stays
bounded at 64 KiB; counting an already rendered review does not consume the
second section's budget again. A failed collection appends no partial evidence.

## Offline replay

Recorded evidence, verdicts, and escalations were copied read-only on
2026-09-13. Replay called the pure `verify.JudgmentPacket` function on these
copies. Missing text was read with `git show <exact-head>:<path>` from existing
local Git objects, checked against its Git blob hash, and added only to a
disposable in-memory artifact list. No live run or custody file was changed.
The private source snapshots remain local; committed regression tests use
synthetic data.

| Recorded subject | Original failure | Replay with this change |
|---|---|---|
| Rooms #120, `da927d501c0f58e7b649dcf83f63492883f36569` | Three reviews reported missing despite already being rendered; the cited whole files exceeded the total source budget. | Four required files add 352,590 bytes to 64,959 bytes already recorded: 417,549 total, below 512 KiB. Complete after that one local supplement. |
| RoxIQ #246, `84fabb918628c6a954e5a1d75fdafebb760a0465` | Prose inference selected unrelated same-basename files and exceeded the total source budget. | Ten required files add 293,761 bytes to 12,846 already recorded: 306,607 total. Ambiguous candidates remain named as hints. Complete after one local supplement. |
| RoxIQ #252, `5e55a555d36f09ab7d90ee813e74c0af81dce04d` | A bare command mention required an unchanged 9,500,802-byte binary that the text collector correctly refuses. | Existing 200,050 bytes of source suffice. The command remains a hint and its review stays visible. Complete without additional source. |

Final context sizes were 613,300, 421,330, and 331,995 bytes respectively. Context
includes reviews and recorded diffs as well as supplemental source, so it is not
the source-budget measurement. These replays establish mechanical completeness;
they do not establish a provider's judgment, merge readiness, or live recovery.

## Verification and operator use

`go test -race ./cmd/gate/...`, Gate vet/lint, and a Gate build passed. Regressions
cover duplicate review accounting, genuinely unrepresented reviews, unresolved
precise findings, ambiguous names, explicit binary anchors, exact-path
precedence, atomic overflow refusal, invalid text, and oversized individual
files. Existing subject, expiry, terminal-run, and race checks remain exercised.

After a separately authorized installation, the owner can inspect the same
parked run with `gate packet -run RUN -state STATE`, collect the remaining
required paths with `gate evidence -run RUN -grant EXISTING_GRANT -state STATE`,
and inspect the packet again. Existing judgment still uses that run and its
authorized grant. This patch adds no installation, automatic judgment, grant
minting, or merge action.
