package driverstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"pgregory.net/rapid"
)

// This file is the generative complement to append_test.go. The example tests
// pin one corruption (a flipped hex digit), one torn tail, one dedupe retry;
// these generators assert the same laws over the whole shape space — every legal
// lifecycle the state machine admits, every single-byte corruption of a sealed
// ledger, every import-key collision pattern. The ledger is the load-bearing
// core (a swallowed break re-drives a merged PR; a wrong dedupe collapses two
// runs into one — the sol bug), so its invariants earn generators, not just
// examples.

// partial is one event before ids/times are assigned — the generator emits a
// stream's path as partials, then the assembler stamps monotone times and
// evt_<n> ids in append order.
type partial struct {
	kind   dsc.Kind
	stream string
	body   any
}

// genLifecycle draws a complete, VALID run: a run_imported naming S streams,
// each stream walking an independently-drawn legal path through the state
// machine, and a run_finished iff every stream reached a terminal status. The
// key discipline: only transitions the append-time state machine accepts are
// ever emitted, so a failure of the append→Verify property is a real regression
// in the mechanism, never a malformed generator.
func genLifecycle(t *rapid.T) (events []Event, streams []string, allTerminal bool) {
	s := rapid.IntRange(1, 3).Draw(t, "numStreams")
	specs := make([]dsc.StreamSpec, s)
	streams = make([]string, s)
	for i := range streams {
		streams[i] = fmt.Sprintf("dss_%d", i)
		specs[i] = dsc.StreamSpec{Stream: streams[i], DocPath: "docs/x.md"}
	}

	parts := []partial{{
		kind: dsc.KindRunImported,
		body: dsc.RunImportedBody{
			Repo: "itsHabib/workbench", Source: "docs/driver.md",
			Manifest: []byte(`{"v":1}`), Streams: specs,
		},
	}}

	allTerminal = true
	for _, st := range streams {
		streamParts, terminal := genStreamPath(t, st)
		parts = append(parts, streamParts...)
		if !terminal {
			allTerminal = false
		}
	}

	finish := allTerminal && rapid.Bool().Draw(t, "runFinished")
	if finish {
		parts = append(parts, partial{kind: dsc.KindRunFinished})
	}

	events = assemble(parts)
	return events, streams, finish
}

// genStreamPath draws one stream's legal walk and reports whether it ends
// terminal (merged / skipped / failed) — the run_finished precondition. Every
// branch mirrors an edge in applyStream; nothing else is emittable.
func genStreamPath(t *rapid.T, stream string) ([]partial, bool) {
	label := "path_" + stream
	switch rapid.SampledFrom([]string{
		"skipped", "dispatched", "failed", "failed_then_skipped",
		"landed", "pr_open", "merged", "fail_redispatch_merge",
	}).Draw(t, label) {
	case "skipped":
		// pending → skipped, no dispatch at all.
		return []partial{{kind: dsc.KindStreamSkipped, stream: stream}}, true
	case "dispatched":
		return withAttempts(t, stream, dispatch(stream), false), false
	case "failed":
		return failVia(t, stream), true
	case "failed_then_skipped":
		p := failVia(t, stream)
		// failed → skipped is a legal edge (applyStream).
		return append(p, partial{kind: dsc.KindStreamSkipped, stream: stream}), true
	case "landed":
		return landVia(t, stream, 1), false // stops at landed (non-terminal)
	case "pr_open":
		p, _ := prOpenVia(t, stream, 1)
		return p, false // pr_open is non-terminal
	case "merged":
		p, pr := prOpenVia(t, stream, 1)
		p = append(p, partial{kind: dsc.KindStreamMerged, stream: stream, body: dsc.StreamMergedBody{
			PR: pr, MergeCommit: "mc_" + stream, MergedAt: "2026-07-16T13:00:00Z",
		}})
		return p, true
	default: // fail_redispatch_merge — exercises the failed→dispatched re-open edge
		p := failVia(t, stream)
		p = append(p, dispatch(stream)) // failed → dispatched (re-open)
		p = append(p, attempt(stream, nextSeq(p, stream), true, ""))
		p = append(p, partial{kind: dsc.KindStreamPROpened, stream: stream, body: dsc.StreamPROpenedBody{
			PR: 42, URL: "https://x/42", HeadSHA: "h",
		}})
		p = append(p, partial{kind: dsc.KindStreamMerged, stream: stream, body: dsc.StreamMergedBody{
			PR: 42, MergeCommit: "mc2_" + stream, MergedAt: "2026-07-16T14:00:00Z",
		}})
		return p, true
	}
}

