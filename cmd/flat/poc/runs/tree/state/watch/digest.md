# Digest · 2026-09-17 03:17 UTC

## landed (5)

- t1-config-timeout · 2m · 4 files · 47d40de6
- t3-report-totals · 2m · 6 files · a8f915fb
- t4-report-header · 5m · 5 files · a1c17f6a
- t5-fixture-title · 2m · 8 files · 3ce8089d
- t6-export-command · 2m · 5 files · 27fdaa2d

## working (1)

- t2-config-retries · 5m · 3 files

## alerts (4)

- overlap_unruled · briefs/out/t3-report-totals/START.md · since 2m
- overlap_unruled · pkg/config/config_test.go · since 5m
- overlap_unruled · pkg/report/report_test.go · since 4m
- request_unclaimed · req_dlh9hcxmlj3s_c79ed325 · since 0s

## requests open (2)

| id | from | needs | age | status | question |
|---|---|---|---|---|---|
| req_dlh9hcxmlj3s_c79ed325 | t2-config-retries | peer | 5m | open | t1-config-timeout and t2-config-retries both change pkg/config/config.go and config_test.go with no ruling on order - which lands first? |
| req_dlh9jqisic68_665ae338 | t3-report-totals | peer | 2m | open | who lands first on briefs/out/t3-report-totals/START.md? |

## rulings in the last hour (6)

- 03:12 · lead (lead) on pkg/config/config.go: t2-config-retries lands first, then t1-config-timeout rebases on it
- 03:13 · t4-report-header (peer) on pkg/report/report.go: t4-report-header
- 03:14 · lead (lead) on fixtures/db.json: t3-report-totals lands first (already landed at 2bf0e417); t5-fixture-title rebases onto it before landing
- 03:14 · lead (lead) on cmd/app/main.go: t4-report-header lands first (already landed at a1c17f6a); t6-export-command rebases onto it before landing
- 03:14 · operator (operator) on pkg/export/export.go: Export format is JSON Lines: one JSON object per row, one row per line, on stdout. Keys are the fixture field names.
- 03:14 · lead (lead) on pkg/fixture/fixture.go: yes, proceed - rebasing onto landed t3-report-totals tip 2bf0e417 satisfies the prior ordering ruling dec_dlh9ip3pnfr4_2c889707

