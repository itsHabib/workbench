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
	call(0, "evidence", "-run", run, "-grant", grantID, "-path", "docs/companion.md")
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
	judged := call(code, "judge", "-run", run, "-grant", grantID, "-decision", decision, "-why", "synthetic judgment for CLI test", "-stamp=false")
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
	source := func(string, string, string) (string, string, error) { return "text", "blob", nil }
	moved := func(string, int) (string, error) { return strings.Repeat("b", 40), nil }
	if _, err := supplementEvidence(e, run, grant.ID, []string{"x"}, moved, source); err == nil || !strings.Contains(err.Error(), "evidence_head_changed") {
		t.Fatalf("changed head: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := supplementEvidence(e, run, grant.ID, []string{"x"}, head, source); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := supplementEvidence(e, run, grant.ID, []string{"x"}, head, source); err == nil || !strings.Contains(err.Error(), "evidence_repair_limit") {
		t.Fatalf("bound: %v", err)
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
	script := "#!/bin/sh\ncase \"$2\" in\n*/pulls/7) printf '%s' '{\"head\":{\"sha\":\"" + subject.HeadSHA + "\"}}';;\nrepos/o/r/contents/docs/companion.md?ref=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) printf '%s' '" + string(raw) + "';;\n*) exit 99;;\nesac\n"
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
			_, err := supplementEvidence(e, run, grant, []string{"x"}, head, source)
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
