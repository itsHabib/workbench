# Digest · 2026-09-17 04:49 UTC

## landed (33)

- t01-p1-k1 · 15m · 5 files · 5d4534c4
- t02-p2-k2 · 15m · 5 files · c7618077
- t03-p3-k3 · 15m · 5 files · 27ca74af
- t03b-p3-k3b · 8m · 4 files · eeb17082
- t03c-p3-k3c · 7m · 4 files · 15caee31
- t04-p4-k4 · 15m · 6 files · e62d33ac
- t05-p5-k5 · 12m · 6 files · 39562a43
- t06-p6-k6 · 14m · 5 files · 6b53a339
- t07-p1-k7 · 14m · 5 files · 24e2649e
- t08-p2-k8 · 14m · 5 files · c54809b3
- t09-p3-k9 · 14m · 5 files · 4be89844
- t10-p4-k10 · 11m · 6 files · b9acb0e9
- t11-p5-k11 · 12m · 5 files · 76e755b3
- t12-p6-k12 · 13m · 5 files · 221d73af
- t13-p1-k13 · 13m · 5 files · d5506452
- t14-p2-k14 · 13m · 5 files · d876a7e7
- t15-p3-k15 · 12m · 6 files · a72fae24
- t16-p4-k16 · 12m · 5 files · e25fb54c
- t17-p5-k17 · 11m · 5 files · adfd71fb
- t18-p6-k18 · 12m · 5 files · 8eb4656e
- t19-p1-k19 · 11m · 5 files · f08272ae
- t20-p2-k20 · 10m · 6 files · 01ef7465
- t21-p3-k21 · 10m · 5 files · ea01353f
- t22-p4-k22 · 10m · 5 files · 67ea13a4
- t23-p5-k23 · 10m · 5 files · 328dbc5b
- t24-p6-k24 · 9m · 5 files · fae8cd6f
- t25-p1-k25 · 8m · 6 files · 1d3a9c8c
- t26-p2-k26 · 9m · 5 files · 0ed9d522
- t27-p3-k27 · 9m · 5 files · 9bf435fb
- t28-p4-k28 · 7m · 5 files · 9f6d089e
- t29-p5-k29 · 8m · 5 files · 032ccc01
- t30-p6-k30 · 5m · 6 files · e03b1c4f
- theme · 46s · 2 files · 23bdc45a

## rulings in the last hour (8)

- 04:32 · t03-p3-k3 (peer) on task:t03-p3-k3: split into t03b-p3-k3b, t03c-p3-k3c: scoped wrong: three keys
- 04:34 · t08-p2-k8 (peer) on pkg/p2: t02-p2-k2 already landed on main per board; t08-p2-k8 rebases on top and adds k8
- 04:34 · t07-p1-k7 (peer) on pkg/p1: t01-p1-k1 already landed on main per board; rebase onto it and land k7 second
- 04:34 · t09-p3-k9 (peer) on pkg/p3: t03-p3-k3 already landed (commit 4c7f206, RESULT.json recorded at 27ca74a); rebase onto it and add k9 on top
- 04:34 · t11-p5-k11 (peer) on pkg/p5: lower task number lands first, consistent with p1/p2 precedent (t01 before t07, t02 before t08); k11 rebases on k5's change to Values().
- 04:34 · t10-p4-k10 (peer) on pkg/p4: t04-p4-k4 already landed (RESULT.json committed, k4 key merged) before t10-p4-k10 started work; t10-p4-k10 rebases onto it and adds k10.
- 04:34 · t06-p6-k6 (peer) on pkg/p6: t06-p6-k6 already landed with k6 added; t12-p6-k12 should rebase on top and add k12 next
- 04:35 · t14-p2-k14 (peer) on fixtures/db.json: t05-p5-k5 currently holds the fixture-db resource lock (per board Resources line); it should land first since it already has exclusive access, then t10-p4-k10 rebases on top.

