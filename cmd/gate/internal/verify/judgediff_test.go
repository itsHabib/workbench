package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	out := h.window([]int{397})
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
	out := h.window([]int{11})
	if !strings.Contains(out, "added_at_11") || strings.Contains(out, diffElision) {
		t.Fatalf("small hunk should render whole without elision: %q", out)
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

// regressionCase pins a real gate run that auto-blocked on procedure: the cited
// findings' code sat past the old 16 KB head-truncation, so the judge never saw
// whether the fix landed. Each needle is the verbatim source line at a cited
// locus; the window must surface every one.
type regressionCase struct {
	run     string
	pr      string
	fixture string
	loci    []locusRef
	needles []string
}

func regressionCases() []regressionCase {
	return []regressionCase{
		{
			run:     "run_349c240be6b2cab8",
			pr:      "102",
			fixture: "pr102.diff",
			loci: []locusRef{
				{"src/main.rs", 1266},
				{"src/restore_exec.rs", 397},
				{"src/firecracker.rs", 1738},
			},
			needles: []string{
				"vm.guard_mut().dismiss();",
				"firecracker::remove_egress_and_tap(&tap)",
				"create_slot_tap(slot)?;",
			},
		},
		{
			run:     "run_c2f2ffaf551ceaba",
			pr:      "101",
			fixture: "pr101.diff",
			loci: []locusRef{
				{"src/rootfs.rs", 71},
				{"src/rootfs.rs", 72},
				{"scripts/setup-rooms-host.sh", 20},
			},
			needles: []string{
				"if !elf && !arm64_image {",
				"return Err(RootfsError::KernelBadFormat {",
				`FIRECRACKER_VERSION="${FIRECRACKER_VERSION:-v1.15.0}"`,
			},
		},
	}
}

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The regression: on the exact diffs that parked #101/#102, every cited locus's
// current code reaches the judge, and the whole quote stays within budget. Under
// the old head-truncation the tail loci were dropped — proven by needleBeyondCap
// below — so the judge blocked on "cannot confirm the fix".
func TestRenderJudgeDiffSurfacesCitedLociFromRealRuns(t *testing.T) {
	for _, tc := range regressionCases() {
		t.Run(tc.pr, func(t *testing.T) {
			diff := loadFixture(t, tc.fixture)
			out := renderJudgeDiff(diff, tc.loci)
			for _, n := range tc.needles {
				if !strings.Contains(out, n) {
					t.Errorf("%s (%s): windowed diff omits cited-locus code %q", tc.run, tc.pr, n)
				}
			}
			// Budget holds: at most one over-budget chunk is refused, so the
			// output never exceeds the cap by more than the truncation note.
			if over := len(out) - (judgeDiffCap + len(diffTruncated) + 1); over > 0 {
				t.Errorf("%s: rendered diff %d bytes over cap", tc.pr, over)
			}
		})
	}
}

// The failure being fixed, made concrete: at least one cited needle in each run
// sat beyond the old 16 KB head-truncation window, so the previous judge context
// could not contain it. Guards against a future cap change silently masking the
// regression these fixtures pin.
func TestRealRunsExerciseTheTruncationFailure(t *testing.T) {
	const oldCap = 16 * 1024
	for _, tc := range regressionCases() {
		t.Run(tc.pr, func(t *testing.T) {
			diff := loadFixture(t, tc.fixture)
			var beyond int
			for _, n := range tc.needles {
				if idx := strings.Index(diff, n); idx < 0 || idx >= oldCap {
					beyond++
				}
			}
			if beyond == 0 {
				t.Fatalf("%s: no cited needle sits beyond the old %d-byte cut; fixture no longer exercises the bug", tc.pr, oldCap)
			}
		})
	}
}

// judgeContext is the seam AutoJudge feeds the provider. Reconstructing it from a
// verdict carrying the cited loci plus the recorded diff evidence — the shape a
// real parked run holds — must place every cited locus's code in the provider
// context.
func TestJudgeContextGroundsCitedLociInDiff(t *testing.T) {
	for _, tc := range regressionCases() {
		t.Run(tc.pr, func(t *testing.T) {
			diff := loadFixture(t, tc.fixture)
			var findings []Finding
			for _, l := range tc.loci {
				findings = append(findings, Finding{Title: "cited", Locus: l.path + ":" + itoa(l.line)})
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
			for _, n := range tc.needles {
				if !strings.Contains(ctx, n) {
					t.Errorf("%s: judge context omits cited-locus code %q", tc.pr, n)
				}
			}
		})
	}
}
