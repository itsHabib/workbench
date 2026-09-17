# Board · 2026-09-17 03:01 UTC · base main @ aa2c6f12

| branch | state | tip age | files | overlaps | note |
|---|---|---|---|---|---|
| t1-config-timeout | landed | 3m | 4 | t2-config-retries (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t2-config-retries | landed | 3m | 4 | t1-config-timeout (UNRULED: pkg/config/config.go, pkg/config/config_test.go) |  |
| t3-report-totals | landed | 3m | 6 | t5-fixture-title (ruled: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (ruled: pkg/report/report.go, pkg/report/report_test.go); t6-export-command (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t4-report-header | landed | 2m | 5 | t6-export-command (ruled: briefs/out/t4-report-header/START.md, cmd/app/main.go, pkg/report/report.go, pkg/report/report_test.go); t3-report-totals (ruled: pkg/report/report.go, pkg/report/report_test.go); t5-fixture-title (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t5-fixture-title | landed | 8s | 6 | t3-report-totals (ruled: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go, pkg/report/report_test.go); t4-report-header (ruled: pkg/report/report.go, pkg/report/report_test.go); t6-export-command (ruled: pkg/report/report.go, pkg/report/report_test.go) |  |
| t6-export-command | pin_violation | 18s | 9 | t4-report-header (ruled: briefs/out/t4-report-header/START.md, cmd/app/main.go, pkg/report/report.go, pkg/report/report_test.go); t3-report-totals (ruled: pkg/report/report.go, pkg/report/report_test.go); t5-fixture-title (ruled: pkg/report/report.go, pkg/report/report_test.go) | changed after RESULT: briefs/out/t6-export-command/RESULT.json, briefs/out/t6-export-command/START.md, cmd/app/main.go, pkg/export/export.go, pkg/export/export_test.go |

## Contended files

| file | branches | ruling |
|---|---|---|
| briefs/out/t4-report-header/START.md | t4-report-header, t6-export-command | dec_dlh97oaghx80_99bbe749 |
| cmd/app/main.go | t4-report-header, t6-export-command | dec_dlh977c0zgk8_e1f1a228 |
| fixtures/db.json | t3-report-totals, t5-fixture-title | dec_dlh97g42my28_e729b80d |
| pkg/config/config.go | t1-config-timeout, t2-config-retries | dec_dlh96f7s1pbc_fb5e86e3 |
| pkg/config/config_test.go | t1-config-timeout, t2-config-retries | none |
| pkg/fixture/fixture.go | t3-report-totals, t5-fixture-title | dec_dlh97mco9efc_135d67d4 |
| pkg/report/report.go | t3-report-totals, t4-report-header, t5-fixture-title, t6-export-command | dec_dlh96njj54ko_7f52cd11 |
| pkg/report/report_test.go | t3-report-totals, t4-report-header, t5-fixture-title, t6-export-command | dec_dlh97qiyq0rc_d1c3543c |

Requests: 0 open (none); oldest open 0s.