func dispatch(stream string) partial {
	return partial{kind: dsc.KindStreamDispatched, stream: stream}
}

func attempt(stream string, seq int, terminal bool, failure string) partial {
	return partial{kind: dsc.KindStreamAttempt, stream: stream, body: dsc.StreamAttemptBody{
		Seq: seq, DocPath: "docs/x.md", Terminal: terminal, FailureCategory: failure,
	}}
}

// nextSeq is the smallest legal stream_attempt seq given the partials already
// drawn for a stream — mirrors checkSeq's strict-increase rule.
func nextSeq(parts []partial, stream string) int {
	highest := 0
	for _, p := range parts {
		if p.stream != stream || p.kind != dsc.KindStreamAttempt {
			continue
		}
		if b, ok := p.body.(dsc.StreamAttemptBody); ok && b.Seq > highest {
			highest = b.Seq
		}
	}
	return highest + 1
}

// withAttempts prepends a dispatch and appends 0..3 non-terminal attempts —
// each keeps the stream in dispatched, so seq monotonicity is the only extra
// invariant they exercise.
func withAttempts(t *rapid.T, stream string, disp partial, _ bool) []partial {
	parts := []partial{disp}
	n := rapid.IntRange(0, 3).Draw(t, "attempts_"+stream)
	for i := 0; i < n; i++ {
		parts = append(parts, attempt(stream, nextSeq(parts, stream), false, ""))
	}
	return parts
}

func failVia(t *rapid.T, stream string) []partial {
	parts := withAttempts(t, stream, dispatch(stream), false)
	return append(parts, attempt(stream, nextSeq(parts, stream), true, "flake"))
}

func landVia(t *rapid.T, stream string, _ int) []partial {
	parts := withAttempts(t, stream, dispatch(stream), false)
	return append(parts, attempt(stream, nextSeq(parts, stream), true, ""))
}

func prOpenVia(t *rapid.T, stream string, pr int) ([]partial, int) {
	parts := landVia(t, stream, pr)
	parts = append(parts, partial{kind: dsc.KindStreamPROpened, stream: stream, body: dsc.StreamPROpenedBody{
		PR: pr, URL: fmt.Sprintf("https://x/%d", pr), HeadSHA: "h",
	}})
	cycles := rapid.IntRange(0, 2).Draw(t, "cycles_"+stream)
	for i := 1; i <= cycles; i++ {
		parts = append(parts, partial{kind: dsc.KindReviewCycle, stream: stream, body: dsc.ReviewCycleBody{
			Cycle: i, PanelSettled: i == cycles, Findings: 0,
		}})
	}
	return parts, pr
}

// assemble stamps evt_<n> ids and strictly-increasing whole-second times onto
// the drawn partials in append order — the shape Append accepts.
func assemble(parts []partial) []Event {
	out := make([]Event, len(parts))
	for i, p := range parts {
		out[i] = ev(fmt.Sprintf("evt_%d", i+1), p.kind, p.stream, "session:a", baseTime.Add(time.Duration(i)*time.Second), p.body)
	}
	return out
}

// appendAll claims a lease and appends every event, failing the property (not
// the run) if any legal event is rejected.
func appendAll(t *rapid.T, dir, run string, events []Event) Lease {
	l, err := Claim(dir, run, "session:a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, e := range events {
		if _, err := Append(dir, l, e); err != nil {
			t.Fatalf("append %s (%s): %v", e.Kind, e.ID, err)
		}
	}
	return l
}

