package evidence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

const (
	judgedHead = "3d1152e0a609c18b1d99874bead070022388c04d"
	liveBase   = "c8b7edc52e020217ce325d51d01da94e1ea757ef"
	forkPoint  = "c6523b7944f964a100fbb3421a21527fb93f36c0"
)

// liveBaseResponse is the GraphQL answer for PR 115 in state, targeting main
// at oid. A null oid renders a PR whose base branch has no ref.
func liveBaseResponse(state, oid string) string {
	ref := `null`
	if oid != "" {
		ref = `{"target":{"oid":"` + oid + `"}}`
	}
	return `{"data":{"repository":{"pullRequest":{"state":"` + state + `","baseRefName":"main","baseRef":` + ref + `}}}}`
}

// comparePath is the one compare readBaseHead may ask for: the live base head
// against the judged head, both exact SHAs.
const comparePath = "repos/o/r/compare/" + liveBase + "..." + judgedHead + "?per_page=1"

// scriptedGH answers the live-base query and the compare from fixed bodies and
// records every call, so a test can assert what was asked — not only what came
// back. An empty body is an unexpected call.
type scriptedGH struct {
	graphql, compare string
	fail             error
	calls            []string
}

func (s *scriptedGH) read(args ...string) (json.RawMessage, error) {
	call := strings.Join(args, " ")
	s.calls = append(s.calls, call)
	if s.fail != nil {
		return nil, s.fail
	}
	switch {
	case len(args) > 1 && args[1] == "graphql" && s.graphql != "":
		return json.RawMessage(s.graphql), nil
	case len(args) == 2 && args[1] == comparePath && s.compare != "":
		return json.RawMessage(s.compare), nil
	}
	return nil, errors.New("evidence: gh [api x]: HTTP 404 Not Found: unexpected " + call)
}

func TestReadBaseHeadRecordsTheLiveBaseAgainstTheJudgedHead(t *testing.T) {
	cases := []struct {
		name, compare string
		want          BaseHeadBody
	}{
		{
			name:    "head contains the base",
			compare: `{"status":"ahead","ahead_by":3,"behind_by":0,"merge_base_commit":{"sha":"` + liveBase + `"}}`,
			want:    BaseHeadBody{Status: "ahead", MergeBaseSHA: liveBase},
		},
		{
			name:    "base moved past the head (ivy#115 at 16:30Z)",
			compare: `{"status":"diverged","ahead_by":3,"behind_by":7,"merge_base_commit":{"sha":"` + forkPoint + `"}}`,
			want:    BaseHeadBody{Status: "diverged", BehindBy: 7, MergeBaseSHA: forkPoint},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &scriptedGH{graphql: liveBaseResponse("OPEN", liveBase), compare: tc.compare}
			got, err := readBaseHead(PRRef{Repo: "o/r", Number: 115}, judgedHead, gh.read)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			want.PR, want.State, want.BaseRef, want.BaseSHA, want.HeadSHA = PRRef{Repo: "o/r", Number: 115}, "OPEN", "main", liveBase, judgedHead
			if got != want {
				t.Fatalf("got %+v\nwant %+v", got, want)
			}
			if len(gh.calls) != 2 || !strings.Contains(gh.calls[0], "baseRef{target{oid}}") ||
				!strings.Contains(gh.calls[0], "owner=o") || !strings.Contains(gh.calls[0], "number=115") {
				t.Fatalf("want the live-base query then the exact-SHA compare, got %q", gh.calls)
			}
		})
	}
}

// A merged PR's merge is history: there is nothing to compare, and its base
// branch may be gone, so the read records the state and stops.
func TestReadBaseHeadSkipsTheCompareForAMergedPR(t *testing.T) {
	gh := &scriptedGH{graphql: liveBaseResponse("MERGED", "")}
	got, err := readBaseHead(PRRef{Repo: "o/r", Number: 115}, judgedHead, gh.read)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "MERGED" || len(gh.calls) != 1 {
		t.Fatalf("a merged PR records its state and makes no compare: %+v after %q", got, gh.calls)
	}
}

// Every way the read can fall short is an error, never a body: the caller is
// about to authorize a merge, and an unread base must not pass as an unmoved one.
func TestReadBaseHeadFailsClosed(t *testing.T) {
	contained := `{"status":"ahead","behind_by":0,"merge_base_commit":{"sha":"` + liveBase + `"}}`
	cases := []struct {
		name, head string
		gh         *scriptedGH
		wantErr    string
	}{
		{"judged head is not a commit id", "abc", &scriptedGH{}, "not a commit id"},
		{"transport failure", judgedHead, &scriptedGH{fail: errors.New("evidence: gh [api graphql]: HTTP 502")}, "HTTP 502"},
		{"PR not found", judgedHead, &scriptedGH{graphql: `{"data":{"repository":{"pullRequest":null}}}`}, "not found"},
		{"open PR whose base has no ref", judgedHead, &scriptedGH{graphql: liveBaseResponse("OPEN", "")}, "no readable head"},
		{"compare without behind_by", judgedHead, &scriptedGH{graphql: liveBaseResponse("OPEN", liveBase),
			compare: `{"status":"ahead","merge_base_commit":{"sha":"` + liveBase + `"}}`}, "missing"},
		{"compare without status", judgedHead, &scriptedGH{graphql: liveBaseResponse("OPEN", liveBase),
			compare: `{"behind_by":0,"merge_base_commit":{"sha":"` + liveBase + `"}}`}, "missing"},
		{"compare unanswered", judgedHead, &scriptedGH{graphql: liveBaseResponse("OPEN", liveBase)}, "HTTP 404"},
		{"malformed live base", judgedHead, &scriptedGH{graphql: `{"data":`, compare: contained}, "parse live base"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readBaseHead(PRRef{Repo: "o/r", Number: 115}, tc.head, tc.gh.read)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// The read lands in state as the body verify decodes, under the gate run.
func TestBaseHeadAppendsTheReadAsEvidence(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	gh := &scriptedGH{
		graphql: liveBaseResponse("OPEN", liveBase),
		compare: `{"status":"diverged","behind_by":7,"merge_base_commit":{"sha":"` + forkPoint + `"}}`,
	}
	id, err := baseHeadFrom(st, "run_b", PRRef{Repo: "o/r", Number: 115}, judgedHead, gh.read)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	var body BaseHeadBody
	if err := json.Unmarshal(a.Body, &body); err != nil {
		t.Fatal(err)
	}
	if a.Kind != state.KindEvidence || a.Run != "run_b" || body.BaseSHA != liveBase || body.BehindBy != 7 {
		t.Fatalf("recorded %s under %s: %+v", a.Kind, a.Run, body)
	}
}
