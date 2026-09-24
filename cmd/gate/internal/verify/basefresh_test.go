package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

const (
	freshHead = "3d1152e0a609c18b1d99874bead070022388c04d"
	freshBase = "c8b7edc52e020217ce325d51d01da94e1ea757ef"
)

var freshSubject = Subject{Repo: "o/r", Number: 115, HeadSHA: freshHead}

// baseRead records one base read for freshSubject's PR, with the fields a test
// overrides layered over an open PR targeting main at freshBase.
func baseRead(t *testing.T, fields map[string]any) (*state.Store, string) {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"pr":    map[string]any{"repo": "o/r", "number": 115},
		"state": "OPEN", "base_ref": "main", "base_sha": freshBase, "head_sha": freshHead,
		"merge_base_sha": freshBase, "status": "ahead", "behind_by": 0,
	}
	for k, v := range fields {
		body[k] = v
	}
	a, err := st.Append(state.KindEvidence, "run_f", nil, body)
	if err != nil {
		t.Fatal(err)
	}
	return st, a.ID
}

// Fresh means exactly one thing: GitHub's compare says the judged head contains
// the base's head. Every other answer — including a status this code has never
// seen, and fields that disagree with each other — is not fresh.
func TestBaseFreshnessDecidesOnContainmentOnly(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		fresh  bool
	}{
		{"head contains the base", nil, true},
		{"head is the base", map[string]any{"status": "identical"}, true},
		{"base moved past the head", map[string]any{"status": "diverged", "behind_by": 7}, false},
		{"head is behind the base", map[string]any{"status": "behind", "behind_by": 2}, false},
		{"fields disagree", map[string]any{"status": "ahead", "behind_by": 2}, false},
		{"unknown status", map[string]any{"status": "tangled"}, false},
		{"merged PR has no base to move", map[string]any{"state": "MERGED", "status": "diverged", "behind_by": 9}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, id := baseRead(t, tc.fields)
			got, err := BaseFreshness(st, id, freshSubject)
			if err != nil {
				t.Fatal(err)
			}
			if got.Fresh != tc.fresh {
				t.Fatalf("fresh = %v, want %v: %s", got.Fresh, tc.fresh, got.Why)
			}
			if got.Why == "" {
				t.Fatal("every answer must say what carried it")
			}
		})
	}
}

// The stale answer is what an operator reads at the moment gate refuses: the
// code, which base moved where, how far, and the one fix — and that no
// judgment of this run can substitute for it.
func TestBaseFreshnessNamesTheMoveAndTheFix(t *testing.T) {
	st, id := baseRead(t, map[string]any{"status": "diverged", "behind_by": 7, "merge_base_sha": "c6523b7944f964a100fbb3421a21527fb93f36c0"})
	got, err := BaseFreshness(st, id, freshSubject)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{CodeBaseMoved + ":", "main is at c8b7edc52e02", "head 3d1152e0a609", "7 commit(s)",
		"merge base c6523b7944f9", "Merge main into the branch", "no judgment of this run can clear it"} {
		if !strings.Contains(got.Why, want) {
			t.Fatalf("stale reason must carry %q: %s", want, got.Why)
		}
	}
}

// A read about another PR or another head is a wiring fault. Answering it
// either way would decide this merge on a fact about a different one.
func TestBaseFreshnessRefusesAReadOfAnotherSubject(t *testing.T) {
	st, id := baseRead(t, nil)
	for _, subject := range []Subject{
		{Repo: "o/r", Number: 115, HeadSHA: "0000000000000000000000000000000000000000"},
		{Repo: "o/r", Number: 116, HeadSHA: freshHead},
		{Repo: "o/x", Number: 115, HeadSHA: freshHead},
	} {
		if _, err := BaseFreshness(st, id, subject); err == nil {
			t.Fatalf("a read of o/r#115@%.12s must not answer for %+v", freshHead, subject)
		}
	}
}

// A record missing the facts the answer rests on is an error, never either
// answer — schema drift must not read as a head that contains its base.
func TestBaseFreshnessRefusesAnIncompleteRead(t *testing.T) {
	cases := map[string]map[string]any{
		"no state":     {"state": ""},
		"no base head": {"base_sha": ""},
		"no compare":   {"status": ""},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			st, id := baseRead(t, fields)
			if got, err := BaseFreshness(st, id, freshSubject); err == nil {
				t.Fatalf("an incomplete read must be an error, got %+v", got)
			}
		})
	}
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Append(state.KindEvidence, "run_f", nil, []string{"not", "a", "read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BaseFreshness(st, a.ID, freshSubject); err == nil {
		t.Fatal("a malformed body must be an error")
	}
}