// tempDir gives each rapid iteration its own state root. rapid.T is not a
// testing.TB, so t.TempDir is unavailable — mkdir + cleanup by hand.
func tempDir(t *rapid.T) string {
	dir, err := os.MkdirTemp("", "dsprop")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// TestPropValidLifecycleVerifiesAndFolds is the headline invariant: any legal
// lifecycle the state machine admits appends cleanly, verifies as an intact hash
// chain, and folds to a state whose per-stream status the two INDEPENDENT
// implementations of the machine — append.go's streamStatus and reduce.go's fold
// — agree on. Two implementations of one state machine that never disagree is a
// far stronger guarantee than either checked against hand-picked expectations.
func TestPropValidLifecycleVerifiesAndFolds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		events, streams, finished := genLifecycle(t)
		dir := tempDir(t)
		run := "dsr_run"
		appendAll(t, dir, run, events)

		if err := Verify(dir, run); err != nil {
			t.Fatalf("valid lifecycle must verify: %v", err)
		}
		read, err := Events(dir, run)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		if len(read) != len(events) {
			t.Fatalf("read %d events, appended %d", len(read), len(events))
		}
		state := FoldEvents(read)
		if finished && state.Run.Status != RunStatusFinished {
			t.Fatalf("run_finished ledger must fold to finished, got %q", state.Run.Status)
		}
		for _, s := range streams {
			oracle := streamStatus(read, s)
			if got := state.Streams[s].Status; got != oracle {
				t.Fatalf("stream %s: reducer status %q disagrees with append-time oracle %q", s, got, oracle)
			}
		}
	})
}

// TestPropSingleByteFlipBreaksChainOrIsNoOp generalizes corruptFirstHash into
// the EXACT integrity guarantee, and is sharper than "any flip breaks": the hash
// chain seals the SEMANTIC event (its canonical encoding), not the raw disk
// bytes. So flipping any byte of a sealed ledger (bar the final newline, which
// heals as a torn tail) either
//
//   - breaks the chain as ErrChainBroken — the line stops decoding, or its
//     recomputed hash no longer seals its bytes, or a Prev link no longer
//     matches; OR
//   - is a semantic no-op — the tolerant reader decodes the identical event set
//     (e.g. renaming the empty optional key "ext_ref" to an unknown "dxt_ref":
//     the value was already absent, so canonicalization normalizes it away).
//
// What must NEVER happen: Verify passing while the decoded content differs from
// what was sealed. A swallowed change to real content re-drives a merged PR.
func TestPropSingleByteFlipBreaksChainOrIsNoOp(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		events, _, _ := genLifecycle(t)
		dir := tempDir(t)
		run := "dsr_run"
		l := appendAll(t, dir, run, events)
		_ = l.Release()

		path := filepath.Join(dir, run, "events.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		sealed, err := decodeLedger(data)
		if err != nil {
			t.Fatalf("committed ledger must decode: %v", err)
		}
		// Exclude the trailing newline: corrupting it is a torn tail (healed),
		// not a mid-chain break — a different, separately tested law.
		if len(data) < 2 {
			return
		}
		idx := rapid.IntRange(0, len(data)-2).Draw(t, "flipIdx")
		delta := byte(rapid.IntRange(1, 255).Draw(t, "flipDelta"))
		data[idx] ^= delta // guaranteed to change the byte (delta != 0)

		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write corrupted ledger: %v", err)
		}
		err = Verify(dir, run)
		if errors.Is(err, ErrChainBroken) {
			return // the corruption was caught — the common case
		}
		if err != nil {
			t.Fatalf("flip at byte %d (delta %d): unexpected non-chain error: %v", idx, delta, err)
		}
		// Verify passed: the flip MUST have been semantically invisible. Prove it
		// by re-reading and comparing the canonical form of every event to what
		// was sealed — anything else is a swallowed corruption.
		got, err := decodeLedger(data)
		if err != nil {
			t.Fatalf("Verify passed but decode failed: %v", err)
		}
		if len(got) != len(sealed) {
			t.Fatalf("flip at byte %d passed Verify but changed the event count: %d != %d", idx, len(got), len(sealed))
		}
		for i := range got {
			if a, b := dsc.Canonical(got[i]), dsc.Canonical(sealed[i]); string(a) != string(b) {
				t.Fatalf("flip at byte %d (delta %d) passed Verify but changed event %d's content:\n got=%s\nwant=%s", idx, delta, i, a, b)
			}
		}
	})
}

