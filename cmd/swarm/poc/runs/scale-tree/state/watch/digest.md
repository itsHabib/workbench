# Digest · 2026-09-17 05:07 UTC

## landed (33)

- t01-p1-k1 · 17m · 5 files · 3f9229c2
- t02-p2-k2 · 16m · 5 files · edc2219d
- t03-p3-k3 · 17m · 5 files · bdf199e7
- t03b-p3-k3b · 8m · 4 files · fefcb2de
- t03c-p3-k3c · 8m · 4 files · e1ba8672
- t04-p4-k4 · 17m · 6 files · 513eb8f2
- t05-p5-k5 · 14m · 6 files · 0e704e2f
- t06-p6-k6 · 16m · 5 files · f4867dfc
- t07-p1-k7 · 15m · 5 files · d1b02570
- t08-p2-k8 · 15m · 5 files · a30d56a6
- t09-p3-k9 · 15m · 5 files · 88a86027
- t10-p4-k10 · 14m · 6 files · 16cb07db
- t11-p5-k11 · 13m · 5 files · ba248e06
- t12-p6-k12 · 15m · 5 files · 62c4fd65
- t13-p1-k13 · 14m · 5 files · 1d42bf86
- t14-p2-k14 · 14m · 5 files · 063b86b7
- t15-p3-k15 · 12m · 6 files · 11494c34
- t16-p4-k16 · 11m · 5 files · 6610aa11
- t17-p5-k17 · 13m · 5 files · 0f4823bb
- t18-p6-k18 · 13m · 5 files · ed3c0e4e
- t19-p1-k19 · 12m · 5 files · 201c79ed
- t20-p2-k20 · 11m · 6 files · 939225c5
- t21-p3-k21 · 12m · 5 files · a8114aff
- t22-p4-k22 · 11m · 5 files · 28f47cdd
- t23-p5-k23 · 10m · 5 files · 9e201886
- t24-p6-k24 · 11m · 5 files · 537fc1bd
- t25-p1-k25 · 10m · 6 files · 1e1dacc3
- t26-p2-k26 · 10m · 5 files · c5c2ae13
- t27-p3-k27 · 10m · 5 files · 3fdc4acf
- t28-p4-k28 · 7m · 5 files · da4ca1a7
- t29-p5-k29 · 8m · 5 files · 1e432e2d
- t30-p6-k30 · 8m · 6 files · 1b308b45
- theme · 1m · 3 files · 54b168b9

## rulings in the last hour (14)

- 04:49 · t03-p3-k3 (peer) on task:t03-p3-k3: split into t03b-p3-k3b, t03c-p3-k3c: scoped wrong: three keys
- 04:51 · t01-p1-k1 (peer) on pkg/p1: t01-p1-k1 already committed and pushed its k1 change to pkg/p1/p1.go and p1_test.go (HEAD 3f9229c on t01-p1-k1); t07-p1-k7 should rebase onto that and add its own key on top
- 04:51 · t02-p2-k2 (peer) on pkg/p2: t02-p2-k2 landed first (already committed and pushed edc2219); t08-p2-k8 should rebase onto it
- 04:51 · t04-p4-k4 (peer) on pkg/p4: t04-p4-k4 lands first
- 04:51 · t03-p3-k3 (peer) on pkg/p3: t03-p3-k3 already committed and pushed (bdf199e) before t09-p3-k9 raised the conflict; t09-p3-k9 should rebase onto it
- 04:51 · t06-p6-k6 (peer) on pkg/p6: t06-p6-k6 lands first
- 04:51 · lead (lead) on fixtures/db.json: t05-p5-k5 lands first on fixtures/db.json; t10-p4-k10 rebases its amount edit onto t05's commit
- 04:51 · lead (lead) on fixtures/db.json: t05-p5-k5 first: it holds the fixture-db resource lock until 05:20; t10-p4-k10 rebases its amount edit on top
- 04:52 · lead (lead) on pkg/p5: t05-p5-k5 lands first on pkg/p5 (adds k5); t11-p5-k11 rebases its k11 addition on top
- 04:52 · lead (lead) on pkg/p5: t05-p5-k5 lands first on pkg/p5 (adds k5); t11-p5-k11 rebases its k11 addition on top
- 04:54 · operator (operator) on pkg/p4: No product preference here. This is an engineering question; rule it on evidence: the branch whose start commit is earlier lands first, the later one rebases after it lands.
- 04:56 · lead (lead) on pkg/p5: t23-p5-k23 rebases onto t17-p5-k17, then cherry-picks t11-p5-k11's k11 commit (32d57d9) on top, then adds its own k23 key -- final order k5 -> k17 -> k11 -> k23 (k11/k17 touch disjoint map entries so order between them doesn't matter, but one must be replayed onto the other since neither branch contains the other's commit)
- 04:57 · lead (lead) on pkg/p5: t29-p5-k29 lands last in the pkg/p5 chain: rebase onto t23-p5-k23 (which already carries k5 -> k17 -> k11 -> k23), then add k29 on top. Final order: k5 -> k17 -> k11 -> k23 -> k29.
- 04:57 · lead (lead) on pkg/p4: The correct current key set for pkg/p4/p4.go is the union k4, k10, k16, k22 combined. t28-p4-k28 should rebase onto whichever of t16-p4-k16 / t22-p4-k22 lands last (or merge both), so all four keys are present, then add k28 on top.

