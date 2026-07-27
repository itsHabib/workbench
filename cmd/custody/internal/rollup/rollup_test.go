package rollup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// line builds one schema-v1 log line. Overrides are merged over the base map.
func line(t *testing.T, overrides map[string]any) string {
	t.Helper()
	base := map[string]any{
		"schema_version": 1,
		"ts":             "2026-07-26T10:00:00Z",
		"request_id":     "req-1",
		"verdict":        "pass",
		"method":         "GET",
		"latency_ms":     100,
	}
	for k, v := range overrides {
		base[k] = v
	}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	return string(data)
}

func TestAggregateCounts(t *testing.T) {
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira", "grant_id": "g1", "upstream_status": 200, "latency_ms": 100}),
		line(t, map[string]any{"key": "jira", "grant_id": "g1", "upstream_status": 200, "latency_ms": 300}),
		line(t, map[string]any{"key": "jira", "grant_id": "g2", "verdict": "denied", "method": "POST", "canonical_target": "/rest/api/2/issue", "latency_ms": 5}),
		line(t, map[string]any{"verdict": "refused", "latency_ms": 1}),
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Total != 4 {
		t.Fatalf("Total = %d, want 4", sum.Total)
	}
	if sum.ByVerdict["pass"] != 2 || sum.ByVerdict["denied"] != 1 || sum.ByVerdict["refused"] != 1 {
		t.Fatalf("ByVerdict = %v", sum.ByVerdict)
	}
	jira := sum.Keys["jira"]
	if jira == nil || jira.Total != 3 {
		t.Fatalf("jira summary = %+v", jira)
	}
	if jira.DistinctGrants != 2 {
		t.Fatalf("DistinctGrants = %d, want 2", jira.DistinctGrants)
	}
	if jira.UpstreamStatus["200"] != 2 {
		t.Fatalf("UpstreamStatus = %v", jira.UpstreamStatus)
	}
	if got := jira.DeniedTargets; len(got) != 1 || got[0].Target != "POST /rest/api/2/issue" || got[0].Count != 1 {
		t.Fatalf("DeniedTargets = %v", got)
	}
	none := sum.Keys["(none)"]
	if none == nil || none.ByVerdict["refused"] != 1 {
		t.Fatalf("(none) summary = %+v", none)
	}
	if sum.Window.From != "2026-07-26T10:00:00Z" || sum.Window.To != "2026-07-26T10:00:00Z" {
		t.Fatalf("Window = %+v", sum.Window)
	}
}

func TestAggregateLatencyPercentiles(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, line(t, map[string]any{"key": "jira", "latency_ms": i}))
	}
	sum, err := Aggregate(strings.NewReader(strings.Join(lines, "\n")), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	lat := sum.Keys["jira"].Latency
	if lat.P50 != 50 || lat.P95 != 95 || lat.Max != 100 {
		t.Fatalf("Latency = %+v, want p50=50 p95=95 max=100", lat)
	}
}

func TestAggregateSingleValuePercentiles(t *testing.T) {
	sum, err := Aggregate(strings.NewReader(line(t, map[string]any{"key": "jira", "latency_ms": 42})), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	lat := sum.Keys["jira"].Latency
	if lat.P50 != 42 || lat.P95 != 42 || lat.Max != 42 {
		t.Fatalf("Latency = %+v, want all 42", lat)
	}
}

func TestAggregateSinceWindowIsLogAnchored(t *testing.T) {
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira", "ts": "2026-07-20T10:00:00Z"}),
		line(t, map[string]any{"key": "jira", "ts": "2026-07-26T09:00:00Z"}),
		line(t, map[string]any{"key": "jira", "ts": "2026-07-26T10:00:00Z"}),
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), 24*time.Hour)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Total != 2 {
		t.Fatalf("Total = %d, want 2 (the 07-20 record is outside 24h of the newest)", sum.Total)
	}
	if sum.Window.From != "2026-07-26T09:00:00Z" {
		t.Fatalf("Window.From = %s, want the windowed span", sum.Window.From)
	}
}

