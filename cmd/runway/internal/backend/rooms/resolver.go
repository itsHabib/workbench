package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
)

// authorityTTLMargin is the D4 slack added past the run's deadline + cancel
// grace so an in-flight vendor call at the deadline edge is not refused by a
// child that expired a hair too early. The child outlives its room by at most
// this margin — "authority lives in the room" stays literal.
const authorityTTLMargin = 90 * time.Second

// custodyRef is a parsed custody:<key>/<action>[,<action>...] secret reference.
// contracts/execution admits the grammar at Runway admission; the resolver
// re-parses it here — the one seam that reads both a grant and a placement —
// and fails closed on anything malformed rather than trust the upstream check.
type custodyRef struct {
	secretName string
	key        string
	actions    []string
}

// parseCustodyRef splits a custody: ref into its key and action set. It rejects
// a missing scheme, an empty key or action list, and empty actions in the list.
func parseCustodyRef(secretName, ref string) (custodyRef, error) {
	body, ok := strings.CutPrefix(ref, "custody:")
	if !ok {
		return custodyRef{}, fmt.Errorf("rooms: secret %q ref %q is not a custody: ref", secretName, ref)
	}
	key, actionList, ok := strings.Cut(body, "/")
	if !ok {
		return custodyRef{}, fmt.Errorf("rooms: custody ref %q missing /<action>: want custody:<key>/<action>[,<action>...]", ref)
	}
	if key == "" {
		return custodyRef{}, fmt.Errorf("rooms: custody ref %q has an empty key", ref)
	}
	if actionList == "" {
		return custodyRef{}, fmt.Errorf("rooms: custody ref %q has no actions", ref)
	}
	actions := make([]string, 0, strings.Count(actionList, ",")+1)
	for _, a := range strings.Split(actionList, ",") {
		if a == "" {
			return custodyRef{}, fmt.Errorf("rooms: custody ref %q has an empty action", ref)
		}
		actions = append(actions, a)
	}
	return custodyRef{secretName: secretName, key: key, actions: actions}, nil
}

// parentGrant is the live parent grant a custody port surfaces for one key: the
// operator-minted root the resolver derives a child through. It is read from
// custody's OUTPUT (the token the operator staged plus the persisted grant
// record) — never by importing custody's grant package (repo boundary law).
type parentGrant struct {
	id      string
	digest  string // sha256:… of the parent grant record bytes
	token   string // cst2_… parent token, passed to `custody derive -grant`
	actions []string
	expiry  time.Time
}

// covers reports whether the parent grant's actions are a superset of want.
func (p parentGrant) covers(want []string) bool {
	set := make(map[string]struct{}, len(p.actions))
	for _, a := range p.actions {
		set[a] = struct{}{}
	}
	for _, a := range want {
		if _, ok := set[a]; !ok {
			return false
		}
	}
	return true
}

// childGrant is a derived, source-bound child: the injectable token plus the
// record fields the receipt needs. Assembled from `custody derive` output.
type childGrant struct {
	id          string
	digest      string
	token       string
	actions     []string
	boundSource string
	mintedAt    time.Time
	expiry      time.Time
}

// deriveRequest is a source-bound child mint (D2b/D4): a subset of the parent's
// actions, capped to the run, bound to the room's transport source.
type deriveRequest struct {
	parentToken string
	actions     []string
	ttl         time.Duration
	boundSource string
}

// custodyPort is the resolver's seam onto custody. The concrete implementation
// shells the custody binary and reads grant record artifacts; tests inject a
// fake. ParentGrant returns a *backend.AuthorityUnresolved for a missing or
// unreadable parent so the resolver surfaces the remedy unchanged.
type custodyPort interface {
	ParentGrant(key string) (parentGrant, error)
	Derive(ctx context.Context, req deriveRequest) (childGrant, error)
}

// DeriveRecord is the resolver's per-ref output: the injectable child token plus
// every durable field receipt assembly needs. ChildToken is the secret and is
// deliberately absent from the receipt Grant.
type DeriveRecord struct {
	SecretName    string
	Key           string
	ParentID      string
	ParentDigest  string
	ParentActions []string
	ChildID       string
	ChildDigest   string
	ChildToken    string
	Actions       []string
	BoundSource   string
	MintedAt      time.Time
	Expiry        time.Time
}

// Resolver is the placement-side authority policy: parse a custody ref, find a
// live parent grant covering it, cap the child's TTL to the run, and derive a
// source-bound child. Mechanism (the custody CLI + record reads) lives behind
// the port; this layer is pure policy.
type Resolver struct {
	port custodyPort
}