// TestPropTornSuffixHealed is the crash-recovery law over arbitrary torn bytes:
// a committed ledger plus ANY newline-free suffix (a partial line a crash left
// mid-write) heals on the next append — the torn bytes vanish, the new event
// chains onto the last COMPLETE line, and the chain verifies. Only the completed
// prefix is ever trusted. The ledger is held at a non-terminal state (dispatched)
// so a legal next event always exists whatever the torn bytes are.
func TestPropTornSuffixHealed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir := tempDir(t)
		run := "dsr_run"
		l, err := Claim(dir, run, "session:a")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		imp := ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
			Repo: "r", Source: "s", Manifest: []byte(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_0", DocPath: "d"}},
		})
		if _, err := Append(dir, l, imp); err != nil {
			t.Fatalf("import: %v", err)
		}
		if _, err := Append(dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_0", "session:a", baseTime.Add(time.Second), nil)); err != nil {
			t.Fatalf("dispatch: %v", err)
		}

		// A torn suffix is arbitrary bytes with no newline (a newline would make
		// it a complete line, not a torn one).
		suffix := rapid.SliceOfN(rapid.ByteRange(0, 255), 0, 40).Draw(t, "suffix")
		clean := make([]byte, 0, len(suffix))
		for _, b := range suffix {
			if b != '\n' {
				clean = append(clean, b)
			}
		}
		path := filepath.Join(dir, run, "events.jsonl")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		if _, err := f.Write(clean); err != nil {
			t.Fatalf("write torn suffix: %v", err)
		}
		f.Close()

		// The stream is dispatched, so a non-terminal attempt is always the legal
		// next move — the heal must trim the torn bytes, then chain onto evt_2.
		next := ev("evt_3", dsc.KindStreamAttempt, "dss_0", "session:a",
			baseTime.Add(2*time.Second), dsc.StreamAttemptBody{Seq: 1, DocPath: "d", Terminal: false})
		if _, err := Append(dir, l, next); err != nil {
			t.Fatalf("append after torn suffix must heal and commit: %v", err)
		}
		if err := Verify(dir, run); err != nil {
			t.Fatalf("healed ledger must verify: %v", err)
		}
		read, err := Events(dir, run)
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		if len(read) != 3 {
			t.Fatalf("healed ledger has %d events, want 3 (torn bytes must be trimmed, evt_3 chained on)", len(read))
		}
	})
}

// TestPropTrimTornTailIsLastNewline is the pure law under the heal: trimTornTail
// returns exactly the bytes through the last newline, and appending any
// newline-free suffix to a newline-terminated ledger trims back to that ledger.
// Fast and exhaustive — no I/O.
func TestPropTrimTornTailIsLastNewline(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOfN(rapid.ByteRange(0, 255), 0, 200).Draw(t, "data")
		trimmed := trimTornTail(data)
		if len(trimmed) > len(data) {
			t.Fatal("trim cannot grow the input")
		}
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] != '\n' {
			t.Fatalf("a non-empty trim must end at a newline, got %q", trimmed[len(trimmed)-1])
		}
		// Nothing past the trim boundary may contain a newline.
		for _, b := range data[len(trimmed):] {
			if b == '\n' {
				t.Fatal("trim dropped bytes before the last newline")
			}
		}
		// The core torn-tail law: complete ledger + torn suffix trims back.
		complete := append(append([]byte{}, data...), '\n')
		suffix := rapid.SliceOfN(rapid.ByteRange(0, 255), 0, 30).Filter(func(bs []byte) bool {
			for _, b := range bs {
				if b == '\n' {
					return false
				}
			}
			return true
		}).Draw(t, "suffix")
		torn := append(append([]byte{}, complete...), suffix...)
		if got := trimTornTail(torn); len(got) != len(complete) {
			t.Fatalf("torn suffix not trimmed to the complete prefix: got %d want %d", len(got), len(complete))
		}
	})
}

// TestPropMonotonicityRejectsEarlierTime: after any valid lifecycle prefix, an
// otherwise-legal next event stamped before the head is rejected for
// monotonicity — the guard against clock skew and replayed writes, over every
// generated head state rather than one hand-built ledger.
func TestPropMonotonicityRejectsEarlierTime(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir := tempDir(t)
		run := "dsr_run"
		l, err := Claim(dir, run, "session:a")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		// A minimal valid head: run_imported then dispatch, so a non-terminal
		// attempt is the legal next move whose time we push backwards.
		imp := ev("evt_1", dsc.KindRunImported, "", "session:a", baseTime, dsc.RunImportedBody{
			Repo: "r", Source: "s", Manifest: []byte(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_0", DocPath: "d"}},
		})
		if _, err := Append(dir, l, imp); err != nil {
			t.Fatalf("import: %v", err)
		}
		if _, err := Append(dir, l, ev("evt_2", dsc.KindStreamDispatched, "dss_0", "session:a", baseTime.Add(time.Second), nil)); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		back := rapid.IntRange(1, 100000).Draw(t, "secondsBack")
		earlier := ev("evt_3", dsc.KindStreamAttempt, "dss_0", "session:a",
			baseTime.Add(time.Second).Add(-time.Duration(back)*time.Second),
			dsc.StreamAttemptBody{Seq: 1, DocPath: "d", Terminal: false})
		_, err = Append(dir, l, earlier)
		if err == nil || !strings.Contains(err.Error(), "monotonicity") {
			t.Fatalf("an earlier-than-head event must be rejected for monotonicity, got %v", err)
		}
	})
}

