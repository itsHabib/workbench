# Score · tree · tree

| kill condition | result | detail |
|---|---|---|
| 1. no request unclaimed over 20m | pass | unclaimed_over=0 claim p50=33s max=55s rule p50=33s max=55s |
| 2. no contended landing without a prior ruling | pass | contended=13 unruled=0 violations= |
| 3. T6 does not guess the export format | pass | T6 not in this run |
| 4. fault A is flagged by the pin check | pass | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| 5. operator requests (compared across modes) | pass | operator_requests=1 unmatched=1 |

## Tasks

| branch | state |
|---|---|
| t01-p1-k1 | landed |
| t02-p2-k2 | landed |
| t03-p3-k3 | landed |
| t03b-p3-k3b | landed |
| t03c-p3-k3c | landed |
| t04-p4-k4 | landed |
| t05-p5-k5 | landed |
| t06-p6-k6 | landed |
| t07-p1-k7 | landed |
| t08-p2-k8 | landed |
| t09-p3-k9 | landed |
| t10-p4-k10 | landed |
| t11-p5-k11 | landed |
| t12-p6-k12 | landed |
| t13-p1-k13 | landed |
| t14-p2-k14 | landed |
| t15-p3-k15 | landed |
| t16-p4-k16 | landed |
| t17-p5-k17 | landed |
| t18-p6-k18 | landed |
| t19-p1-k19 | landed |
| t20-p2-k20 | landed |
| t21-p3-k21 | landed |
| t22-p4-k22 | landed |
| t23-p5-k23 | landed |
| t24-p6-k24 | landed |
| t25-p1-k25 | landed |
| t26-p2-k26 | landed |
| t27-p3-k27 | landed |
| t28-p4-k28 | landed |
| t29-p5-k29 | landed |
| t30-p6-k30 | landed |
| theme | landed |

## Faults

| fault | outcome |
|---|---|
| A | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| B | killed=true resumed=true final=landed |
| C | T6 not in this run |
| D | disk_low refusals=6 |
| E | t03-p3-k3 split into 2 (2 landed); parent final=landed |

Consolidation: theme landed, 38 branches merged, tests pass

## Numbers

| metric | value |
|---|---|
| requests | 13 (11 peer, 2 lead) |
| escalations | 2 |
| routed to a seat with context / ruled by one | 9 / 8 |
| minutes to first claim, p50 / max | 0.6 / 0.9 |
| minutes to ruling, p50 / max | 0.6 / 0.9 |
| operator requests / without a product question | 1 / 1 |
| contended files / unruled | 13 / 0 |
| pin violations flagged | 0 |
| landed / red / blocked / working / silent | 33 / 0 / 0 / 0 / 0 |
| admission refusals | 7 |
| nudges delivered / wakes | 58 / 13 |
| substrate refusals to agents | already_ruled 5, contended_unruled 4, held_by_other 1, must_supersede 3, outranked 1, tier_too_low 1 |
| sessions (builders / lead ticks / wakes) | 55 (34 / 8 / 13) |
| output tokens (lead / wakes) | 156122 (18729 / 6810) |
| input tokens incl. cache | 63211217 |
| cost USD | 19.39 |
| wall minutes | 16.8 |
