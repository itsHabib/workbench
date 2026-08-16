package evidence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// fetchTable answers from a fixed path→response table. An unlisted path is an
// error, so a test only has to state the endpoint it means to exercise.
func fetchTable(t *testing.T, responses map[string]string, failures map[string]error) protectionFetch {
	t.Helper()
	return func(path string) (json.RawMessage, error) {
		if err, ok := failures[path]; ok {
			return nil, err
		}
		body, ok := responses[path]
		if !ok {
			return nil, errors.New("evidence: gh [api " + path + "]: HTTP 404 Not Found")
		}
		return json.RawMessage(body), nil
	}
}

const (
	protPath  = "repos/o/r/branches/main/protection"
	rulesPath = "repos/o/r/rules/branches/main"
)

func TestReadProtectionClassifiesEachShape(t *testing.T) {
	notFound := errors.New("evidence: gh [api x]: gh: Branch not protected (HTTP 404)")
	cases := []struct {
		name      string
		responses map[string]string
		failures  map[string]error
		want      ProtectionBody
	}{
		{
			name:      "classic protection, strict",
			responses: map[string]string{protPath: `{"required_status_checks":{"strict":true,"contexts":["ci"]}}`},
			want:      ProtectionBody{Readable: true, Strict: true, Source: ProtectionSourceBranch},
		},
		{
			name:      "classic protection, not strict",
			responses: map[string]string{protPath: `{"required_status_checks":{"strict":false,"contexts":["Go"]}}`},
			want:      ProtectionBody{Readable: true, Source: ProtectionSourceBranch},
		},
		{
			name:      "protected with no required checks at all",
			responses: map[string]string{protPath: `{"enforce_admins":{"enabled":false}}`},
			want:      ProtectionBody{Readable: true, Source: ProtectionSourceBranch},
		},
		{
			name: "no classic protection, strict ruleset",
			responses: map[string]string{rulesPath: `[{"type":"pull_request"},
				{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true}}]`},
			failures: map[string]error{protPath: notFound},
			want:     ProtectionBody{Readable: true, Strict: true, Source: ProtectionSourceRuleset},
		},
		{
			name: "no classic protection, non-strict ruleset",
			responses: map[string]string{rulesPath: `[{"type":"required_status_checks",
				"parameters":{"strict_required_status_checks_policy":false}}]`},
			failures: map[string]error{protPath: notFound},
			want:     ProtectionBody{Readable: true, Source: ProtectionSourceNone},
		},
		{
			name:      "unprotected branch, no rulesets",
			responses: map[string]string{rulesPath: `[]`},
			failures:  map[string]error{protPath: notFound},
			want:      ProtectionBody{Readable: true, Source: ProtectionSourceNone},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readProtection(PRRef{Repo: "o/r", Number: 7}, "main",
				fetchTable(t, c.responses, c.failures))
			got.Note = ""
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// A read that fails for any reason OTHER than "not protected" leaves the
// setting unknown. Unknown must never read as "no requirement" — a later
// consumer that hardened on it would block merges on a permission gate has,
// and one that trusted it would emit the doomed command this rung exists to
// prevent. Both are wrong; the evidence says so explicitly instead.
func TestReadProtectionDegradesOnUnreadableEndpoints(t *testing.T) {
	forbidden := errors.New("evidence: gh [api x]: HTTP 403: Resource not accessible")
	cases := []struct {
		name      string
		responses map[string]string
		failures  map[string]error
		baseRef   string
	}{
		{name: "protection forbidden", failures: map[string]error{protPath: forbidden}, baseRef: "main"},
		{
			name:     "rules endpoint fails after a 404 on protection",
			failures: map[string]error{protPath: errors.New("HTTP 404"), rulesPath: forbidden},
			baseRef:  "main",
		},
		{
			name:      "protection body carries no strict field",
			responses: map[string]string{protPath: `{"required_status_checks":{"contexts":["ci"]}}`},
			baseRef:   "main",
		},
		{
			name:      "protection body is not JSON",
			responses: map[string]string{protPath: `not-json`},
			baseRef:   "main",
		},
		{
			name:      "rules body is not JSON",
			responses: map[string]string{rulesPath: `{}`},
			failures:  map[string]error{protPath: errors.New("HTTP 404")},
			baseRef:   "main",
		},
		{name: "view recorded no base branch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readProtection(PRRef{Repo: "o/r", Number: 7}, c.baseRef,
				fetchTable(t, c.responses, c.failures))
			if got.Readable || got.Strict {
				t.Fatalf("an unreadable setting must not be reported as fact: %+v", got)
			}
			if got.Note == "" {
				t.Fatal("an unreadable read must record why")
			}
		})
	}
}

func TestProtectionRecordsEvidence(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	pr := PRRef{Repo: "o/r", Number: 7}
	id, err := protectionFrom(st, "run_t", pr, "main", fetchTable(t,
		map[string]string{protPath: `{"required_status_checks":{"strict":true}}`}, nil))
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	var body ProtectionBody
	if err := json.Unmarshal(a.Body, &body); err != nil {
		t.Fatal(err)
	}
	if !body.Readable || !body.Strict || body.Source != ProtectionSourceBranch {
		t.Fatalf("recorded evidence lost the read: %+v", body)
	}
	if body.PR != pr || body.BaseRef != "main" {
		t.Fatalf("recorded evidence must name its subject and base: %+v", body)
	}
}

func TestBaseRefReadsTheView(t *testing.T) {
	if got := BaseRef(json.RawMessage(`{"baseRefName":"main"}`)); got != "main" {
		t.Fatalf("got %q, want main", got)
	}
	if got := BaseRef(json.RawMessage(`{`)); got != "" {
		t.Fatalf("an unparsable view must yield no base, got %q", got)
	}
}

func TestNotProtectedOnlyMatches404(t *testing.T) {
	if !notProtected(errors.New("gh: Branch not protected (HTTP 404)")) {
		t.Fatal("a 404 is GitHub's not-protected answer")
	}
	if notProtected(errors.New("HTTP 403")) || notProtected(nil) {
		t.Fatal("only a 404 means not protected")
	}
}

// strictFromRules must ignore rules of other types, so an unrelated ruleset
// entry cannot be read as an up-to-date requirement.
func TestStrictFromRulesIgnoresOtherRuleTypes(t *testing.T) {
	strict, err := strictFromRules(json.RawMessage(
		`[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if strict {
		t.Fatal("unrelated rules must not imply an up-to-date requirement")
	}
}

func TestStrictFromProtectionRejectsDrift(t *testing.T) {
	_, err := strictFromProtection(json.RawMessage(`{"required_status_checks":{}}`))
	if err == nil {
		t.Fatal("a present required_status_checks with no strict field is drift, not false")
	}
	if !strings.Contains(err.Error(), "strict field") {
		t.Fatalf("the drift error must name what was missing: %v", err)
	}
}
