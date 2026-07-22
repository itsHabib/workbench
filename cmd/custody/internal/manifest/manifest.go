// Package manifest loads and validates custody's key manifest
// (`<state>/manifest.json`, spec §5). It is a policy layer: it parses the
// operator-owned manifest and fails closed at load time — unknown fields,
// missing required fields, a malformed path glob, or a denied predicate are
// startup errors, never per-request surprises. It validates STRUCTURE only;
// runtime request matching is the proxy engine's job and lives elsewhere.
//
// Callers obtain a *Manifest only through Load, which guarantees every
// invariant below; an empty manifest is invalid.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
)

// Manifest is a validated custody manifest. It is produced only by Load, so a
// non-nil *Manifest has already passed every check in this package.
type Manifest struct {
	Version        int            `json:"version"`
	Keys           map[string]Key `json:"keys"`
	versionPresent bool
}

// Key is one credential behind the proxy: the secret reference, the upstream it
// forwards to, how the secret is injected, and the named actions that bound
// what a grant may reach.
type Key struct {
	Secret   string            `json:"secret"`
	Upstream string            `json:"upstream"`
	Inject   []Injection       `json:"inject"`
	Actions  map[string]Action `json:"actions"`
	Note     string            `json:"note"`
}

// Injection is one tagged step of secret placement. v0 accepts exactly one
// entry whose kind is "header"; the list shape is locked additively so a later
// injection kind is a new entry, not a schema change (spec §5).
type Injection struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Template string `json:"template"`
}

// Action is a named set of rules. The action set is the ceiling a grant scopes
// to (spec §4 D3).
type Action struct {
	Rules []Rule `json:"rules"`
}

// Rule is one request shape an action permits: HTTP methods, an anchored path
// glob, and optional anchored-scalar query predicates. AllowExtraParams opts a
// rule out of the deny-by-default treatment of unlisted query params (spec §7 D).
type Rule struct {
	Methods          []string             `json:"methods"`
	Path             string               `json:"path"`
	Query            map[string]Predicate `json:"query"`
	AllowExtraParams bool                 `json:"allowExtraParams"`
}

// Predicate is an anchored-scalar query test: an exact value that must occur a
// fixed number of times. It deliberately offers no substring/regex shape —
// embedded query languages are deny-by-default in v0 (spec §7 D). A mustMatch
// field is captured only so its presence can be rejected with a named error.
type Predicate struct {
	Equals    *string         `json:"equals"`
	Occurs    string          `json:"occurs"`
	MustMatch json.RawMessage `json:"mustMatch"`
}

// Named error classes so callers (and tests) branch on the code, never prose.
var (
	ErrUnknownField       = errors.New("manifest_unknown_field")
	ErrTrailingData       = errors.New("manifest_trailing_data")
	ErrMissingField       = errors.New("manifest_missing_field")
	ErrUnsupportedVersion = errors.New("manifest_unsupported_version")
	ErrBadSecretRef       = errors.New("manifest_bad_secret_ref")
	ErrBadUpstream        = errors.New("manifest_bad_upstream")
	ErrBadInject          = errors.New("manifest_bad_inject")
	ErrBadHeaderName      = errors.New("manifest_bad_header_name")
	ErrBadTemplate        = errors.New("manifest_bad_template")
	ErrBadMethod          = errors.New("manifest_bad_method")
	ErrBadPath            = errors.New("manifest_bad_path")
	ErrBadPredicate       = errors.New("manifest_bad_predicate")
	ErrMustMatchRejected  = errors.New("manifest_mustmatch_rejected")
)

const secretScheme = "wincred:"

// LoadFile reads and validates the manifest at path (spec §5).
func LoadFile(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: open %s: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}