// TestPropImportDedupeKeyDiscriminates is the MUST-HAVE property and the direct
// guard against the sol bug: import identity is the FULL (repo, source,
// generated_at, parent, parent_stream) tuple. Distinct keys mint distinct runs;
// identical keys dedupe to one committed run. The sol bug was importKey ignoring
// (parent, parent_stream), so sibling child sub-runs sharing (repo, source,
// generated_at) collapsed into one run — a bug this property makes unpressable,
// because the generator deliberately varies parent/parent_stream while holding
// the other three fixed.
func TestPropImportDedupeKeyDiscriminates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// A small component pool so collisions AND parent-only differences both
		// occur frequently.
		repo := rapid.SampledFrom([]string{"r1", "r2"})
		source := rapid.SampledFrom([]string{"s1", "s2"})
		gen := rapid.SampledFrom([]string{"g1", "g2"})
		parent := rapid.SampledFrom([]string{"", "p1", "p2"})
		pstream := rapid.SampledFrom([]string{"", "ps1", "ps2"})

		type key struct{ repo, source, gen, parent, pstream string }
		genKey := rapid.Custom(func(t *rapid.T) key {
			return key{
				repo:    repo.Draw(t, "repo"),
				source:  source.Draw(t, "source"),
				gen:     gen.Draw(t, "gen"),
				parent:  parent.Draw(t, "parent"),
				pstream: pstream.Draw(t, "pstream"),
			}
		})
		keys := rapid.SliceOfN(genKey, 2, 6).Draw(t, "keys")

		dir := tempDir(t)
		// committed[key] = the Hash of the run that key minted (or deduped to).
		committed := map[key]string{}
		results := make([]string, len(keys))
		for i, k := range keys {
			run := fmt.Sprintf("dsr_run%d", i)
			l, err := Claim(dir, run, "session:a")
			if err != nil {
				t.Fatalf("claim %s: %v", run, err)
			}
			body := dsc.RunImportedBody{
				Repo: k.repo, Source: k.source, GeneratedAt: k.gen,
				Parent: k.parent, ParentStream: k.pstream,
				Manifest: []byte(`{}`), Streams: []dsc.StreamSpec{{Stream: "dss_0", DocPath: "d"}},
			}
			out, err := Append(dir, l, ev(fmt.Sprintf("evt_%d", i), dsc.KindRunImported, "", "session:a", baseTime.Add(time.Duration(i)*time.Second), body))
			if err != nil {
				t.Fatalf("import %d: %v", i, err)
			}
			_ = l.Release()
			results[i] = out.Hash
			if prev, seen := committed[k]; seen {
				if out.Hash != prev {
					t.Fatalf("identical key %+v deduped to a different run: %q vs %q", k, out.Hash, prev)
				}
			} else {
				committed[k] = out.Hash
			}
		}

		// Cross-check: two imports share a committed hash IFF their keys are equal.
		for i := range keys {
			for j := i + 1; j < len(keys); j++ {
				sameKey := keys[i] == keys[j]
				sameRun := results[i] == results[j]
				if sameKey != sameRun {
					t.Fatalf("dedupe disagreed with key equality: keys[%d]=%+v keys[%d]=%+v sameKey=%v sameRun=%v",
						i, keys[i], j, keys[j], sameKey, sameRun)
				}
			}
		}
		// The number of distinct minted runs equals the number of distinct keys.
		distinctHashes := map[string]bool{}
		for _, h := range results {
			distinctHashes[h] = true
		}
		if len(distinctHashes) != len(committed) {
			t.Fatalf("minted %d distinct runs for %d distinct keys", len(distinctHashes), len(committed))
		}
	})
}
