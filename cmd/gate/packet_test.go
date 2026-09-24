package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

func TestCandidateEvidenceIDMatchesStoreWidth(t *testing.T) {
	e := testEnv(t)
	a, err := e.st.Append(state.KindEvidence, state.NewRunID(), nil, map[string]string{"fixture": "ID width"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.ID) != len(candidateEvidenceID) {
		t.Fatalf("candidate header width %d differs from stored evidence ID width %d", len(candidateEvidenceID), len(a.ID))
	}
}

func TestPacketCLI(t *testing.T) {
	if os.Getenv("GATE_PACKET_HELPER") == "1" {
		os.Args = append([]string{"gate"}, os.Args[3:]...)
		main()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX gh fixture; repair invariants also tested in-process")
	}
	for _, decision := range []string{"pass", "block"} {
		t.Run(decision, func(t *testing.T) { testPacketJudgmentCLI(t, decision) })
	}
}

func testPacketJudgmentCLI(t *testing.T, decision string) {
	e, subject, grantID, run, call := packetCLIFixture(t)
	packet := call(0, "packet", "-run", run)
	if !strings.Contains(packet, `"complete": false`) || !strings.Contains(packet, "docs/companion.md") || !strings.Contains(packet, "new b/README.md") {
		t.Fatal(packet)
	}
	failed := call(4, "judge", "-run", run, "-grant", grantID, "-auto", "-provider", "codex", "-stamp=false")
	if !strings.Contains(failed, "judgment_evidence_incomplete") {
		t.Fatal(failed)
	}
	before, _ := cycleCount(e.st, subject, "")
	call(0, "evidence", "-run", run, "-grant", grantID)
	call(0, "evidence", "-run", run, "-grant", grantID, "-path", "docs/a,b.md")
	after, _ := cycleCount(e.st, subject, "")
	if before != 1 || after != before {
		t.Fatalf("repair cycles %d -> %d", before, after)
	}
	packet = call(0, "packet", "-run", run)
	if !strings.Contains(packet, `"complete": true`) || !strings.Contains(packet, "complete companion source") {
		t.Fatal(packet)
	}
	// Every settled judgment is irrevocable; repair cannot launder a substantive block.
	code := 1
	if decision == "pass" {
		code = 0
	}
	judged := call(code, "judge", "-run", run, "-grant", grantID, "-decision", decision, "-why", "synthetic judgment for CLI test", "-who", "fixture", "-stamp=false")
	if !strings.Contains(judged, subject.HeadSHA) {
		t.Fatal(judged)
	}
	closed := call(4, "evidence", "-run", run, "-grant", grantID, "-path", "docs/companion.md")
	if !strings.Contains(closed, "evidence_repair_closed") {
		t.Fatal(closed)
	}
	refused := call(3, "gate", "-repo", subject.Repo, "-pr", "7", "-grant", grantID, "-stamp=false")
	if !strings.Contains(refused, "grant_cycle_exceeded") {
		t.Fatal(refused)
	}
	audit, err := e.st.Audit()
	if err != nil || !audit.OK {
		t.Fatalf("append-only audit: %+v %v", audit, err)
	}
}

func TestEvidenceRepairBounds(t *testing.T) {
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: strings.Repeat("a", 40)}
	grant, err := capability.Mint(e.st, e.keyPath, subject.Repo, "merge", "T2", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	_, err = verify.Record(e.st, run, nil, verify.Verdict{Subject: subject, Source: "floor", Producer: verify.Producer{Class: verify.ClassCode}, Decision: verify.DecisionPass, Tier: "T0", Confidence: 1})
	if err != nil {
		t.Fatal(err)
	}
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, _, err := act(e, run, grant.ID, v, id, gateResult{}, false, nil); err != nil {
		t.Fatal(err)
	}
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	source := func(_, _, path string) (string, string, error) {
		if path != "docs/companion.md" {
			return "", "", fmt.Errorf("repository path changed: %q", path)
		}
		return "text", "blob", nil
	}
	moved := func(string, int) (string, error) { return strings.Repeat("b", 40), nil }
	if _, err := supplementEvidence(e, run, grant.ID, []string{"docs/companion.md"}, moved, source, packetTestIndex); err == nil || !strings.Contains(err.Error(), "evidence_head_changed") {
		t.Fatalf("changed head: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := supplementEvidence(e, run, grant.ID, []string{"docs/companion.md"}, head, source, packetTestIndex); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := supplementEvidence(e, run, grant.ID, []string{"docs/companion.md"}, head, source, packetTestIndex); err == nil || !strings.Contains(err.Error(), "evidence_repair_limit") {
		t.Fatalf("bound: %v", err)
	}
}

func TestEvidenceRepairAggregateOverflowRecordsNothingPartial(t *testing.T) {
	e, subject, grant, run, _ := packetCLIFixture(t)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"first", "second", "third", "overflow"}, nil }
	read := func(_, _, path string) (string, string, error) {
		length := 250 * 1024 // Each complete text file remains below 256 KiB.
		if path == "overflow" {
			length = 100 * 1024
		}
		return strings.Repeat("x", length), "fixture-blob-" + path, nil
	}
	if _, err := supplementEvidence(e, run, grant, []string{"first", "second", "third"}, head, read, index); err != nil {
		t.Fatalf("750 KiB aggregate was rejected: %v", err)
	}
	before, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supplementEvidence(e, run, grant, []string{"overflow"}, head, read, index); err == nil || !strings.Contains(err.Error(), "evidence_budget_exceeded") {
		t.Fatalf("aggregate overflow accepted: %v", err)
	}
	after, err := e.st.Run(run)
	if err != nil || len(after) != len(before) {
		t.Fatalf("failed supplement wrote partial evidence: %d -> %d: %v", len(before), len(after), err)
	}
}

