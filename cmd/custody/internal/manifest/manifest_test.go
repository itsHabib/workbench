package manifest

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadFileValid(t *testing.T) {
	m, err := LoadFile("testdata/valid.json")
	if err != nil {
		t.Fatalf("valid manifest failed to load: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(m.Keys))
	}

	tracker, ok := m.Keys["tracker"]
	if !ok {
		t.Fatal("missing tracker key")
	}
	if tracker.Secret != "wincred:tracker-pat" {
		t.Errorf("secret = %q", tracker.Secret)
	}
	if got := len(tracker.Inject); got != 1 {
		t.Fatalf("inject entries = %d, want 1", got)
	}
	if tracker.Inject[0].Name != "Authorization" {
		t.Errorf("inject name = %q", tracker.Inject[0].Name)
	}
	if tracker.Note == "" {
		t.Error("note not parsed")
	}

	read, ok := tracker.Actions["read"]
	if !ok {
		t.Fatal("missing read action")
	}
	if len(read.Rules) != 2 {
		t.Fatalf("read rules = %d, want 2", len(read.Rules))
	}
	pred, ok := read.Rules[1].Query["state"]
	if !ok {
		t.Fatal("missing state predicate")
	}
	if pred.Equals == nil || *pred.Equals != "released" {
		t.Errorf("equals = %v, want released", pred.Equals)
	}
	if pred.Occurs != "once" {
		t.Errorf("occurs = %q, want once", pred.Occurs)
	}

	hobby := m.Keys["hobbyvendor"]
	if !hobby.Actions["all"].Rules[0].AllowExtraParams {
		t.Error("allowExtraParams not parsed")
	}
}

// TestLoadErrorClasses pins one fixture per error class to its named error.
func TestLoadErrorClasses(t *testing.T) {
	cases := []struct {
		file string
		want error
	}{
		{"testdata/unknown-field.json", ErrUnknownField},
		{"testdata/missing-required.json", ErrMissingField},
		{"testdata/bad-glob.json", ErrBadPath},
		{"testdata/mustmatch.json", ErrMustMatchRejected},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			_, err := LoadFile(tc.file)
			if err == nil {
				t.Fatalf("%s: expected load error, got nil", tc.file)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: error = %v, want %v", tc.file, err, tc.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"missing", `{"keys":{}}`, ErrMissingField},
		{"unsupported", `{"version":2,"keys":{}}`, ErrUnsupportedVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNoKeys(t *testing.T) {
	_, err := Load(strings.NewReader(`{"version":1,"keys":{}}`))
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("error = %v, want ErrMissingField", err)
	}
}

func TestUpstream(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		want     error
	}{
		{"http", "http://issues.example.com", ErrBadUpstream},
		{"userinfo", "https://user:pw@issues.example.com", ErrBadUpstream},
		{"query", "https://issues.example.com?a=b", ErrBadUpstream},
		{"fragment", "https://issues.example.com#frag", ErrBadUpstream},
		{"nohost", "https:///path", ErrBadUpstream},
		{"missing", "", ErrMissingField},
		{"ok", "https://issues.example.com", nil},
		{"okpath", "https://issues.example.com/base", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUpstream(tc.upstream)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSecretRef(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		want   error
	}{
		{"empty", "", ErrMissingField},
		{"noscheme", "tracker-pat", ErrBadSecretRef},
		{"emptyref", "wincred:", ErrBadSecretRef},
		{"ok", "wincred:tracker-pat", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSecretRef(tc.secret)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestInject(t *testing.T) {
	cases := []struct {
		name   string
		inject []Injection
		want   error
	}{
		{"none", nil, ErrMissingField},
		{"two",
			[]Injection{
				{Kind: "header", Name: "Authorization", Template: "Bearer {secret}"},
				{Kind: "header", Name: "X-Api-Key", Template: "{secret}"},
			}, ErrBadInject},
		{"badkind", []Injection{{Kind: "query", Name: "Authorization", Template: "{secret}"}}, ErrBadInject},
		{"host", []Injection{{Kind: "header", Name: "Host", Template: "{secret}"}}, ErrBadHeaderName},
		{"hopbyhop", []Injection{{Kind: "header", Name: "Connection", Template: "{secret}"}}, ErrBadHeaderName},
		{"forwarding", []Injection{{Kind: "header", Name: "X-Forwarded-For", Template: "{secret}"}}, ErrBadHeaderName},
		{"custodyns", []Injection{{Kind: "header", Name: "X-Custody-Grant", Template: "{secret}"}}, ErrBadHeaderName},
		{"badtoken", []Injection{{Kind: "header", Name: "Bad Header", Template: "{secret}"}}, ErrBadHeaderName},
		{"crlf", []Injection{{Kind: "header", Name: "Authorization", Template: "Bearer {secret}\r\nX: y"}}, ErrBadTemplate},
		{"noplaceholder", []Injection{{Kind: "header", Name: "Authorization", Template: "Bearer token"}}, ErrBadTemplate},
		{"twoplaceholder", []Injection{{Kind: "header", Name: "Authorization", Template: "{secret}{secret}"}}, ErrBadTemplate},
		{"ok", []Injection{{Kind: "header", Name: "Authorization", Template: "Bearer {secret}"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInject(tc.inject)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMethods(t *testing.T) {
	cases := []struct {
		name    string
		methods []string
		want    error
	}{
		{"empty", nil, ErrMissingField},
		{"trace", []string{"TRACE"}, ErrBadMethod},
		{"connect", []string{"CONNECT"}, ErrBadMethod},
		{"lowercase", []string{"get"}, ErrBadMethod},
		{"star", []string{"*"}, nil},
		{"ok", []string{"GET", "POST"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMethods(tc.methods)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want error
	}{
		{"empty", "", ErrMissingField},
		{"relative", "rest/api", ErrBadPath},
		{"bracket", "/issue/PROJ-[", ErrBadPath},
		{"semicolon", "/issue/PROJ;evil", ErrBadPath},
		{"backslash", "/issue\\evil", ErrBadPath},
		{"space", "/issue /x", ErrBadPath},
		{"glob", "/rest/api/2/issue/PROJ-*", nil},
		{"doublestar", "/**", nil},
		{"nested", "/rest/api/2/issue/PROJ-*/comment", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePath(tc.path)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPredicate(t *testing.T) {
	released := "released"
	cases := []struct {
		name string
		pred Predicate
		want error
	}{
		{"mustmatch", Predicate{MustMatch: &released, Occurs: "once"}, ErrMustMatchRejected},
		{"noequals", Predicate{Occurs: "once"}, ErrMissingField},
		{"badoccurs", Predicate{Equals: &released, Occurs: "twice"}, ErrBadPredicate},
		{"emptyoccurs", Predicate{Equals: &released}, ErrBadPredicate},
		{"ok", Predicate{Equals: &released, Occurs: "once"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePredicate(tc.pred)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUnknownNestedField(t *testing.T) {
	body := `{"version":1,"keys":{"k":{"secret":"wincred:x","upstream":"https://h.example",
		"inject":[{"kind":"header","name":"Authorization","template":"{secret}","extra":1}],
		"actions":{"a":{"rules":[{"methods":["GET"],"path":"/x"}]}}}}}`
	_, err := Load(strings.NewReader(body))
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("error = %v, want ErrUnknownField", err)
	}
}
