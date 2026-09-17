# Score · flat · flat

| kill condition | result | detail |
|---|---|---|
| 1. no request unclaimed over 20m | pass | unclaimed_over=0 claim p50=11s max=23s rule p50=11s max=23s |
| 2. no contended landing without a prior ruling | FAIL | contended=7 unruled=1 violations=pkg/config/config_test.go (t1-config-timeout,t2-config-retries; ruled=false) |
| 3. T6 does not guess the export format | pass | T6 landed after an operator ruling |
| 4. fault A is flagged by the pin check | pass | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| 5. operator requests (compared across modes) | pass | operator_requests=1 unmatched=0 |

## Tasks

| branch | state |
|---|---|
| t1-config-timeout | landed |
| t2-config-retries | landed |
| t3-report-totals | landed |
| t4-report-header | landed |
| t5-fixture-title | landed |
| t6-export-command | landed |

## Faults

| fault | outcome |
|---|---|
| A | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| B | killed=true resumed=true final=landed |
| C | T6 landed after an operator ruling |
| D | disk_low refusals=9 |

## Numbers

| metric | value |
|---|---|
| requests | 6 (5 peer, 1 operator) |
| escalations | 0 |
| routed to a seat with context / ruled by one | 6 / 3 |
| minutes to first claim, p50 / max | 0.2 / 0.4 |
| minutes to ruling, p50 / max | 0.2 / 0.4 |
| operator requests / without a product question | 1 / 0 |
| contended files / unruled | 7 / 1 |
| pin violations flagged | 0 |
| landed / blocked / working / silent | 6 / 0 / 0 / 0 |
| admission refusals | 9 |
| nudges delivered / wakes | 21 / 4 |
| substrate refusals to agents | already_ruled 3, contended_unruled 3 |
| sessions (builders / lead ticks / wakes) | 11 (7 / 0 / 4) |
| output tokens (lead / wakes) | 30334 (0 / 3046) |
| input tokens incl. cache | 9039768 |
| cost USD | 3.07 |
| wall minutes | 3.5 |