func TestEvidenceRepairIncludesReviewsInAtomicBudget(t *testing.T) {
	e, subject, grant, run, _ := packetCLIFixture(t)
	_, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{
		"comments": []map[string]any{{"is_bot": true, "body": strings.Repeat("unresolved finding ", 12000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"first", "second", "third"}, nil }
	read := func(_, _, path string) (string, string, error) {
		return strings.Repeat("x", 230*1024), "fixture-blob-" + path, nil
	}
	before, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	_, err = supplementEvidence(e, run, grant, []string{"first", "second", "third"}, head, read, index)
	if err == nil || !strings.Contains(err.Error(), "evidence_budget_exceeded") {
		t.Fatalf("source-only admission hid review overflow: %v", err)
	}
	after, err := e.st.Run(run)
	if err != nil || len(after) != len(before) {
		t.Fatalf("shared-budget rejection appended evidence: %d -> %d: %v", len(before), len(after), err)
	}
}

func TestEvidenceRepairConcurrentSharedBudget(t *testing.T) {
	e, subject, grant, run, _ := packetCLIFixture(t)
	_, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{
		"comments": []map[string]any{{"is_bot": true, "body": strings.Repeat("unresolved finding ", 12000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"a1", "a2", "b1", "b2"}, nil }
	var ready sync.WaitGroup
	ready.Add(2)
	read := func(_, _, path string) (string, string, error) {
		if strings.HasSuffix(path, "1") {
			ready.Done()
			ready.Wait()
		}
		return strings.Repeat("x", 190*1024), "fixture-blob-" + path, nil
	}
	results := make(chan error, 2)
	for _, paths := range [][]string{{"a1", "a2"}, {"b1", "b2"}} {
		go func() {
			_, err := supplementEvidence(e, run, grant, paths, head, read, index)
			results <- err
		}()
	}
	passed, rejected := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			passed++
			continue
		}
		if !strings.Contains(err.Error(), "evidence_budget_exceeded") {
			t.Fatalf("unexpected concurrent failure: %v", err)
		}
		rejected++
	}
	if passed != 1 || rejected != 1 {
		t.Fatalf("shared capacity admitted twice: passed=%d rejected=%d", passed, rejected)
	}
}

func packetCLIFixture(t *testing.T) (env, verify.Subject, string, string, func(int, ...string) string) {
	t.Helper()
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: strings.Repeat("a", 40)}
	grant, err := capability.Mint(e.st, e.keyPath, subject.Repo, "merge", "T2", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	_, err = verify.Record(e.st, run, nil, verify.Verdict{Subject: subject, Source: "floor", Producer: verify.Producer{Class: verify.ClassCode}, Decision: verify.DecisionPass, Tier: "T0", Confidence: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Multiple similarly named files, plus an unchanged companion absent from diff.
	diff := strings.Repeat("large irrelevant preface\n", 3000)
	for _, p := range []string{"a/README.md", "b/README.md"} {
		diff += fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n-old\n+new %s\n", p, p, p, p, p)
	}
	_, err = e.st.Append(state.KindEvidence, run, nil, map[string]any{"diff": diff, "head": subject.HeadSHA, "comments": []map[string]any{{"is_bot": true, "body": "Inspect `README.md` and `docs/companion.md` before judgment."}}})
	if err != nil {
		t.Fatal(err)
	}
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grant.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park %d %v", code, err)
	}
	bin := t.TempDir()
	content := "complete companion source\n"
	sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(content), 0)), []byte(content)...))
	raw, _ := json.Marshal(map[string]any{"type": "file", "path": "docs/companion.md", "sha": hex.EncodeToString(sum[:]), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)), "size": len(content)})
	commaRaw, _ := json.Marshal(map[string]any{"type": "file", "path": "docs/a,b.md", "sha": hex.EncodeToString(sum[:]), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)), "size": len(content)})
	indexRaw, _ := json.Marshal(map[string]any{"sha": subject.HeadSHA, "tree": []map[string]string{{"path": "docs/companion.md", "type": "blob"}, {"path": "docs/a,b.md", "type": "blob"}}, "truncated": false})
	// The judge reads the base at emission: a head that contains the base's
	// head, so the pass the fixture judges can emit.
	liveBase := `{"data":{"repository":{"pullRequest":{"state":"OPEN","baseRefName":"main","baseRef":{"target":{"oid":"` + fakeBaseSHA + `"}}}}}}`
	contained := `{"status":"ahead","behind_by":0,"merge_base_commit":{"sha":"` + fakeBaseSHA + `"}}`
	script := "#!/bin/sh\ncase \"$2\" in\ngraphql) printf '%s' '" + liveBase + "';;\nrepos/o/r/compare/" + fakeBaseSHA + "..." + subject.HeadSHA + "?per_page=1) printf '%s' '" + contained + "';;\n*/pulls/7) printf '%s' '{\"head\":{\"sha\":\"" + subject.HeadSHA + "\"}}';;\nrepos/o/r/contents/docs/companion.md?ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) printf '%s' '" + string(raw) + "';;\nrepos/o/r/contents/docs/a%2Cb.md?ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) printf '%s' '" + string(commaRaw) + "';;\nrepos/o/r/git/trees/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?recursive=1) printf '%s' '" + string(indexRaw) + "';;\n*) exit 99;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	call := func(want int, args ...string) string {
		t.Helper()
		args = append(args, "-state", e.stateDir, "-key", filepath.Dir(e.keyPath))
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestPacketCLI$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "GATE_PACKET_HELPER=1", "PATH="+bin)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want {
			t.Fatalf("%v exit %d want %d: %s", args, code, want, out)
		}
		return string(out)
	}
	return e, subject, grant.ID, run, call
}

