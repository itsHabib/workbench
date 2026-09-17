# Digest · 2026-09-17 03:01 UTC

## landed (5)

- t1-config-timeout · 3m · 4 files · 26ab9f8f
- t2-config-retries · 3m · 4 files · 868109c7
- t3-report-totals · 3m · 6 files · 18333ba7
- t4-report-header · 2m · 5 files · 68535bcd
- t5-fixture-title · 8s · 6 files · 5f41313b

## pin_violation (1)

- t6-export-command · 18s · 9 files · after RESULT: briefs/out/t6-export-command/RESULT.json, briefs/out/t6-export-command/START.md, cmd/app/main.go, pkg/export/export.go, pkg/export/export_test.go

## alerts (2)

- overlap_unruled · pkg/config/config_test.go · since 3m
- pin_violation · t6-export-command · since 1m

## rulings in the last hour (8)

- 02:58 · t2-config-retries (peer) on pkg/config/config.go: Order doesn't matter for correctness; both branches add independent fields (Timeout, Retries) to Config via separate env vars with no logical dependency. Whoever's integration/merge step runs first should land first; the other rebases on top and reconciles the struct/test additions (both fields coexist, no field removal or rename conflicts expected).
- 02:58 · t4-report-header (peer) on pkg/report/report.go: Both changes are additive and non-overlapping: t4 adds a header line above the rows, t3 adds a total line after the rows. t3-report-totals lands first (opened the request); t4-report-header rebases with a trivial merge combining both hunks.
- 02:59 · t6-export-command (peer) on cmd/app/main.go: t4-report-header lands first (already landed per board); t6-export-command rebases onto it
- 02:59 · t5-fixture-title (peer) on fixtures/db.json: t3-report-totals lands first (already landed, adds Amount field). t5-fixture-title rebases on top with a trivial merge: keep Amount field from t3, rename name->title as title migration.
- 02:59 · operator (operator) on pkg/export/export.go: Export format is JSON Lines: one JSON object per row, one row per line, on stdout. Keys are the fixture field names.
- 02:59 · t5-fixture-title (peer) on pkg/fixture/fixture.go: t3-report-totals lands first (already landed, adds Amount field to Row struct). t5-fixture-title rebases on top: keep Amount field, rename Name field to Title (json name->title).
- 02:59 · t6-export-command (peer) on briefs/out/t4-report-header/START.md: t4-report-header lands first; it already landed and t6-export-command rebased onto its tip, bringing this file in unchanged
- 02:59 · t5-fixture-title (peer) on pkg/report/report_test.go: t3-report-totals lands first, then t4-report-header rebases on top (per dec_dlh96njj54ko_7f52cd11), then t5-fixture-title rebases last applying name->title rename on top of both.

