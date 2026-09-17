# Board · 2026-09-17 03:11 UTC · base main @ 3b0a6302

| branch | state | tip age | files | overlaps | note |
|---|---|---|---|---|---|
| t1-config-timeout | landed | 2m | 4 | t2-config-retries (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t2-config-retries | landed | 1m | 4 | t1-config-timeout (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t3-report-totals | landed | 2m | 6 | t5-fixture-title (ruled: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t4-report-header | landed | 1m | 5 | t6-export-command (ruled: cmd/app/main.go); t3-report-totals (ruled: pkg/report/report.go, pkg/report/report_test.go); t5-fixture-title (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t5-fixture-title | landed | 8s | 6 | t3-report-totals (ruled: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t6-export-command | landed | 39s | 5 | t4-report-header (ruled: cmd/app/main.go) |  |

## Contended files

| file | branches | ruling |
|---|---|---|
| cmd/app/main.go | t4-report-header, t6-export-command | dec_dlh9fl2qfq4g_b3ddd17a |
| fixtures/db.json | t3-report-totals, t5-fixture-title | dec_dlh9fknm44rk_0fd3e025 |
| pkg/config/config.go | t1-config-timeout, t2-config-retries | dec_dlh9exnocwqg_764005da |
| pkg/config/config_test.go | t1-config-timeout, t2-config-retries | none |
| pkg/fixture/fixture.go | t3-report-totals, t5-fixture-title | dec_dlh9fknm44rk_0fd3e025 |
| pkg/report/report.go | t3-report-totals, t4-report-header, t5-fixture-title | dec_dlh9eku6541k_86772f25 |
| pkg/report/report_test.go | t3-report-totals, t4-report-header, t5-fixture-title | dec_dlh9ememy3h4_881a0480 |

Requests: 0 open (none); oldest open 0s.