func TestEvidenceRepairConcurrentBound(t *testing.T) {
	e, subject, grant, run, _ := packetCLIFixture(t)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	source := func(string, string, string) (string, string, error) { return "text", "blob", nil }
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := supplementEvidence(e, run, grant, []string{"x"}, head, source, packetTestIndex)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !strings.Contains(err.Error(), "evidence_repair_limit") {
			t.Fatal(err)
		}
	}
	if successes != 3 {
		t.Fatalf("%d accepted supplements, want 3", successes)
	}
	audit, err := e.st.Audit()
	if err != nil || !audit.OK {
		t.Fatalf("%+v %v", audit, err)
	}
}

func packetTestIndex(string, string) ([]string, error) {
	return []string{"docs/companion.md", "x"}, nil
}

// A bare mention matching a changed nested file must stay satisfiable once the
// collector records the index, even when an unchanged root blob of the same
// name is unreadable by the collector.
func TestEvidenceRepairBareTokenStaysSatisfiable(t *testing.T) {
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: strings.Repeat("a", 40)}
	grant, err := capability.Mint(e.st, e.keyPath, subject.Repo, "merge", "T2", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	diff := "diff --git a/web/package-lock.json b/web/package-lock.json\n--- a/web/package-lock.json\n+++ b/web/package-lock.json\n@@ -1 +1 @@\n-old\n+new\n"
	body := "The `package-lock.json` churn looks unrelated; also check `docs/companion.md`."
	if _, err := e.st.Append(state.KindEvidence, run, nil, map[string]any{"diff": diff, "head": subject.HeadSHA, "comments": []map[string]any{{"is_bot": true, "body": body}}}); err != nil {
		t.Fatal(err)
	}
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grant.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park %d %v", code, err)
	}
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) {
		return []string{"package-lock.json", "web/package-lock.json", "docs/companion.md"}, nil
	}
	read := func(_, _, path string) (string, string, error) {
		if path == "package-lock.json" {
			return "", "", fmt.Errorf("evidence_source_invalid: expected bounded regular file %s", path)
		}
		return "text\n", "blob-" + path, nil
	}
	if _, err := supplementEvidence(e, run, grant.ID, nil, head, read, index); err != nil {
		t.Fatalf("default collection selected an unreadable unchanged blob: %v", err)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	p, err := verify.JudgmentPacket(arts, subject)
	if err != nil || !p.Complete {
		t.Fatalf("bare token left the packet unsatisfiable: %v %v %v", p.Missing, p.RequiredSources, err)
	}
}

