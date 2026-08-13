package verify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestParseLocus(t *testing.T) {
	cases := []struct {
		in   string
		path string
		line int
		ok   bool
	}{
		{"src/firecracker.rs:1738", "src/firecracker.rs", 1738, true},
		{"a/b/c.go:1", "a/b/c.go", 1, true},
		{"noline.go", "", 0, false},
		{"issue-level", "", 0, false},
		{"trailing:", "", 0, false},
		{":10", "", 0, false},
		{"neg.go:-3", "", 0, false},
		{"zero.go:0", "", 0, false},
		{"win:path:42", "win:path", 42, true},
	}
	for _, tc := range cases {
		got, ok := parseLocus(tc.in)
		if ok != tc.ok || got.path != tc.path || got.line != tc.line {
			t.Errorf("parseLocus(%q) = %+v,%v want {%q %d},%v", tc.in, got, ok, tc.path, tc.line, tc.ok)
		}
	}
}

func TestHunkNewStart(t *testing.T) {
	cases := []struct {
		header string
		want   int
	}{
		{"@@ -1096,6 +1176,163 @@ fn emit_base_created(room_", 1176},
		{"@@ -0,0 +1,851 @@", 1},
		{"@@ -52,11 +54,22 @@ pub fn validate_kernel(path:", 54},
		{"@@ -5 +5 @@", 5},
		{"garbage", 0},
	}
	for _, tc := range cases {
		if got := hunkNewStart(tc.header); got != tc.want {
			t.Errorf("hunkNewStart(%q) = %d want %d", tc.header, got, tc.want)
		}
	}
}

// A very large added hunk (a whole new file) must contribute only a bounded
// window around the cited line, not its entire body — otherwise one hunk eats
// the budget and later cited loci get truncated away, the very failure this
// windowing exists to prevent.
func TestWindowBoundsLargeHunk(t *testing.T) {
	var body []string
	for i := 1; i <= 800; i++ {
		if i == 397 {
			body = append(body, "+        the_cited_marker_line();")
			continue
		}
		body = append(body, "+        filler_line_number_"+itoa(i)+"();")
	}
	h := diffHunk{header: "@@ -0,0 +1,800 @@", newStart: 1, body: body}
	out := h.window([]int{397}, locusContext)
	if !strings.Contains(out, "the_cited_marker_line();") {
		t.Fatal("window dropped the cited line")
	}
	if strings.Contains(out, "filler_line_number_1()") || strings.Contains(out, "filler_line_number_800()") {
		t.Fatal("window kept lines far from the cited line")
	}
	if !strings.Contains(out, diffElision) {
		t.Fatal("window over a large hunk must mark the elision")
	}
	// Around one target the window is ~2*locusContext lines, far under the hunk.
	if got := strings.Count(out, "filler_line_number_"); got > 2*locusContext+2 {
		t.Fatalf("window kept %d filler lines, want <= %d", got, 2*locusContext+2)
	}
}

// A hunk small enough to fit renders whole, untrimmed, with no elision noise.
func TestWindowSmallHunkRendersWhole(t *testing.T) {
	h := diffHunk{
		header:   "@@ -10,2 +10,3 @@ fn f()",
		newStart: 10,
		body:     []string{" ctx", "+added_at_11", " ctx2"},
	}
	out := h.window([]int{11}, locusContext)
	if !strings.Contains(out, "added_at_11") || strings.Contains(out, diffElision) {
		t.Fatalf("small hunk should render whole without elision: %q", out)
	}
}

