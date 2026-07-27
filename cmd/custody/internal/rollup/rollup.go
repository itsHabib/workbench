// Package rollup aggregates custody's request-log artifact
// (<state>/log/requests.jsonl) into a deterministic per-key summary — the
// telemetry base an offline reviewer (human or local model) reads instead of
// the raw log. It is read-side only: it parses the JSONL schema the serve
// engine writes, never imports the engine, and by construction its output
// carries no header value, body byte, or secret — the log never held them.
package rollup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// SchemaVersion pins the rollup artifact shape so a downstream reader can
// branch on it, mirroring the request log's own versioning.
const SchemaVersion = 1

// logSchemaVersion is the request-log line schema this reader understands.
const logSchemaVersion = 1

// maxDeniedTargets caps the per-key denied-target list so the artifact stays
// bounded no matter how noisy a probing client gets.
const maxDeniedTargets = 10

// noKey buckets log lines that never resolved a key (unknown key, malformed
// grant header) so refusal pressure is visible without inventing a key name.
const noKey = "(none)"

// overflowKey absorbs keys past maxKeys — and any historical key name outside
// the manifest.ValidateKeyName charset (logs written before serve stopped
// stamping unresolved prefixes can carry attacker-chosen names, including
// literal "(none)"/"(other)"). Because a VALID key name can never contain a
// parenthesis, real traffic can never merge into a synthetic bucket.
const overflowKey = "(other)"

// maxKeys bounds the distinct key buckets in one rollup INCLUDING the overflow
// bucket — far above any real manifest, low enough that a probed log cannot
// balloon the artifact.
const maxKeys = 100

// maxMethods and maxQueryKeys bound the per-key method and denied-query-key
// maps: both strings are client-chosen tokens, so without a cap a loopback
// prober could grow the artifact without bound. Past-cap entries increment a
// structural overflow COUNTER, never an in-map marker — a real token could
// spell any marker string, so overflow must live outside the map's namespace.
const (
	maxMethods   = 10
	maxQueryKeys = 20
)

// maxLineBytes bounds one log line; a longer line is counted as malformed and
// skipped, never fatal — the reader's fail-soft promise covers size too.
const maxLineBytes = 1 << 20

// Record is the read-side view of one request-log line (log schema v1). Only
// the fields the rollup consumes are declared; unknown fields are ignored so
// additive log growth does not break older readers.
type Record struct {
	SchemaVersion   int      `json:"schema_version"`
	Timestamp       string   `json:"ts"`
	Key             string   `json:"key"`
	GrantID         string   `json:"grant_id"`
	Verdict         string   `json:"verdict"`
	Method          string   `json:"method"`
	CanonicalTarget string   `json:"canonical_target"`
	QueryKeys       []string `json:"query_keys"`
	UpstreamStatus  int      `json:"upstream_status"`
	LatencyMs       int64    `json:"latency_ms"`

	// when is Timestamp parsed once at read time. All ordering uses it:
	// RFC3339Nano strings do NOT sort chronologically when fractional and
	// whole-second stamps mix ("…00Z" > "…00.5Z" byte-wise), and the writer's
	// RFC3339Nano format drops trailing zeros, so that mix genuinely occurs.
	when time.Time
}

// Window is the observed time span of the aggregated records.
type Window struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Latency is the nearest-rank latency profile of a key's requests.
type Latency struct {
	P50 int64 `json:"p50_ms"`
	P95 int64 `json:"p95_ms"`
	Max int64 `json:"max_ms"`
}

// Target is one denied request shape and how often it was attempted.
type Target struct {
	Target string `json:"target"`
	Count  int    `json:"count"`
}

// KeySummary aggregates every record that resolved to one manifest key.
type KeySummary struct {
	Total            int            `json:"total"`
	ByVerdict        map[string]int `json:"by_verdict"`
	ByMethod         map[string]int `json:"by_method"`
	MethodOverflow   int            `json:"method_overflow,omitempty"`
	UpstreamStatus   map[string]int `json:"upstream_status,omitempty"`
	Latency          Latency        `json:"latency"`
	DistinctGrants   int            `json:"distinct_grants"`
	DeniedTargets    []Target       `json:"denied_targets,omitempty"`
	DeniedQueryKeys  map[string]int `json:"denied_query_keys,omitempty"`
	QueryKeyOverflow int            `json:"denied_query_key_overflow,omitempty"`

	latencies []int64
	grants    map[string]struct{}
	denied    map[string]int
}

