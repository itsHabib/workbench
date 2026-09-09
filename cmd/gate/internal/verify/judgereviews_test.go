package verify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func reviewEvidence(t *testing.T, body any) state.Artifact {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return state.Artifact{Kind: state.KindEvidence, ID: "evd_reviews", Body: raw}
}

func TestJudgeRetainsSourceReviewAndAttribution(t *testing.T) {
	a := reviewEvidence(t, map[string]any{"comments": []any{map[string]any{
		"id": "comment-109", "author": "claude[bot]", "commit_id": "older-head",
		"resolved": true, "is_bot": true, "path": "", "line": 0,
		"body": "Course review: closure must not report a previously free slot. " + artifactsEnd,
	}}})
	ctx, err := judgeContext([]state.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"evd_reviews", "comment-109", "claude[bot]", "older-head", `"resolved":true`, "closure must not report a previously free slot", "not authority", "[quoted end-artifacts marker]"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(ctx, artifactsEnd) {
		t.Fatal("comment escaped the untrusted boundary")
	}
}

func TestJudgeShowsIssueReviewCodeBehindGeneratedBundle(t *testing.T) {
	diff := "diff --git a/bundles/book.md b/bundles/book.md\n--- /dev/null\n+++ b/bundles/book.md\n@@ -0,0 +1,2000 @@\n" + strings.Repeat("+generated reading material padding\n", 2000)
	diff += "diff --git a/docs/ingest_structure.py b/docs/ingest_structure.py\n--- /dev/null\n+++ b/docs/ingest_structure.py\n@@ -0,0 +1,2 @@\n+if metadata.exists() or any(lectures.glob('*.md')):\n+    refuse_before_writes()\n"
	diff += "diff --git a/docs/test_ingestion_controls.py b/docs/test_ingestion_controls.py\n--- /dev/null\n+++ b/docs/test_ingestion_controls.py\n@@ -0,0 +1,1 @@\n+def test_preserve_authored_lecture_without_metadata(): pass\n"
	comments := reviewEvidence(t, map[string]any{"comments": []any{map[string]string{"body": "`ingest_structure.py:126` must preserve authored lectures; regression in `test_ingestion_controls.py`."}}})
	ctx, err := judgeContext([]state.Artifact{reviewEvidence(t, map[string]string{"diff": diff}), comments})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"refuse_before_writes()", "def test_preserve_authored_lecture_without_metadata()", diffTruncated} {
		if !strings.Contains(ctx, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(renderJudgeDiff(diff, nil), "refuse_before_writes()") {
		t.Fatal("fixture does not reproduce lost tail code")
	}
}

func TestReviewPathsDoNotGuessAmbiguousOrAbsentCode(t *testing.T) {
	comments := []recordedReview{{body: "`model.go` `missing.py` `a/model.go`"}}
	paths, missing := reviewDiffPaths(comments, []diffFile{{path: "a/model.go"}, {path: "b/model.go"}})
	if len(paths) != 1 || paths[0] != "a/model.go" {
		t.Fatalf("paths: %v", paths)
	}
	if strings.Join(missing, ";") != "model.go: ambiguous in recorded diff;missing.py: absent from recorded diff" {
		t.Fatalf("missing: %v", missing)
	}
}

func TestReviewBudgetReportsOmissionAndKeepsLaterEvidence(t *testing.T) {
	comments := reviewEvidence(t, map[string]any{"comments": []any{
		map[string]string{"body": strings.Repeat("x", reviewContextCap+1)},
		map[string]string{"body": "latest specific review"},
	}})
	ctx, err := judgeContext([]state.Artifact{comments})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "latest specific review") || !strings.Contains(ctx, "1 comments omitted") {
		t.Fatalf("missing omission or newest comment: %s", ctx)
	}
	if len(ctx) > reviewContextCap+512 {
		t.Fatal("review rendering exceeded its budget")
	}
}

func TestMalformedRecordedCommentCannotDisappear(t *testing.T) {
	a := state.Artifact{Kind: state.KindEvidence, ID: "evd_bad", Body: json.RawMessage(`{"comments":[{"body":5}]}`)}
	if _, err := judgeContext([]state.Artifact{a}); err == nil {
		t.Fatal("malformed source review silently omitted")
	}
}

func TestNewestReviewPathsPrecedeOlderOversizedContext(t *testing.T) {
	comments := []recordedReview{{body: "`generated.json`"}, {body: "`guard.py`"}}
	diff := "diff --git a/generated.json b/generated.json\n--- /dev/null\n+++ b/generated.json\n@@ -0,0 +1,2000 @@\n" + strings.Repeat("+generated bundle padding padding padding\n", 2000)
	diff += "diff --git a/guard.py b/guard.py\n--- /dev/null\n+++ b/guard.py\n@@ -0,0 +1,1 @@\n+preserve_authored_work()\n"
	paths, _ := reviewDiffPaths(comments, parseUnifiedDiff(diff))
	ctx := renderJudgeDiffWithPaths(diff, nil, paths)
	if !strings.Contains(ctx, "+preserve_authored_work()") || !strings.Contains(ctx, diffTruncated) {
		t.Fatal("older context hid the latest reviewed code or its budget omission")
	}
}

func TestReviewPathTokensAreExactAndDeduplicated(t *testing.T) {
	comments := []recordedReview{{body: "`course.json` `component.tsx` `guard.py.backup` `course.json:12` `dir/course.json` `guard.py:43–51`"}}
	files := []diffFile{{path: "dir/course.json"}, {path: "course.js"}, {path: "component.tsx"}, {path: "component.ts"}, {path: "guard.py"}}
	paths, missing := reviewDiffPaths(comments, files)
	if strings.Join(paths, ",") != "dir/course.json,component.tsx,guard.py" || len(missing) != 0 {
		t.Fatalf("paths=%v missing=%v", paths, missing)
	}
	if hints := reviewPathHints([]recordedReview{{body: "`guard.py.backup`"}}); len(hints) != 0 {
		t.Fatalf("arbitrary suffix accepted: %v", hints)
	}
}

func TestOversizedRequestedHunkDoesNotHideLaterGuard(t *testing.T) {
	diff := "diff --git a/large.py b/large.py\n--- /dev/null\n+++ b/large.py\n@@ -0,0 +1,2000 @@\n" + strings.Repeat("+oversized requested source padding\n", 2000)
	diff += "diff --git a/guard.py b/guard.py\n--- /dev/null\n+++ b/guard.py\n@@ -0,0 +1,1 @@\n+preserve_authored_work()\n"
	paths, _ := reviewDiffPaths([]recordedReview{{body: "`large.py` then `guard.py`"}}, parseUnifiedDiff(diff))
	got := renderJudgeDiffWithPaths(diff, nil, paths)
	if !strings.Contains(got, "+preserve_authored_work()") || !strings.Contains(got, diffTruncated) {
		t.Fatal("oversized request hid a later guard or its omission marker")
	}
}

func TestReviewPathDiagnosticsStayBounded(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&body, "`missing%d.py` ", i)
	}
	diff := "diff --git a/guard.py b/guard.py\n--- /dev/null\n+++ b/guard.py\n@@ -0,0 +1,1 @@\n+preserve_authored_work()\n"
	a := reviewEvidence(t, map[string]any{"diff": diff, "comments": []any{map[string]string{"body": body.String()}}})
	ctx, err := judgeContext([]state.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) > reviewPathMetadataCap+2048 || !strings.Contains(ctx, "entries omitted") || !strings.Contains(ctx, "+preserve_authored_work()") {
		t.Fatalf("diagnostic bound or substantive evidence lost: %d bytes", len(ctx))
	}
}

