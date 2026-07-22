package manifest

import (
	"encoding/json"
	"errors"
	"os"
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

func TestLoadFileMissing(t *testing.T) {
	path := "testdata/does-not-exist.json"
	_, err := LoadFile(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name path %q", err, path)
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
		{"zero", `{"version":0,"keys":{}}`, ErrUnsupportedVersion},
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
		{"path", "https://issues.example.com/base", ErrBadUpstream},
		{"deeppath", "https://issues.example.com/a/b/c", ErrBadUpstream},
		{"missing", "", ErrMissingField},
		{"ok", "https://issues.example.com", nil},
		{"okslash", "https://issues.example.com/", nil},
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

func TestUpstreamErrorDoesNotEchoUserinfo(t *testing.T) {
	const upstream = "https://user:TOP-SECRET@example.com"
	err := validateUpstream(upstream)
	if err == nil {
		t.Fatal("expected userinfo rejection")
	}
	if strings.Contains(err.Error(), "TOP-SECRET") {
		t.Fatalf("error leaked upstream password: %v", err)
	}
}

func TestUpstreamErrorsNeverEchoRawURL(t *testing.T) {
	const secret = "TOP-SECRET"
	for _, upstream := range []string{
		"http://user:" + secret + "@example.com",
		"https:///?token=" + secret,
		"https://example.com/%" + secret,
	} {
		err := validateUpstream(upstream)
		if err == nil {
			t.Fatalf("validateUpstream(%q) succeeded", upstream)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked raw upstream: %v", err)
		}
	}
}

func TestMustMatchNullIsRejected(t *testing.T) {
	data, err := os.ReadFile("testdata/mustmatch.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	body := strings.Replace(string(data), `"project = PROJ"`, `null`, 1)
	_, err = Load(strings.NewReader(body))
	if !errors.Is(err, ErrMustMatchRejected) {
		t.Fatalf("error = %v, want %v", err, ErrMustMatchRejected)
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

func TestSecretRefErrorDoesNotEchoValue(t *testing.T) {
	const secret = "TOP-SECRET-TOKEN"
	err := validateSecretRef(secret)
	if err == nil {
		t.Fatal("expected bad secret ref")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret field: %v", err)
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
		{"via", []Injection{{Kind: "header", Name: "Via", Template: "{secret}"}}, ErrBadHeaderName},
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

// manifestWithTemplate returns a minimal valid manifest whose one inject
// template is tmpl. tmpl is marshaled with encoding/json, so a control byte in
// tmpl becomes a proper \uXXXX escape and the manifest stays valid JSON — the
// decoder then materializes that byte back into the string validation sees.
func manifestWithTemplate(t *testing.T, tmpl string) string {
	t.Helper()
	quoted, err := json.Marshal(tmpl)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	return `{"version":1,"keys":{"k":{"secret":"wincred:x","upstream":"https://h.example",` +
		`"inject":[{"kind":"header","name":"Authorization","template":` + string(quoted) + `}],` +
		`"actions":{"a":{"rules":[{"methods":["GET"],"path":"/x"}]}}}}}`
}

// TestTemplateControlBytesRejectedAtLoad pins the L1 tightening: a template
// carrying any control byte (NUL, ESC, DEL, ...) fails closed at Load, while a
// clean template still loads. Bytes are built with rune() so the source carries
// no escape literals of its own.
func TestTemplateControlBytesRejectedAtLoad(t *testing.T) {
	bad := map[string]string{
		"nul": "Bearer {secret}" + string(rune(0x00)),
		"esc": "Bearer {secret}" + string(rune(0x1b)),
		"del": "Bearer {secret}" + string(rune(0x7f)),
		"tab": "Bearer {secret}" + string(rune(0x09)),
		"cr":  "Bearer {secret}" + string(rune(0x0d)),
	}
	for name, tmpl := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := Load(strings.NewReader(manifestWithTemplate(t, tmpl)))
			if !errors.Is(err, ErrBadTemplate) {
				t.Fatalf("error = %v, want ErrBadTemplate", err)
			}
		})
	}
	t.Run("clean", func(t *testing.T) {
		if _, err := Load(strings.NewReader(manifestWithTemplate(t, "Bearer {secret}"))); err != nil {
			t.Fatalf("clean template failed to load: %v", err)
		}
	})
}

// TestUpstreamPathRejectedAtLoad pins the L2 design-lock: an upstream carrying a
// base path fails closed at Load (the proxy would silently drop it), while
// scheme+host — with or without a bare root slash — loads.
func TestUpstreamPathRejectedAtLoad(t *testing.T) {
	body := func(upstream string) string {
		return `{"version":1,"keys":{"k":{"secret":"wincred:x","upstream":"` + upstream + `",` +
			`"inject":[{"kind":"header","name":"Authorization","template":"{secret}"}],` +
			`"actions":{"a":{"rules":[{"methods":["GET"],"path":"/x"}]}}}}}`
	}
	for _, upstream := range []string{"https://api.example.com/base", "https://api.example.com/a/b/c"} {
		_, err := Load(strings.NewReader(body(upstream)))
		if !errors.Is(err, ErrBadUpstream) {
			t.Fatalf("upstream %q: error = %v, want ErrBadUpstream", upstream, err)
		}
	}
	for _, upstream := range []string{"https://api.example.com", "https://api.example.com/"} {
		if _, err := Load(strings.NewReader(body(upstream))); err != nil {
			t.Fatalf("upstream %q failed to load: %v", upstream, err)
		}
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
		{"dot-segment", "/api/./issues", ErrBadPath},
		{"dot-dot-segment", "/api/../issues", ErrBadPath},
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
		{"mustmatch", Predicate{MustMatch: json.RawMessage(`"released"`), Occurs: "once"}, ErrMustMatchRejected},
		{"noequals", Predicate{Occurs: "once"}, ErrMissingField},
		{"badoccurs", Predicate{Equals: &released, Occurs: "twice"}, ErrBadPredicate},
		{"emptyoccurs", Predicate{Equals: &released}, ErrMissingField},
		{"wildcard-star", predicate("proj*"), ErrBadPredicate},
		{"wildcard-question", predicate("proj?"), ErrBadPredicate},
		{"wildcard-range", predicate("proj[0-9]"), ErrBadPredicate},
		{"alternation", predicate("proj|other"), ErrBadPredicate},
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

func predicate(equals string) Predicate {
	return Predicate{Equals: &equals, Occurs: "once"}
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

func TestTrailingDataRejected(t *testing.T) {
	valid := `{"version":1,"keys":{"k":{"secret":"wincred:x","upstream":"https://h.example","inject":[{"kind":"header","name":"Authorization","template":"{secret}"}],"actions":{"a":{"rules":[{"methods":["GET"],"path":"/x"}]}}}}}`
	for _, suffix := range []string{`{}`, `]`, `true`} {
		_, err := Load(strings.NewReader(valid + suffix))
		if !errors.Is(err, ErrTrailingData) {
			t.Fatalf("suffix %q: error = %v, want ErrTrailingData", suffix, err)
		}
	}
}

func TestValidationErrorsAreDeterministic(t *testing.T) {
	bad := Key{}
	m := &Manifest{Version: 1, Keys: map[string]Key{"z": bad, "a": bad}}
	for range 20 {
		err := m.validate()
		if err == nil || !strings.Contains(err.Error(), `key "a"`) {
			t.Fatalf("error = %v, want first sorted key", err)
		}
	}
}
