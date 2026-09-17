# Digest · 2026-09-17 03:11 UTC

## landed (6)

- t1-config-timeout · 2m · 4 files · fb2f1fea
- t2-config-retries · 1m · 4 files · d291f20a
- t3-report-totals · 2m · 6 files · 8f0a1cd3
- t4-report-header · 1m · 5 files · e50c0477
- t5-fixture-title · 8s · 6 files · 73013374
- t6-export-command · 39s · 5 files · 542e5b32

## alerts (1)

- overlap_unruled · pkg/config/config_test.go · since 2m

## rulings in the last hour (6)

- 03:08 · t3-report-totals (peer) on pkg/report/report.go: t3-report-totals lands first: it already committed and pushed both the report.go/report_test.go changes and RESULT.json before this overlap was flagged. t4-report-header should rebase on t3's head.
- 03:08 · t3-report-totals (peer) on pkg/report/report_test.go: t3-report-totals lands first: it already committed and pushed both the report.go/report_test.go changes and RESULT.json before this overlap was flagged. t4-report-header should rebase on t3's head.
- 03:09 · t2-config-retries (peer) on pkg/config/config.go: t1-config-timeout lands first
- 03:10 · operator (operator) on pkg/export/export.go: Export format is JSON Lines: one JSON object per row, one row per line, on stdout. Keys are the fixture field names.
- 03:10 · t5-fixture-title (peer) on pkg/fixture/fixture.go,fixtures/db.json: t3-report-totals lands first: it is already landed (committed+pushed, RESULT.json recorded) and added the amount field to fixture.go/db.json. t5-fixture-title should rebase onto t3-report-totals's tip before editing these files.
- 03:10 · t4-report-header (peer) on cmd/app/main.go: t4-report-header lands first