func parkedPacketRun(t *testing.T, evidenceBody map[string]any) (env, verify.Subject, string, string) {
	t.Helper()
	e := testEnv(t)
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: strings.Repeat("a", 40)}
	grant, err := capability.Mint(e.st, e.keyPath, subject.Repo, "merge", "T2", 1, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	recordVerifier(t, e, run, subject, verify.DecisionEscalate)
	if _, err := e.st.Append(state.KindEvidence, run, nil, evidenceBody); err != nil {
		t.Fatal(err)
	}
	v := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	id := recordReduced(t, e, run, v)
	if _, code, err := act(e, run, grant.ID, v, id, gateResult{}, false, nil); err != nil || code != codeParked {
		t.Fatalf("park %d %v", code, err)
	}
	return e, subject, grant.ID, run
}

func runPacket(t *testing.T, e env, run string, subject verify.Subject) (verify.Packet, int) {
	t.Helper()
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	p, err := verify.JudgmentPacket(arts, subject)
	if err != nil {
		t.Fatal(err)
	}
	return p, len(arts)
}

func largeDiff(path string, pairs int) string {
	var body strings.Builder
	for i := 0; i < pairs; i++ {
		fmt.Fprintf(&body, "-old line %06d %s\n+new line %06d %s\n", i, strings.Repeat("o", 40), i, strings.Repeat("n", 40))
	}
	return fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", path, path, path, path, pairs, pairs) + body.String()
}

// A supplement that fits the source/review share must still not push a
// rendered required diff out of a complete packet: the run would then need
// source that no longer fits and could never reach judgment.
func TestEvidenceRepairRefusesDisplacingCompletePacket(t *testing.T) {
	body := map[string]any{"diff": largeDiff("q.go", 2000), "comments": []map[string]any{{"is_bot": true, "body": "Bug at `q.go:1`"}}}
	e, subject, grant, run := parkedPacketRun(t, body)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"q.go", "c1", "c2", "c3"}, nil }
	read := func(_, _, path string) (string, string, error) {
		return strings.Repeat("c", 250*1024), "blob-" + path, nil
	}
	before, n := runPacket(t, e, run, subject)
	if !before.Complete {
		t.Fatalf("fixture must start complete: %v", before.Missing)
	}
	if _, err := supplementEvidence(e, run, grant, []string{"c1", "c2", "c3"}, head, read, index); err == nil || !strings.Contains(err.Error(), "evidence_budget_exceeded") {
		t.Fatalf("displacing supplement admitted: %v", err)
	}
	after, m := runPacket(t, e, run, subject)
	if !after.Complete || m != n {
		t.Fatalf("refused supplement changed the run: complete=%v artifacts %d -> %d", after.Complete, n, m)
	}
	if _, err := supplementEvidence(e, run, grant, []string{"c1"}, head, read, index); err != nil {
		t.Fatalf("companion that keeps the packet complete was refused: %v", err)
	}
}

// Two collectors that race on the same required path both see it missing. The
// duplicate copy must not consume capacity a required diff still needs.
func TestEvidenceRepairConcurrentDuplicateKeepsPacketComplete(t *testing.T) {
	review := "Bug at `r.go:1`; also compare `docs/p.md`. " + strings.Repeat("z", 300000)
	body := map[string]any{"diff": largeDiff("r.go", 2000), "comments": []map[string]any{{"is_bot": true, "body": review}}}
	e, subject, grant, run := parkedPacketRun(t, body)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"docs/p.md", "r.go"}, nil }
	var ready sync.WaitGroup
	ready.Add(2)
	read := func(_, _, path string) (string, string, error) {
		if path == "docs/p.md" {
			ready.Done()
			ready.Wait()
			return strings.Repeat("p", 200*1024), "blob-p", nil
		}
		return strings.Repeat("r", 150*1024), "blob-r", nil
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := supplementEvidence(e, run, grant, nil, head, read, index)
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent collector: %v", err)
		}
	}
	p, _ := runPacket(t, e, run, subject)
	if !p.Complete || strings.Count(p.Context, "## Exact-head source docs/p.md") != 1 {
		t.Fatalf("duplicate collection stranded the packet: complete=%v missing=%v", p.Complete, p.Missing)
	}
}

