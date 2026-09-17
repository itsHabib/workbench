# Score · tree · tree

| kill condition | result | detail |
|---|---|---|
| 1. no request unclaimed over 20m | pass | unclaimed_over=0 claim p50=13s max=135s rule p50=13s max=50s |
| 2. no contended landing without a prior ruling | FAIL | contended=8 unruled=3 violations=briefs/out/t3-report-totals/START.md (t3-report-totals,t5-fixture-title; ruled=false); pkg/report/report_test.go (t3-report-totals,t4-report-header,t5-fixture-title; ruled=false) |
| 3. T6 does not guess the export format | pass | T6 landed after an operator ruling |
| 4. fault A is flagged by the pin check | pass | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| 5. operator requests (compared across modes) | pass | operator_requests=1 unmatched=0 |

## Tasks

| branch | state |
|---|---|
| t1-config-timeout | landed |
| t2-config-retries | working |
| t3-report-totals | landed |
| t4-report-header | landed |
| t5-fixture-title | landed |
| t6-export-command | landed |

## Faults

| fault | outcome |
|---|---|
| A | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| B | killed=true resumed=true final=working |
| C | T6 landed after an operator ruling |
| D | disk_low refusals=9 |

## Numbers

| metric | value |
|---|---|
| requests | 8 (3 peer, 4 lead, 1 operator) |
| escalations | 0 |
| routed to a seat with context / ruled by one | 8 / 6 |
| minutes to first claim, p50 / max | 0.2 / 2.3 |
| minutes to ruling, p50 / max | 0.2 / 0.8 |
| operator requests / without a product question | 1 / 0 |
| contended files / unruled | 8 / 3 |
| pin violations flagged | 0 |
| landed / blocked / working / silent | 5 / 0 / 1 / 0 |
| admission refusals | 9 |
| nudges delivered / wakes | 30 / 5 |
| substrate refusals to agents | contended_unruled 4, outranked 1 |
| sessions (builders / lead ticks / wakes) | 14 (7 / 2 / 5) |
| output tokens (lead / wakes) | 36287 (4614 / 6256) |
| input tokens incl. cache | 11994473 |
| cost USD | 4.36 |
| wall minutes | 4.0 |