// Summary is the rollup artifact. Every map marshals with sorted keys and the
// denied-target lists are explicitly ordered, so the same log bytes always
// produce the same output bytes — deterministic by construction.
type Summary struct {
	SchemaVersion     int                    `json:"schema_version"`
	Window            Window                 `json:"window"`
	Total             int                    `json:"total"`
	Malformed         int                    `json:"malformed_lines,omitempty"`
	UnsupportedSchema int                    `json:"unsupported_schema_lines,omitempty"`
	ByVerdict         map[string]int         `json:"by_verdict"`
	Keys              map[string]*KeySummary `json:"keys"`

	windowFrom time.Time
	windowTo   time.Time
}

// Aggregate reads request-log JSONL from r and rolls it up. A non-zero since
// keeps only records within that duration of the NEWEST record's timestamp —
// windowing is anchored to the log, not the wall clock, so the same input
// always yields the same output. Lines that fail to parse are counted, not
// fatal: the log is append-only and a crash can leave a torn tail.
func Aggregate(r io.Reader, since time.Duration) (*Summary, error) {
	records, sum, err := read(r)
	if err != nil {
		return nil, err
	}
	records = window(records, since)
	for _, rec := range records {
		sum.add(rec)
	}
	sum.finish()
	return sum, nil
}

// read parses every line, splitting them into usable records and the
// malformed/unsupported counters on the summary.
func read(r io.Reader) ([]Record, *Summary, error) {
	sum := &Summary{
		SchemaVersion: SchemaVersion,
		ByVerdict:     map[string]int{},
		Keys:          map[string]*KeySummary{},
	}
	var records []Record
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, tooLong, err := readLine(br)
		if err != nil && err != io.EOF {
			return nil, nil, fmt.Errorf("rollup: scan log: %w", err)
		}
		records = fold(records, line, tooLong, sum)
		if err == io.EOF {
			return records, sum, nil
		}
	}
}

// readLine reads one newline-terminated line. A line longer than maxLineBytes
// is discarded and reported as tooLong instead of failing the whole read. The
// returned error is io.EOF only at end of input.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) > maxLineBytes {
			tooLong = true
			line = nil
		}
		if !tooLong {
			line = append(line, frag...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			return nil, false, err
		}
		return bytes.TrimSuffix(line, []byte("\n")), tooLong, err
	}
}

// fold accounts one raw line into the record list or the skip counters.
func fold(records []Record, line []byte, tooLong bool, sum *Summary) []Record {
	if tooLong {
		sum.Malformed++
		return records
	}
	if len(line) == 0 {
		return records
	}
	rec, ok := parse(line, sum)
	if !ok {
		return records
	}
	return append(records, rec)
}

// parse decodes one line, bumping the right counter when it is unusable.
func parse(line []byte, sum *Summary) (Record, bool) {
	var rec Record
	if err := json.Unmarshal(line, &rec); err != nil {
		sum.Malformed++
		return Record{}, false
	}
	if rec.SchemaVersion != logSchemaVersion {
		sum.UnsupportedSchema++
		return Record{}, false
	}
	when, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil {
		sum.Malformed++
		return Record{}, false
	}
	rec.when = when
	return rec, true
}

// window drops records older than since before the newest record, comparing
// the timestamps parsed in parse — never the strings.
func window(records []Record, since time.Duration) []Record {
	if since <= 0 || len(records) == 0 {
		return records
	}
	newest := records[0].when
	for _, rec := range records {
		if rec.when.After(newest) {
			newest = rec.when
		}
	}
	cutoff := newest.Add(-since)
	kept := records[:0]
	for _, rec := range records {
		if !rec.when.Before(cutoff) {
			kept = append(kept, rec)
		}
	}
	return kept
}

// add folds one record into the summary.
func (s *Summary) add(rec Record) {
	s.Total++
	s.ByVerdict[rec.Verdict]++
	s.stretchWindow(rec)
	key := bucketKey(s.Keys, rec.Key)
	ks := s.Keys[key]
	if ks == nil {
		ks = &KeySummary{
			ByVerdict: map[string]int{},
			ByMethod:  map[string]int{},
			grants:    map[string]struct{}{},
			denied:    map[string]int{},
		}
		s.Keys[key] = ks
	}
	ks.add(rec)
}