// One hunk covering two far-apart cited lines keeps a band around each and
// elides the stretch between them — rendering the whole span would let a single
// wide hunk eat the budget the later loci depend on.
func TestWindowElidesBetweenDistantLoci(t *testing.T) {
	var body []string
	for i := 1; i <= 800; i++ {
		switch i {
		case 100:
			body = append(body, "+        first_cited_marker();")
		case 700:
			body = append(body, "+        second_cited_marker();")
		default:
			body = append(body, "+        filler_line_number_"+itoa(i)+"();")
		}
	}
	h := diffHunk{header: "@@ -0,0 +1,800 @@", newStart: 1, body: body}
	out := h.window([]int{100, 700}, locusContext)
	if !strings.Contains(out, "first_cited_marker();") || !strings.Contains(out, "second_cited_marker();") {
		t.Fatal("window dropped a cited line")
	}
	if strings.Contains(out, "filler_line_number_400()") {
		t.Fatal("window rendered the span between two distant loci instead of eliding it")
	}
	if got := strings.Count(out, diffElision); got < 3 {
		t.Fatalf("expected elisions before, between, and after the loci, got %d", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}

// oldDiffCap is the naive head-truncation this change replaced. The synthetic
// diff below deliberately pushes cited loci past it, reproducing the failure that
// auto-blocked itsHabib/rooms #101 (run_c2f2ffaf551ceaba, a rootfs kernel-arch
// finding at ~26 KB) and #102 (run_349c240be6b2cab8, main.rs / restore_exec.rs
// findings at ~31 KB and ~44 KB) — loci the judge could not see, so it blocked on
// "cannot confirm the fix". A byte-for-byte capture of those 95 KB / 29 KB diffs
// is not carried here; the shape that mattered — cited code beyond the cut, one
// locus inside a very large added hunk — is what the fixture recreates.
const oldDiffCap = 16 * 1024

// citedLocus pairs a cited file:line with the verbatim source line the fix must
// let the judge read there.
type citedLocus struct {
	ref    locusRef
	needle string
}

// syntheticParkedDiff builds a multi-file unified diff large enough that the
// cited loci fall past oldDiffCap. host.rs is padded so its cited line, and every
// file after it, sits beyond the old cut; restore.rs is a large added file with
// its cited line deep inside one hunk, exercising within-hunk windowing end to
// end.
func syntheticParkedDiff() (string, []citedLocus) {
	var b strings.Builder
	loci := []citedLocus{
		{locusRef{"src/host.rs", 400}, "let host_arch = uname_machine();"},
		{locusRef{"src/restore.rs", 397}, "firecracker::remove_egress_and_tap(&tap)?;"},
		{locusRef{"src/kill.rs", 150}, "vm.guard_mut().dismiss();"},
	}
	addSyntheticFile(&b, "src/host.rs", 420, 400, loci[0].needle)
	addSyntheticFile(&b, "src/restore.rs", 851, 397, loci[1].needle)
	addSyntheticFile(&b, "src/kill.rs", 200, 150, loci[2].needle)
	return b.String(), loci
}

// addSyntheticFile renders an added-file diff section of n lines; the line at
// mark carries needle, the rest are filler wide enough to cross the old cut fast.
func addSyntheticFile(b *strings.Builder, path string, n, mark int, needle string) {
	fmt.Fprintf(b, "diff --git a/%s b/%s\n", path, path)
	fmt.Fprintf(b, "new file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/%s\n", path)
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", n)
	for i := 1; i <= n; i++ {
		if i == mark {
			fmt.Fprintf(b, "+%s\n", needle)
			continue
		}
		fmt.Fprintf(b, "+    let filler_%d = compute_padding_value(%d);\n", i, i)
	}
}

func lociRefs(cited []citedLocus) []locusRef {
	refs := make([]locusRef, len(cited))
	for i, c := range cited {
		refs[i] = c.ref
	}
	return refs
}

// The regression: every cited locus's current code reaches the judge, and the
// whole quote stays within budget — even though the loci sit past the old cut.
func TestRenderJudgeDiffSurfacesCitedLoci(t *testing.T) {
	diff, cited := syntheticParkedDiff()
	out := renderJudgeDiff(diff, lociRefs(cited))
	for _, c := range cited {
		if !strings.Contains(out, c.needle) {
			t.Errorf("windowed diff omits cited-locus code %q at %s:%d", c.needle, c.ref.path, c.ref.line)
		}
	}
	// The 851-line restore.rs hunk is windowed, not dumped whole: lines far from
	// the cited line are trimmed so one hunk cannot crowd the other loci out.
	if strings.Contains(out, "filler_1 =") || strings.Contains(out, "filler_800 =") {
		t.Error("large added hunk was not windowed around its cited line")
	}
	// At most one over-budget chunk is refused, so the output never exceeds the
	// cap by more than the truncation note.
	if over := len(out) - (judgeDiffCap + len(diffTruncated) + 1); over > 0 {
		t.Errorf("rendered diff %d bytes over cap", over)
	}
}

// The failure being fixed, made concrete: cited loci sit beyond the old 16 KB
// head-truncation window, so the previous judge context could not contain them.
// Guards against the fixture drifting so small it stops exercising the bug.
func TestSyntheticDiffExercisesTheTruncationFailure(t *testing.T) {
	diff, cited := syntheticParkedDiff()
	var beyond int
	for _, c := range cited {
		if idx := strings.Index(diff, c.needle); idx >= oldDiffCap {
			beyond++
		}
	}
	if beyond < 2 {
		t.Fatalf("only %d cited needles sit beyond the old %d-byte cut; fixture no longer reproduces the bug", beyond, oldDiffCap)
	}
}

// When the cited windows collectively exceed the budget, a refused window must
// not abandon the loci after it: later windows fall back to tight ones, so every
// cited locus the budget can possibly carry still reaches the judge.
func TestRenderJudgeDiffKeepsLaterCitedLociUnderPressure(t *testing.T) {
	const nFiles = 24
	var b strings.Builder
	var cited []citedLocus
	for f := 1; f <= nFiles; f++ {
		path := "src/pressure_" + itoa(f) + ".rs"
		needle := "cited_marker_in_file_" + itoa(f) + "();"
		cited = append(cited, citedLocus{locusRef{path, 300}, needle})
		addSyntheticFile(&b, path, 600, 300, needle)
	}
	out := renderJudgeDiff(b.String(), lociRefs(cited))
	for _, c := range cited {
		if !strings.Contains(out, c.needle) {
			t.Errorf("cited locus %s:%d dropped under budget pressure", c.ref.path, c.ref.line)
		}
	}
	if over := len(out) - (judgeDiffCap + len(diffTruncated) + 1); over > 0 {
		t.Errorf("rendered diff %d bytes over cap", over)
	}
}

// A file section with no hunks — binary, mode-only, rename-only, submodule —
// still reaches the judge through its preface; iterating hunks alone would drop
// the change entirely.
func TestRenderJudgeDiffEmitsHunklessFiles(t *testing.T) {
	diff := "diff --git a/img/logo.png b/img/logo.png\n" +
		"index 1111111..2222222 100644\n" +
		"Binary files a/img/logo.png and b/img/logo.png differ\n" +
		"diff --git a/scripts/run.sh b/scripts/run.sh\n" +
		"old mode 100644\n" +
		"new mode 100755\n" +
		"diff --git a/src/lib.rs b/src/lib.rs\n" +
		"index 3333333..4444444 100644\n" +
		"--- a/src/lib.rs\n" +
		"+++ b/src/lib.rs\n" +
		"@@ -1,2 +1,3 @@\n" +
		" fn a() {}\n" +
		"+fn cited_addition() {}\n" +
		" fn b() {}\n"
	out := renderJudgeDiff(diff, []locusRef{{"src/lib.rs", 2}})
	if !strings.Contains(out, "Binary files a/img/logo.png and b/img/logo.png differ") {
		t.Error("binary file section dropped from the judge diff")
	}
	if !strings.Contains(out, "new mode 100755") {
		t.Error("mode-only file section dropped from the judge diff")
	}
	if !strings.Contains(out, "cited_addition") {
		t.Error("cited hunk missing")
	}
}

// judgeContext is the seam AutoJudge feeds the provider. Reconstructed from a
// verdict carrying the cited loci plus the recorded diff evidence — the shape a
// parked run holds — it must place every cited locus's code in the provider
// context.
func TestJudgeContextGroundsCitedLociInDiff(t *testing.T) {
	diff, cited := syntheticParkedDiff()
	var findings []Finding
	for _, c := range cited {
		findings = append(findings, Finding{Title: "cited", Locus: c.ref.path + ":" + itoa(c.ref.line)})
	}
	verdict, err := json.Marshal(Verdict{Source: "review-consolidation", Findings: findings})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(map[string]string{"diff": diff})
	if err != nil {
		t.Fatal(err)
	}
	arts := []state.Artifact{
		{Kind: state.KindVerdict, ID: "vrd_x", Body: verdict},
		{Kind: state.KindEvidence, ID: "evd_x", Body: evidence},
	}
	ctx, err := judgeContext(arts)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cited {
		if !strings.Contains(ctx, c.needle) {
			t.Errorf("judge context omits cited-locus code %q", c.needle)
		}
	}
}