// Load parses and fully validates a manifest from r, failing closed. Unknown
// fields, missing required fields, a bad path glob, or a denied predicate are
// all load-time errors. On success the returned *Manifest satisfies every §5
// invariant.
func Load(r io.Reader) (*Manifest, error) {
	m, err := decode(r)
	if err != nil {
		return nil, err
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manifest) validate() error {
	if !m.versionPresent && m.Version == 0 {
		return fmt.Errorf("%w: version", ErrMissingField)
	}
	if m.Version != 1 {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, m.Version)
	}
	if len(m.Keys) == 0 {
		return fmt.Errorf("%w: keys", ErrMissingField)
	}
	for _, name := range sortedKeys(m.Keys) {
		k := m.Keys[name]
		if err := validateKey(k); err != nil {
			return fmt.Errorf("key %q: %w", name, err)
		}
	}
	return nil
}

func validateKey(k Key) error {
	if err := validateSecretRef(k.Secret); err != nil {
		return err
	}
	if err := validateUpstream(k.Upstream); err != nil {
		return err
	}
	if err := validateInject(k.Inject); err != nil {
		return err
	}
	return validateActions(k.Actions)
}

func validateSecretRef(secret string) error {
	if secret == "" {
		return fmt.Errorf("%w: secret", ErrMissingField)
	}
	if !strings.HasPrefix(secret, secretScheme) {
		return fmt.Errorf("%w: secret field must use %s<ref> form", ErrBadSecretRef, secretScheme)
	}
	if strings.TrimPrefix(secret, secretScheme) == "" {
		return fmt.Errorf("%w: empty ref after %s", ErrBadSecretRef, secretScheme)
	}
	return nil
}

func validateUpstream(upstream string) error {
	if upstream == "" {
		return fmt.Errorf("%w: upstream", ErrMissingField)
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return fmt.Errorf("%w: upstream URL is malformed", ErrBadUpstream)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: upstream must use https", ErrBadUpstream)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: upstream has no host", ErrBadUpstream)
	}
	if u.User != nil {
		return fmt.Errorf("%w: upstream carries userinfo; remove credentials from the URL", ErrBadUpstream)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("%w: upstream carries a query; remove query parameters from the URL", ErrBadUpstream)
	}
	if u.Fragment != "" {
		return fmt.Errorf("%w: upstream carries a fragment; remove it from the URL", ErrBadUpstream)
	}
	return nil
}

func validateActions(actions map[string]Action) error {
	if len(actions) == 0 {
		return fmt.Errorf("%w: actions", ErrMissingField)
	}
	for _, name := range sortedKeys(actions) {
		a := actions[name]
		if err := validateAction(a); err != nil {
			return fmt.Errorf("action %q: %w", name, err)
		}
	}
	return nil
}

func validateAction(a Action) error {
	if len(a.Rules) == 0 {
		return fmt.Errorf("%w: rules", ErrMissingField)
	}
	for i, r := range a.Rules {
		if err := validateRule(r); err != nil {
			return fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	return nil
}

func validateRule(r Rule) error {
	if err := validateMethods(r.Methods); err != nil {
		return err
	}
	if err := validatePath(r.Path); err != nil {
		return err
	}
	for _, param := range sortedKeys(r.Query) {
		p := r.Query[param]
		if err := validatePredicate(p); err != nil {
			return fmt.Errorf("query %q: %w", param, err)
		}
	}
	return nil
}

func validatePredicate(p Predicate) error {
	if len(p.MustMatch) != 0 {
		return fmt.Errorf("%w: substring/regex predicates are deny-by-default in v0", ErrMustMatchRejected)
	}
	if p.Equals == nil {
		return fmt.Errorf("%w: equals", ErrMissingField)
	}
	if strings.ContainsAny(*p.Equals, "*?[]|") {
		return fmt.Errorf("%w: equals carries wildcard or alternation metacharacters", ErrBadPredicate)
	}
	if p.Occurs == "" {
		return fmt.Errorf("%w: occurs", ErrMissingField)
	}
	if p.Occurs != "once" {
		return fmt.Errorf("%w: occurs must be \"once\", got %q", ErrBadPredicate, p.Occurs)
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