// bucketKey maps a record's key to its summary bucket. An empty key never
// resolved ("(none)" — unknown-key pressure, always its own bucket); a name
// outside the manifest charset is historical junk a current manifest could not
// declare and folds into "(other)", which also absorbs keys past the cap. Both
// synthetic buckets always insert, so real keys cap at maxKeys-2 and the
// total, sentinels included, never exceeds maxKeys.
func bucketKey(keys map[string]*KeySummary, key string) string {
	if key == "" {
		return noKey
	}
	if manifest.ValidateKeyName(key) != nil {
		return overflowKey
	}
	if keys[key] == nil && len(keys) >= maxKeys-2 {
		return overflowKey
	}
	return key
}

// stretchWindow widens the observed span to include rec, comparing parsed
// times; the Window strings echo the boundary records' original timestamps.
func (s *Summary) stretchWindow(rec Record) {
	if s.Window.From == "" || rec.when.Before(s.windowFrom) {
		s.Window.From = rec.Timestamp
		s.windowFrom = rec.when
	}
	if s.Window.To == "" || rec.when.After(s.windowTo) {
		s.Window.To = rec.Timestamp
		s.windowTo = rec.when
	}
}

// add folds one record into a key's summary.
func (k *KeySummary) add(rec Record) {
	k.Total++
	k.ByVerdict[rec.Verdict]++
	if counted(k.ByMethod, rec.Method, maxMethods) {
		k.MethodOverflow++
	}
	k.latencies = append(k.latencies, rec.LatencyMs)
	if rec.GrantID != "" {
		k.grants[rec.GrantID] = struct{}{}
	}
	if rec.UpstreamStatus != 0 {
		if k.UpstreamStatus == nil {
			k.UpstreamStatus = map[string]int{}
		}
		k.UpstreamStatus[strconv.Itoa(rec.UpstreamStatus)]++
	}
	if rec.Verdict != "denied" {
		return
	}
	// Logs written before serve stamped the canonical path on denials carry an
	// empty target; keep those buckets honest instead of collapsing per method.
	target := rec.CanonicalTarget
	if target == "" {
		target = "(target unrecorded)"
	}
	k.denied[rec.Method+" "+target]++
	for _, q := range rec.QueryKeys {
		if k.DeniedQueryKeys == nil {
			k.DeniedQueryKeys = map[string]int{}
		}
		if counted(k.DeniedQueryKeys, q, maxQueryKeys) {
			k.QueryKeyOverflow++
		}
	}
}

// counted increments counts[name], or reports overflow when name is new and
// the map is at limit — the caller tracks past-cap volume in a separate
// counter so no token string can ever collide with an overflow marker.
func counted(counts map[string]int, name string, limit int) (overflow bool) {
	if _, ok := counts[name]; !ok && len(counts) >= limit {
		return true
	}
	counts[name]++
	return false
}

// finish derives the ordered/derived fields once all records are folded in.
func (s *Summary) finish() {
	for _, ks := range s.Keys {
		ks.finish()
	}
}

// finish computes latency percentiles, the distinct-grant count, and the
// bounded, deterministically ordered denied-target list.
func (k *KeySummary) finish() {
	k.DistinctGrants = len(k.grants)
	k.Latency = percentiles(k.latencies)
	k.DeniedTargets = topTargets(k.denied, maxDeniedTargets)
}

// percentiles returns the nearest-rank p50/p95 and the max of values.
func percentiles(values []int64) Latency {
	if len(values) == 0 {
		return Latency{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := func(p int) int64 {
		idx := (p*len(sorted) + 99) / 100 // nearest-rank: ceil(p/100 * n)
		return sorted[idx-1]
	}
	return Latency{P50: rank(50), P95: rank(95), Max: sorted[len(sorted)-1]}
}

// topTargets orders denied targets by count descending, ties lexicographically,
// and caps the list at n.
func topTargets(denied map[string]int, n int) []Target {
	if len(denied) == 0 {
		return nil
	}
	out := make([]Target, 0, len(denied))
	for target, count := range denied {
		out = append(out, Target{Target: target, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Target < out[j].Target
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
