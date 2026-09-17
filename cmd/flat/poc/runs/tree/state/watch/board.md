# Board · 2026-09-17 03:17 UTC · base main @ 1c8f92a2

| branch | state | tip age | files | overlaps | note |
|---|---|---|---|---|---|
| t1-config-timeout | landed | 2m | 4 | t2-config-retries (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t2-config-retries | working | 5m | 3 | t1-config-timeout (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t3-report-totals | landed | 2m | 6 | t5-fixture-title (UNRULED: briefs/out/t3-report-totals/START.md, fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (UNRULED: pkg/report/report.go, pkg/report/report_test.go) |  |
| t4-report-header | landed | 5m | 5 | t6-export-command (ruled: cmd/app/main.go); t3-report-totals (UNRULED: pkg/report/report.go, pkg/report/report_test.go); t5-fixture-title (UNRULED: pkg/report/report.go, pkg/report/report_test.go) |  |
| t5-fixture-title | landed | 2m | 8 | t3-report-totals (UNRULED: briefs/out/t3-report-totals/START.md, fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (UNRULED: pkg/report/report.go, pkg/report/report_test.go) |  |
| t6-export-command | landed | 2m | 5 | t4-report-header (ruled: cmd/app/main.go) |  |

## Contended files

| file | branches | ruling |
|---|---|---|
| briefs/out/t3-report-totals/START.md | t3-report-totals, t5-fixture-title | none |
| cmd/app/main.go | t4-report-header, t6-export-command | dec_dlh9ir8yq4wg_6d5c6b74 |
| fixtures/db.json | t3-report-totals, t5-fixture-title | dec_dlh9ip3pnfr4_2c889707 |
| pkg/config/config.go | t1-config-timeout, t2-config-retries | dec_dlh9hp4eg0jk_7e0070f5 |
| pkg/config/config_test.go | t1-config-timeout, t2-config-retries | none |
| pkg/fixture/fixture.go | t3-report-totals, t5-fixture-title | dec_dlh9j1hmqq2w_42f58ca5 |
| pkg/report/report.go | t3-report-totals, t4-report-header, t5-fixture-title | dec_dlh9i7w1hru8_2f529fc0 |
| pkg/report/report_test.go | t3-report-totals, t4-report-header, t5-fixture-title | none |

Requests: 2 open (2 peer); oldest open 5m.