// Resolve turns one custody ref into a derive record. A missing or non-covering
// parent refuses with backend.AuthorityUnresolved carrying the exact remedy; a
// derive failure fails closed (no room boots, FR6).
func (r Resolver) Resolve(ctx context.Context, ref custodyRef, deadline time.Time, grace time.Duration, boundSource string, now time.Time) (DeriveRecord, error) {
	parent, err := r.port.ParentGrant(ref.key)
	if err != nil {
		return DeriveRecord{}, r.unresolved(ref, err)
	}
	if !now.Before(parent.expiry) {
		return DeriveRecord{}, r.unresolved(ref, fmt.Errorf("parent grant expired %s", parent.expiry.UTC().Format(time.RFC3339)))
	}
	if !parent.covers(ref.actions) {
		return DeriveRecord{}, r.unresolved(ref, fmt.Errorf("parent grant covers %v, not %v", parent.actions, ref.actions))
	}
	ttl := capTTL(parent.expiry, deadline, grace, now)
	if ttl <= 0 {
		return DeriveRecord{}, r.unresolved(ref, fmt.Errorf("no positive run-capped ttl remains under the parent's expiry"))
	}
	child, err := r.port.Derive(ctx, deriveRequest{
		parentToken: parent.token,
		actions:     ref.actions,
		ttl:         ttl,
		boundSource: boundSource,
	})
	if err != nil {
		return DeriveRecord{}, fmt.Errorf("rooms: derive child for %s: %w", ref.secretName, err)
	}
	return DeriveRecord{
		SecretName:    ref.secretName,
		Key:           ref.key,
		ParentID:      parent.id,
		ParentDigest:  parent.digest,
		ParentActions: parent.actions,
		ChildID:       child.id,
		ChildDigest:   child.digest,
		ChildToken:    child.token,
		Actions:       child.actions,
		BoundSource:   child.boundSource,
		MintedAt:      child.mintedAt,
		Expiry:        child.expiry,
	}, nil
}

// unresolved wraps a cause as an AuthorityUnresolved refusal naming the mint
// remedy (FR2): the exact `custody grant` a human runs to make the placement go.
func (r Resolver) unresolved(ref custodyRef, cause error) error {
	return &backend.AuthorityUnresolved{
		Ref:    ref.secretName,
		Reason: cause.Error(),
		Remedy: fmt.Sprintf("custody grant -key %s -actions %s -ttl 8h", ref.key, strings.Join(ref.actions, ",")),
	}
}

// capTTL is D4: the child expires at min(parent expiry, now + deadline + grace +
// margin). The returned duration is that cap minus now, so `custody derive`
// mints a child that outlives its room by at most the margin.
func capTTL(parentExpiry, deadline time.Time, grace time.Duration, now time.Time) time.Duration {
	runCap := deadline.Add(grace).Add(authorityTTLMargin)
	capExpiry := runCap
	if parentExpiry.Before(runCap) {
		capExpiry = parentExpiry
	}
	return capExpiry.Sub(now)
}

// ResolveCustody implements backend.CustodyResolver: it resolves every custody:
// ref in the request into guest environment additions, redaction bytes, and the
// derive records receipt assembly consumes. Non-custody secrets are ignored
// here — the controller already expanded them.
func (b *Backend) ResolveCustody(ctx context.Context, req backend.CustodyRequest) (backend.CustodyResolution, error) {
	resolver := Resolver{port: b.port}
	env := map[string]string{}
	var redact [][]byte
	var records []DeriveRecord
	for _, s := range req.Secrets {
		ref, err := parseCustodyRef(s.Name, s.Ref)
		if err != nil {
			return backend.CustodyResolution{}, err
		}
		record, err := resolver.Resolve(ctx, ref, req.Deadline, req.Grace, b.config.tapSource(), req.Now)
		if err != nil {
			return backend.CustodyResolution{}, err
		}
		env["CUSTODY_GRANT_"+envKey(ref.key)] = record.ChildToken
		env["CUSTODY_BASE_"+envKey(ref.key)] = b.config.custodyBase(ref.key)
		redact = append(redact, []byte(record.ChildToken))
		records = append(records, record)
	}
	return backend.CustodyResolution{Env: env, Redact: redact, Records: records}, nil
}