func TestAggregateWindowMixedFractionalTimestamps(t *testing.T) {
	// RFC3339Nano drops trailing zeros, so whole-second stamps ("…00Z") sit
	// alongside fractional ones ("…00.5Z") — and the strings sort in the wrong
	// chronological order. The window must compare parsed times.
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira", "ts": "2026-07-26T10:00:00Z"}),
		line(t, map[string]any{"key": "jira", "ts": "2026-07-26T10:00:00.5Z"}),
		line(t, map[string]any{"key": "jira", "ts": "2026-07-25T09:00:00.5Z"}),
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), time.Hour)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Total != 2 {
		t.Fatalf("Total = %d, want 2 (newest is the fractional 10:00:00.5Z; the 07-25 record is out)", sum.Total)
	}
	if sum.Window.From != "2026-07-26T10:00:00Z" || sum.Window.To != "2026-07-26T10:00:00.5Z" {
		t.Fatalf("Window = %+v, want chronological boundaries, not string-order ones", sum.Window)
	}
}

func TestDeniedWithoutTargetBucketsAsUnrecorded(t *testing.T) {
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira", "verdict": "denied", "method": "POST"}),
		line(t, map[string]any{"key": "jira", "verdict": "denied", "method": "POST"}),
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	got := sum.Keys["jira"].DeniedTargets
	if len(got) != 1 || got[0].Target != "POST (target unrecorded)" || got[0].Count != 2 {
		t.Fatalf("DeniedTargets = %v, want one explicit unrecorded bucket", got)
	}
}

func TestAggregateSkipsBadLines(t *testing.T) {
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira"}),
		"{torn json",
		line(t, map[string]any{"key": "jira", "schema_version": 2}),
		line(t, map[string]any{"key": "jira", "ts": "not-a-time"}),
		"",
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Total != 1 || sum.Malformed != 2 || sum.UnsupportedSchema != 1 {
		t.Fatalf("Total=%d Malformed=%d UnsupportedSchema=%d, want 1/2/1", sum.Total, sum.Malformed, sum.UnsupportedSchema)
	}
}