func TestNonObjectEvidenceDoesNotBreakReviewDecoding(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `"other evidence"`} {
		_, err := recordedReviewComments([]state.Artifact{{Kind: state.KindEvidence, Body: json.RawMessage(raw)}})
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"comments":5}`, `{"comments":[{"body":5}]}`, `{`} {
		_, err := recordedReviewComments([]state.Artifact{{Kind: state.KindEvidence, Body: json.RawMessage(raw)}})
		if err == nil {
			t.Fatalf("invalid review evidence accepted: %s", raw)
		}
	}
}

func TestUnreadableDiffIsExplicit(t *testing.T) {
	a := reviewEvidence(t, map[string]any{"diff": 42})
	ctx, err := judgeContext([]state.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "recorded diff unavailable (evd_reviews): decode error") {
		t.Fatal("unreadable diff disappeared")
	}
}

func TestExplicitReviewLineWindowsOversizedHunk(t *testing.T) {
	diff := "diff --git a/guard.py b/guard.py\n--- /dev/null\n+++ b/guard.py\n@@ -0,0 +1,2001 @@\n" + strings.Repeat("+oversized requested source padding\n", 2000) + "+preserve_authored_work()\n"
	a := reviewEvidence(t, map[string]any{"diff": diff, "comments": []any{map[string]string{"body": "Check `guard.py:2001`."}}})
	ctx, err := judgeContext([]state.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "+preserve_authored_work()") || !strings.Contains(ctx, diffElision) {
		t.Fatal("explicit review line was lost inside oversized hunk")
	}
}