// envKey upper-cases a custody key into the CUSTODY_GRANT_<KEY> suffix, mapping
// the hyphen custody keys allow into an underscore so the result is a legal env
// var name.
func envKey(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

// cliCustody is the concrete custody port: it shells the custody binary and
// reads persisted grant records. It imports no custody Go package — the seam is
// the CLI plus the JSON record artifacts (repo boundary law).
type cliCustody struct {
	bin      string
	stateDir string
	keyDir   string
	// parentToken resolves the operator-staged parent token for a key. Split out
	// so tests exercise the record-reading path without real environment.
	parentToken func(key string) string
}

// defaultCLICustody builds the CLI port from Runway's environment. The parent
// token for a key is staged by the operator in RUNWAY_CUSTODY_PARENT_<KEY>; it
// stays host-side and never enters the guest.
func defaultCLICustody() cliCustody {
	return cliCustody{
		bin:      envOrDefault("RUNWAY_CUSTODY_BIN", "custody"),
		stateDir: os.Getenv("CUSTODY_STATE"),
		keyDir:   os.Getenv("CUSTODY_KEY_DIR"),
		parentToken: func(key string) string {
			return os.Getenv("RUNWAY_CUSTODY_PARENT_" + envKey(key))
		},
	}
}

// ParentGrant reads the staged parent token for key and its persisted record.
func (c cliCustody) ParentGrant(key string) (parentGrant, error) {
	token := c.parentToken(key)
	if token == "" {
		return parentGrant{}, fmt.Errorf("no parent grant staged for key %q (set RUNWAY_CUSTODY_PARENT_%s)", key, envKey(key))
	}
	id, err := grantIDFromToken(token)
	if err != nil {
		return parentGrant{}, err
	}
	record, digest, err := c.readGrantRecord(id)
	if err != nil {
		return parentGrant{}, err
	}
	return parentGrant{
		id:      id,
		digest:  digest,
		token:   token,
		actions: record.Actions,
		expiry:  record.MintedAt.Add(record.TTL),
	}, nil
}

// Derive shells `custody derive`, then reads back the persisted child record for
// the receipt fields. The child token is the derive command's stdout.
func (c cliCustody) Derive(ctx context.Context, req deriveRequest) (childGrant, error) {
	args := []string{
		"derive",
		"-grant", req.parentToken,
		"-actions", strings.Join(req.actions, ","),
		"-ttl", req.ttl.String(),
		"-minted-by", "runway",
	}
	if req.boundSource != "" {
		args = append(args, "-bound-source", req.boundSource)
	}
	if c.stateDir != "" {
		args = append(args, "-state", c.stateDir)
	}
	if c.keyDir != "" {
		args = append(args, "-mint-key-dir", c.keyDir)
	}
	out, err := exec.CommandContext(ctx, c.bin, args...).Output()
	if err != nil {
		return childGrant{}, fmt.Errorf("custody derive: %w", err)
	}
	token := strings.TrimSpace(string(out))
	id, err := grantIDFromToken(token)
	if err != nil {
		return childGrant{}, err
	}
	record, digest, err := c.readGrantRecord(id)
	if err != nil {
		return childGrant{}, err
	}
	return childGrant{
		id:          id,
		digest:      digest,
		token:       token,
		actions:     record.Actions,
		boundSource: record.BoundSource,
		mintedAt:    record.MintedAt,
		expiry:      record.MintedAt.Add(record.TTL),
	}, nil
}

// grantRecord mirrors the persisted custody grant fields the resolver reads. It
// is a local view of custody's OUTPUT artifact, not an import of its package —
// the same posture the adapter takes toward rooms' lifecycle records.
type grantRecord struct {
	ID          string        `json:"id"`
	Actions     []string      `json:"actions"`
	Parent      string        `json:"parent,omitempty"`
	BoundSource string        `json:"bound_source,omitempty"`
	MintedAt    time.Time     `json:"minted_at"`
	TTL         time.Duration `json:"ttl"`
}

// readGrantRecord reads <state>/grants/<id>.json and returns the parsed record
// plus the sha256:… digest of the exact bytes on disk.
func (c cliCustody) readGrantRecord(id string) (grantRecord, string, error) {
	data, err := os.ReadFile(filepath.Join(c.stateDir, "grants", id+".json"))
	if err != nil {
		return grantRecord{}, "", fmt.Errorf("read grant record %s: %w", id, err)
	}
	var record grantRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return grantRecord{}, "", fmt.Errorf("parse grant record %s: %w", id, err)
	}
	sum := sha256.Sum256(data)
	return record, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// grantIDFromToken extracts the record id from a cst2_<id>.<sig> token without
// trusting the signature — the record read and `custody derive` are the trust
// boundary, this only names the file to read.
func grantIDFromToken(token string) (string, error) {
	body, ok := strings.CutPrefix(token, "cst2_")
	if !ok {
		return "", fmt.Errorf("grant token %q is not a cst2_ token", short(token))
	}
	id, _, ok := strings.Cut(body, ".")
	if !ok || id == "" {
		return "", fmt.Errorf("grant token is malformed")
	}
	return id, nil
}

// short truncates a token for error messages so a signature never lands in a log.
func short(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "…"
}

// envOrDefault returns env var key's value or fallback when unset/empty.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