func TestDeniedTargetsOrderedAndCapped(t *testing.T) {
	var lines []string
	for i := 0; i < 12; i++ {
		target := "/rest/api/3/path-" + string(rune('a'+i))
		lines = append(lines, line(t, map[string]any{"key": "jira", "verdict": "denied", "canonical_target": target}))
	}
	for i := 0; i < 3; i++ {
		lines = append(lines, line(t, map[string]any{"key": "jira", "verdict": "denied", "canonical_target": "/rest/api/3/hot", "query_keys": []string{"jql"}}))
	}
	sum, err := Aggregate(strings.NewReader(strings.Join(lines, "\n")), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	got := sum.Keys["jira"].DeniedTargets
	if len(got) != maxDeniedTargets {
		t.Fatalf("len(DeniedTargets) = %d, want cap %d", len(got), maxDeniedTargets)
	}
	if got[0].Target != "GET /rest/api/3/hot" || got[0].Count != 3 {
		t.Fatalf("top denied = %+v, want the ×3 target first", got[0])
	}
	for i := 2; i < len(got); i++ {
		if got[i-1].Target > got[i].Target {
			t.Fatalf("ties not lexicographic at %d: %s > %s", i, got[i-1].Target, got[i].Target)
		}
	}
	if sum.Keys["jira"].DeniedQueryKeys["jql"] != 3 {
		t.Fatalf("DeniedQueryKeys = %v", sum.Keys["jira"].DeniedQueryKeys)
	}
}

func TestOversizedLineSkippedNotFatal(t *testing.T) {
	huge := line(t, map[string]any{"key": "jira", "canonical_target": strings.Repeat("a", maxLineBytes+1024)})
	log := huge + "\n" + line(t, map[string]any{"key": "jira"}) + "\n"
	sum, err := Aggregate(strings.NewReader(log), 0)
	if err != nil {
		t.Fatalf("Aggregate must skip an oversized line, not fail: %v", err)
	}
	if sum.Total != 1 || sum.Malformed != 1 {
		t.Fatalf("Total=%d Malformed=%d, want 1/1 (huge line skipped, next line still read)", sum.Total, sum.Malformed)
	}
}

func TestKeyCardinalityCapped(t *testing.T) {
	var lines []string
	for i := 0; i < maxKeys+20; i++ {
		lines = append(lines, line(t, map[string]any{"key": fmt.Sprintf("probe-%03d", i), "verdict": "refused"}))
	}
	sum, err := Aggregate(strings.NewReader(strings.Join(lines, "\n")), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sum.Keys) > maxKeys {
		t.Fatalf("len(Keys) = %d, want at most %d including the overflow bucket", len(sum.Keys), maxKeys)
	}
	other := sum.Keys[overflowKey]
	if other == nil || other.Total != 22 {
		t.Fatalf("overflow bucket = %+v, want the 22 past-cap keys folded in", other)
	}
	if sum.Total != maxKeys+20 {
		t.Fatalf("Total = %d — capping buckets must not drop records", sum.Total)
	}
}

func TestNoKeyBucketSurvivesTheCap(t *testing.T) {
	var lines []string
	for i := 0; i < maxKeys+10; i++ {
		lines = append(lines, line(t, map[string]any{"key": fmt.Sprintf("probe-%03d", i), "verdict": "refused"}))
	}
	lines = append(lines, line(t, map[string]any{"verdict": "refused"})) // no key resolved
	sum, err := Aggregate(strings.NewReader(strings.Join(lines, "\n")), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	none := sum.Keys[noKey]
	if none == nil || none.Total != 1 {
		t.Fatalf("(none) bucket = %+v — unknown-key pressure must never hide inside the overflow bucket", none)
	}
	if len(sum.Keys) > maxKeys {
		t.Fatalf("len(Keys) = %d, want at most %d including both sentinels", len(sum.Keys), maxKeys)
	}
}

func TestMethodAndQueryKeyCardinalityCapped(t *testing.T) {
	var lines []string
	for i := 0; i < maxMethods+5; i++ {
		lines = append(lines, line(t, map[string]any{"key": "jira", "method": fmt.Sprintf("M%02d", i)}))
	}
	for i := 0; i < maxQueryKeys+5; i++ {
		lines = append(lines, line(t, map[string]any{"key": "jira", "verdict": "denied", "method": "M00", "query_keys": []string{fmt.Sprintf("q%02d", i)}}))
	}
	sum, err := Aggregate(strings.NewReader(strings.Join(lines, "\n")), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	jira := sum.Keys["jira"]
	if len(jira.ByMethod) != maxMethods {
		t.Fatalf("len(ByMethod) = %d, want the cap %d", len(jira.ByMethod), maxMethods)
	}
	if jira.MethodOverflow != 5 {
		t.Fatalf("MethodOverflow = %d, want the 5 past-cap records counted structurally", jira.MethodOverflow)
	}
	if len(jira.DeniedQueryKeys) != maxQueryKeys {
		t.Fatalf("len(DeniedQueryKeys) = %d, want the cap %d", len(jira.DeniedQueryKeys), maxQueryKeys)
	}
	if jira.QueryKeyOverflow != 5 {
		t.Fatalf("QueryKeyOverflow = %d, want the 5 past-cap keys counted structurally", jira.QueryKeyOverflow)
	}
}

func TestHistoricalSentinelNamedKeysFoldIntoOverflow(t *testing.T) {
	// A pre-validation log can carry key names a current manifest could never
	// declare — including literal "(none)"/"(other)". Those must fold into the
	// overflow bucket, never masquerade as (or pollute) a synthetic bucket.
	log := strings.Join([]string{
		line(t, map[string]any{"key": "(none)", "verdict": "refused"}),
		line(t, map[string]any{"key": "(other)", "verdict": "refused"}),
		line(t, map[string]any{"key": "Probe Key!", "verdict": "refused"}),
		line(t, map[string]any{"verdict": "refused"}), // genuinely unresolved
	}, "\n")
	sum, err := Aggregate(strings.NewReader(log), 0)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if got := sum.Keys[overflowKey]; got == nil || got.Total != 3 {
		t.Fatalf("overflow bucket = %+v, want the 3 invalid-named historical keys", got)
	}
	if got := sum.Keys[noKey]; got == nil || got.Total != 1 {
		t.Fatalf("(none) bucket = %+v, want only the genuinely unresolved record", got)
	}
}

func TestAggregateDeterministicJSON(t *testing.T) {
	log := strings.Join([]string{
		line(t, map[string]any{"key": "jira", "grant_id": "g1", "upstream_status": 200}),
		line(t, map[string]any{"key": "confluence", "verdict": "denied", "canonical_target": "/wiki/x"}),
		line(t, map[string]any{"verdict": "refused"}),
	}, "\n")
	var outs [][]byte
	for i := 0; i < 2; i++ {
		sum, err := Aggregate(strings.NewReader(log), 0)
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		data, err := json.Marshal(sum)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		outs = append(outs, data)
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatalf("same log produced different artifacts:\n%s\n%s", outs[0], outs[1])
	}
}

func TestAggregateEmptyLog(t *testing.T) {
	sum, err := Aggregate(strings.NewReader(""), time.Hour)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Total != 0 || len(sum.Keys) != 0 {
		t.Fatalf("empty log rolled up to %+v", sum)
	}
}
