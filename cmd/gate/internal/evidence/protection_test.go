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

// errNotProtected is GitHub's specific answer for a branch with no classic
// protection — the one 404 that is a fact about the branch.
var errNotProtected = errors.New("evidence: gh [api x]: gh: Branch not protected (HTTP 404)")

// noRules is the rules endpoint's answer for a branch with no rulesets: an
// empty list, never an error.
const noRules = `[]`

func TestReadProtectionClassifiesEachShape(t *testing.T) {
	cases := []struct {
		name      string
		responses map[string]string
		failures  map[string]error
		want      ProtectionBody
	}{
		{
			name: "classic protection, strict",
			responses: map[string]string{
				protPath:  `{"required_status_checks":{"strict":true,"contexts":["ci"]}}`,
				rulesPath: noRules,
			},
			want: ProtectionBody{Readable: true, Strict: true, Source: ProtectionSourceBranch},
		},
		{
			name: "classic protection, not strict",
			responses: map[string]string{
				protPath:  `{"required_status_checks":{"strict":false,"contexts":["Go"]}}`,
				rulesPath: noRules,
			},
			want: ProtectionBody{Readable: true, Source: ProtectionSourceBranch},
		},
		{
			name: "protected, but no required status checks configured",
			responses: map[string]string{
				protPath:  `{"enforce_admins":{"enabled":false}}`,
				rulesPath: noRules,
			},
			want: ProtectionBody{Readable: true, Source: ProtectionSourceBranch},
		},
		{
			name: "no classic protection, strict ruleset",
			responses: map[string]string{rulesPath: `[{"type":"pull_request"},
				{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true}}]`},
			failures: map[string]error{protPath: errNotProtected},
			want:     ProtectionBody{Readable: true, Strict: true, Source: ProtectionSourceRuleset},
		},
		{
			name: "no classic protection, non-strict ruleset",
			responses: map[string]string{rulesPath: `[{"type":"required_status_checks",
				"parameters":{"strict_required_status_checks_policy":false}}]`},
			failures: map[string]error{protPath: errNotProtected},
			want:     ProtectionBody{Readable: true, Source: ProtectionSourceRuleset},
		},
		{
			name:      "unprotected branch, no rulesets",
			responses: map[string]string{rulesPath: noRules},
			failures:  map[string]error{protPath: errNotProtected},
			want:      ProtectionBody{Readable: true, Source: ProtectionSourceNone},
		},
		{
			name: "both mechanisms present, neither strict",
			responses: map[string]string{
				protPath: `{"required_status_checks":{"strict":false,"contexts":["ci"]}}`,
				rulesPath: `[{"type":"required_status_checks",
					"parameters":{"strict_required_status_checks_policy":false}}]`,
			},
			want: ProtectionBody{Readable: true, Source: ProtectionSourceBranch + "+" + ProtectionSourceRuleset},
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

// The mechanisms are a UNION, not alternatives: a base can carry classic
// protection that is not strict AND a ruleset that is. Reading only the classic
// answer would record the base as non-strict and emit the merge command GitHub
// rejects — the friction this rung exists to remove.
func TestReadProtectionUnionsStrictnessAcrossMechanisms(t *testing.T) {
	strictRules := `[{"type":"required_status_checks",
		"parameters":{"strict_required_status_checks_policy":true}}]`
	cases := []struct {
		name       string
		responses  map[string]string
		failures   map[string]error
		wantSource string
	}{
		{
			name: "non-strict classic, strict ruleset",
			responses: map[string]string{
				protPath:  `{"required_status_checks":{"strict":false,"contexts":["ci"]}}`,
				rulesPath: strictRules,
			},
			wantSource: ProtectionSourceRuleset,
		},
		{
			name: "strict classic, non-strict ruleset",
			responses: map[string]string{
				protPath: `{"required_status_checks":{"strict":true}}`,
				rulesPath: `[{"type":"required_status_checks",
					"parameters":{"strict_required_status_checks_policy":false}}]`,
			},
			wantSource: ProtectionSourceBranch,
		},
		{
			name: "both strict",
			responses: map[string]string{
				protPath:  `{"required_status_checks":{"strict":true}}`,
				rulesPath: strictRules,
			},
			wantSource: ProtectionSourceBranch + "+" + ProtectionSourceRuleset,
		},
		{
			// A strict answer already establishes the requirement; the unread
			// mechanism could only add another one gate is honouring anyway.
			name:       "strict ruleset with an unreadable classic endpoint",
			responses:  map[string]string{rulesPath: strictRules},
			failures:   map[string]error{protPath: errors.New("HTTP 403")},
			wantSource: ProtectionSourceRuleset,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readProtection(PRRef{Repo: "o/r", Number: 7}, "main",
				fetchTable(t, c.responses, c.failures))
			if !got.Readable || !got.Strict {
				t.Fatalf("any strict mechanism makes the base strict: %+v", got)
			}
			if got.Source != c.wantSource {
				t.Fatalf("source %q, want %q", got.Source, c.wantSource)
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
	bare404 := errors.New("evidence: gh [api x]: HTTP 404: Not Found")
	cases := []struct {
		name      string
		responses map[string]string
		failures  map[string]error
		baseRef   string
	}{
		{
			name:      "protection forbidden",
			responses: map[string]string{rulesPath: noRules},
			failures:  map[string]error{protPath: forbidden},
			baseRef:   "main",
		},
		{
			// A missing repo, a mistyped branch, and a token that cannot read
			// protection all answer a bare 404. None of them is evidence the
			// branch is unprotected.
			name:      "bare 404 on the protection endpoint",
			responses: map[string]string{rulesPath: noRules},
			failures:  map[string]error{protPath: bare404},
			baseRef:   "main",
		},
		{
			name:     "rules endpoint fails",
			failures: map[string]error{protPath: errNotProtected, rulesPath: forbidden},
			baseRef:  "main",
		},
		{
			// Every mechanism must be read before "nothing requires it" is a
			// fact, so a readable non-strict classic answer does not rescue an
			// unreadable ruleset endpoint.
			name:      "classic readable and non-strict, rules unreadable",
			responses: map[string]string{protPath: `{"required_status_checks":{"strict":false}}`},
			failures:  map[string]error{rulesPath: forbidden},
			baseRef:   "main",
		},
		{
			name:      "protection body carries no strict field",
			responses: map[string]string{protPath: `{"required_status_checks":{"contexts":["ci"]}}`, rulesPath: noRules},
			baseRef:   "main",
		},
		{
			name:      "protection body is not JSON",
			responses: map[string]string{protPath: `not-json`, rulesPath: noRules},
			baseRef:   "main",
		},
		{
			name:      "rules body is not JSON",
			responses: map[string]string{rulesPath: `{}`},
			failures:  map[string]error{protPath: errNotProtected},
			baseRef:   "main",
		},
		{
			name: "a rule carries no strict policy field",
			responses: map[string]string{
				protPath:  `{"required_status_checks":{"strict":false}}`,
				rulesPath: `[{"type":"required_status_checks","parameters":{}}]`,
			},
			baseRef: "main",
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
	id, err := protectionFrom(st, "run_t", pr, "main", fetchTable(t, map[string]string{
		protPath: `{"required_status_checks":{"strict":true}}`, rulesPath: noRules,
	}, nil))
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

func TestNotProtectedMatchesOnlyGitHubsAnswer(t *testing.T) {
	if !notProtected(errors.New("gh: Branch not protected (HTTP 404)")) {
		t.Fatal("this IS GitHub's not-protected answer")
	}
	for _, err := range []error{
		errors.New("HTTP 404: Not Found"),
		errors.New("HTTP 403: Resource not accessible"),
		errors.New("Branch not protected"),
		nil,
	} {
		if notProtected(err) {
			t.Fatalf("a generic failure is not proof the branch is unprotected: %v", err)
		}
	}
}

// statusCheckRules must ignore rules of other types, so an unrelated ruleset
// entry cannot be read as an up-to-date requirement — or as a status-check rule
// that was present at all.
func TestStatusCheckRulesIgnoresOtherRuleTypes(t *testing.T) {
	rules, err := statusCheckRules(json.RawMessage(
		`[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if rules.strict || rules.present {
		t.Fatalf("unrelated rules must not imply a status-check requirement: %+v", rules)
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