func TestEvidenceRepairSkipsCollectedPaths(t *testing.T) {
	e, subject, grant, run, _ := packetCLIFixture(t)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	read := func(_, _, path string) (string, string, error) { return "text", "blob-" + path, nil }
	if _, err := supplementEvidence(e, run, grant, []string{"docs/companion.md"}, head, read, packetTestIndex); err != nil {
		t.Fatal(err)
	}
	id, err := supplementEvidence(e, run, grant, []string{"docs/companion.md"}, head, read, packetTestIndex)
	if err != nil {
		t.Fatal(err)
	}
	arts, err := e.st.Run(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		var s verify.SourceEvidence
		if a.ID != id || json.Unmarshal(a.Body, &s) != nil {
			continue
		}
		if len(s.Sources) != 0 {
			t.Fatalf("already collected path copied again: %+v", s.Sources)
		}
		return
	}
	t.Fatal("second supplement not recorded")
}

// A packet whose only gap is the file index is complete once this supplement's
// index is recorded, so its sources must not displace a rendered diff either.
func TestEvidenceRepairRefusesDisplacingIndexOnlyGap(t *testing.T) {
	body := map[string]any{"diff": largeDiff("q.go", 2000), "comments": []map[string]any{{"is_bot": true, "body": "Bug at `q.go:1`; see `docs/gone.md:5`"}}}
	e, subject, grant, run := parkedPacketRun(t, body)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"q.go", "c1", "c2", "c3"}, nil }
	read := func(_, _, path string) (string, string, error) {
		return strings.Repeat("c", 250*1024), "blob-" + path, nil
	}
	before, n := runPacket(t, e, run, subject)
	if before.Complete || strings.Join(before.Missing, " ") != "exact-head file index unavailable; run gate evidence to discover real companion paths" {
		t.Fatalf("fixture must be missing only the index: %v", before.Missing)
	}
	if _, err := supplementEvidence(e, run, grant, []string{"c1", "c2", "c3"}, head, read, index); err == nil || !strings.Contains(err.Error(), "evidence_budget_exceeded") {
		t.Fatalf("displacing supplement admitted behind an index-only gap: %v", err)
	}
	if _, m := runPacket(t, e, run, subject); m != n {
		t.Fatalf("refused supplement changed the run: %d -> %d artifacts", n, m)
	}
	if _, err := supplementEvidence(e, run, grant, nil, head, read, index); err != nil {
		t.Fatalf("index-only repair refused: %v", err)
	}
	if after, _ := runPacket(t, e, run, subject); !after.Complete {
		t.Fatalf("index-only repair left the packet incomplete: %v", after.Missing)
	}
}

// While a packet is incomplete, collected source may push a rendered diff out:
// that diff's own smaller source then repairs it.
func TestEvidenceRepairLetsSourceDisplaceDiffWhileIncomplete(t *testing.T) {
	body := map[string]any{"diff": largeDiff("q.go", 5500), "comments": []map[string]any{{"is_bot": true, "body": "Bug at `q.go:1` and `a.go:1`"}}}
	e, subject, grant, run := parkedPacketRun(t, body)
	head := func(string, int) (string, error) { return subject.HeadSHA, nil }
	index := func(string, string) ([]string, error) { return []string{"q.go", "a.go"}, nil }
	read := func(_, _, path string) (string, string, error) {
		if path == "q.go" {
			return strings.Repeat("q", 50*1024), "blob-q", nil
		}
		return strings.Repeat("a", 250*1024), "blob-a", nil
	}
	if _, err := supplementEvidence(e, run, grant, nil, head, read, index); err != nil {
		t.Fatalf("required source refused while incomplete: %v", err)
	}
	p, _ := runPacket(t, e, run, subject)
	if p.Complete || strings.Join(p.RequiredSources, ",") != "q.go" {
		t.Fatalf("expected the displaced diff to request its source: %v %v", p.Missing, p.RequiredSources)
	}
	if _, err := supplementEvidence(e, run, grant, nil, head, read, index); err != nil {
		t.Fatalf("smaller replacement source refused: %v", err)
	}
	if p, _ = runPacket(t, e, run, subject); !p.Complete {
		t.Fatalf("displaced diff not repaired by its source: %v", p.Missing)
	}
}
