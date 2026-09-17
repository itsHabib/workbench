# Score · flat · flat

| kill condition | result | detail |
|---|---|---|
| 1. no request unclaimed over 20m | pass | unclaimed_over=0 claim p50=10s max=34s rule p50=10s max=38s |
| 2. no contended landing without a prior ruling | FAIL | contended=17 unruled=0 violations=briefs/out/t04-p4-k4/START.md (t04-p4-k4,t10-p4-k10; ruled=true); briefs/out/t05-p5-k5/START.md (t10-p4-k10,t15-p3-k15; ruled=true); briefs/out/t11-p5-k11/START.md (t05-p5-k5,t11-p5-k11; ruled=true); cmd/app/main.go (t04-p4-k4,t10-p4-k10; ruled=true); pkg/p6/p6.go (t06-p6-k6,t12-p6-k12,t18-p6-k18,t24-p6-k24,t30-p6-k30; ruled=true); pkg/p6/p6_test.go (t06-p6-k6,t12-p6-k12,t18-p6-k18,t24-p6-k24,t30-p6-k30; ruled=true) |
| 3. T6 does not guess the export format | pass | T6 not in this run |
| 4. fault A is flagged by the pin check | pass | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| 5. operator requests (compared across modes) | pass | operator_requests=0 unmatched=0 |

## Tasks

| branch | state |
|---|---|
| t01-p1-k1 | landed |
| t02-p2-k2 | landed |
| t03-p3-k3 | landed |
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
| t21-p3-k21 | working |
| t22-p4-k22 | landed |
| t23-p5-k23 | landed |
| t24-p6-k24 | landed |
| t25-p1-k25 | landed |
| t26-p2-k26 | landed |
| t27-p3-k27 | landed |
| t28-p4-k28 | landed |
| t29-p5-k29 | landed |
| t30-p6-k30 | landed |

## Faults

| fault | outcome |
|---|---|
| A | T4 landed with the bump pinned by RESULT.json (absorbed: flagged, then re-pinned) |
| B | killed=true resumed=true final=landed |
| C | T6 not in this run |
| D | disk_low refusals=6 |

Consolidation: no consolidator ran

## Numbers

| metric | value |
|---|---|
| requests | 13 (13 peer) |
| escalations | 0 |
| routed to a seat with context / ruled by one | 13 / 1 |
| minutes to first claim, p50 / max | 0.2 / 0.6 |
| minutes to ruling, p50 / max | 0.2 / 0.6 |
| operator requests / without a product question | 0 / 0 |
| contended files / unruled | 17 / 0 |
| pin violations flagged | 0 |
| landed / red / blocked / working / silent | 29 / 0 / 0 / 1 / 0 |
| admission refusals | 9 |
| nudges delivered / wakes | 50 / 17 |
| substrate refusals to agents | already_ruled 9, contended_unruled 6, must_supersede 1 |
| sessions (builders / lead ticks / wakes) | 49 (32 / 0 / 17) |
| output tokens (lead / wakes) | 131658 (0 / 19585) |
| input tokens incl. cache | 51977986 |
| cost USD | 17.15 |
| wall minutes | 13.1 |
