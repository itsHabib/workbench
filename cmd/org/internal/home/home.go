// Package home is the org runtime's mechanism layer: chains on disk, one
// writer at a time, bodies as content-addressed blobs.
//
// The layer split is deliberate. Every law about WHAT may extend a chain lives
// in contracts/org (the fold); this package owns only WHERE records live and
// HOW an append is serialized. It decides nothing — a record this package
// writes was admitted by org.Advance under the lock, and a record refused
// there is refused here with the kernel's own reason.
//
// # Layout
//
//	<state>/<tenant>/<role>/chain.jsonl   one record per line, append-only
//	<state>/blobs/sha256-<hex>            erasable bodies, content-addressed
//
// Role ids carry colons (lead:agentic-development); the on-disk directory
// replaces them with "--" so paths stay friendly to every tool that splits on
// colons.
package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/contracts/org"
)

// Home is one state root: every chain and blob under one directory.
type Home struct {
	root string
}

// Open returns a Home rooted at dir, creating it if absent.
func Open(dir string) (*Home, error) {
	if dir == "" {
		return nil, fmt.Errorf("state dir is empty; set -state or ORG_STATE")
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	return &Home{root: dir}, nil
}

// Root reports the state directory this home is rooted at.
func (h *Home) Root() string { return h.root }

// dirFor maps a tenant and role to the chain directory.
func (h *Home) dirFor(tenant, role string) string {
	return filepath.Join(h.root, tenant, strings.ReplaceAll(role, ":", "--"))
}

// chainPath is the JSONL file holding a role's records.
func (h *Home) chainPath(tenant, role string) string {
	return filepath.Join(h.dirFor(tenant, role), "chain.jsonl")
}

// Roles enumerates every chain under the home as (tenant, role) pairs, derived
// from the directory layout rather than an index that could drift from it.
func (h *Home) Roles() ([][2]string, error) {
	tenants, err := os.ReadDir(h.root)
	if err != nil {
		return nil, fmt.Errorf("read state root: %w", err)
	}
	var out [][2]string
	for _, t := range tenants {
		if !t.IsDir() || t.Name() == "blobs" {
			continue
		}
		roles, err := os.ReadDir(filepath.Join(h.root, t.Name()))
		if err != nil {
			return nil, fmt.Errorf("read tenant %s: %w", t.Name(), err)
		}
		for _, r := range roles {
			if !r.IsDir() {
				continue
			}
			out = append(out, [2]string{t.Name(), strings.ReplaceAll(r.Name(), "--", ":")})
		}
	}
	return out, nil
}

// Load reads and folds a role's chain. A missing chain is not an error: it
// folds to the zero state, which is exactly what the kernel says an empty
// chain means.
func (h *Home) Load(tenant, role string) ([]org.Record, org.RoleState, error) {
	records, err := readChain(h.chainPath(tenant, role))
	if err != nil {
		return nil, org.RoleState{}, err
	}
	state, err := org.Reduce(records)
	if err != nil {
		return nil, org.RoleState{}, fmt.Errorf("chain for %s/%s does not fold: %w", tenant, role, err)
	}
	return records, state, nil
}

// Draft is what a verb asks the home to append: the transition and its
// structural parameter. Everything positional — seq, prev, fence, incarnation,
// class, timestamp — is the home's to fill, exactly as a real writer would.
type Draft struct {
	Kind    string
	Subject org.Subject
	Terms   *org.Terms
	Refs    []org.Ref
	// Body is optional prose. Non-empty stores a blob and stamps its digest.
	Body      []byte
	BodyClass string
	// NextDue, when set, declares the writer's own next-append deadline.
	NextDue time.Time
	// Incarnation optionally presents the writer's identity explicitly. Empty
	// lets the home write as the current holder, which is the single-operator
	// posture this POC runs in; a multi-writer home would require it.
	Incarnation string
}

// Append admits and appends one record under the chain's lock, returning the
// record as written and the state after it.
func (h *Home) Append(tenant, role string, d Draft) (org.Record, org.RoleState, error) {
	dir := h.dirFor(tenant, role)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return org.Record{}, org.RoleState{}, fmt.Errorf("create chain dir: %w", err)
	}
	unlock, err := lock(filepath.Join(dir, "lock"))
	if err != nil {
		return org.Record{}, org.RoleState{}, err
	}
	defer unlock()

	_, state, err := h.Load(tenant, role)
	if err != nil {
		return org.Record{}, org.RoleState{}, err
	}
	r, err := h.draft(tenant, role, state, d)
	if err != nil {
		return org.Record{}, org.RoleState{}, err
	}
	next, err := org.Advance(state, r)
	if err != nil {
		return org.Record{}, org.RoleState{}, err
	}
	if err := appendLine(h.chainPath(tenant, role), r); err != nil {
		return org.Record{}, org.RoleState{}, err
	}
	return r, next, nil
}

// draft fills the positional spine the way the kernel's own writer fixture
// does: seq and prev from the folded tip, fence carried forward, incarnation
// as the holder unless the kind mints one or the caller presented an identity.
func (h *Home) draft(tenant, role string, state org.RoleState, d Draft) (org.Record, error) {
	class, known := org.DeclaredClass(d.Kind)
	if !known {
		return org.Record{}, fmt.Errorf("unknown kind %q", d.Kind)
	}
	r := org.Record{
		V: org.Version, Scheme: org.Scheme,
		Seq: state.Seq + 1, Prev: state.Tip,
		Tenant: tenant, Role: role,
		Kind: d.Kind, KindClass: class,
		Fence:   state.Fence,
		At:      time.Now().UTC().Format(time.RFC3339),
		Subject: d.Subject,
		Terms:   d.Terms,
		Refs:    d.Refs,
	}
	// A takeover or revoke advances the fence to its own position, which is
	// what kills every credential minted before it.
	if d.Kind == org.KindTakeover || d.Kind == org.KindRevoke {
		r.Fence = r.Seq
	}
	if !mints(d.Kind) {
		r.Incarnation = state.Holder
		if d.Incarnation != "" {
			r.Incarnation = d.Incarnation
		}
	}
	if !d.NextDue.IsZero() {
		r.NextDue = d.NextDue.UTC().Format(time.RFC3339)
	}
	if len(d.Body) > 0 {
		digest, err := h.PutBlob(d.Body)
		if err != nil {
			return org.Record{}, err
		}
		r.BodyDigest = digest
		r.BodyClass = d.BodyClass
		if r.BodyClass == "" {
			r.BodyClass = "narrative"
		}
	}
	return r, nil
}

// mints reports the kinds whose own digest becomes an identity, and which
// therefore carry no incarnation of their own. Revoke is not among them: it is
// written by (or as) the displaced holder, and only advances the fence.
func mints(kind string) bool {
	return kind == org.KindCharter || kind == org.KindAttach || kind == org.KindTakeover
}

// PutBlob stores a body content-addressed and returns its digest.
func (h *Home) PutBlob(body []byte) (string, error) {
	digest := org.DigestBytes(body)
	path := filepath.Join(h.root, "blobs", strings.ReplaceAll(digest, ":", "-"))
	if _, err := os.Stat(path); err == nil {
		return digest, nil
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("store blob: %w", err)
	}
	return digest, nil
}

// Blob reads a body back by digest. A missing blob is reported as erased, not
// as an error in the chain: erasability is the design, and callers render the
// absence.
func (h *Home) Blob(digest string) ([]byte, bool, error) {
	path := filepath.Join(h.root, "blobs", strings.ReplaceAll(digest, ":", "-"))
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read blob: %w", err)
	}
	return body, true, nil
}

// readChain decodes a JSONL chain file. Missing file folds as empty.
func readChain(path string) ([]org.Record, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chain: %w", err)
	}
	var records []org.Record
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var r org.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("chain line %d: %w", i+1, err)
		}
		records = append(records, r)
	}
	return records, nil
}

// appendLine writes one record as a JSON line and syncs it, so a record the
// caller was told about is on disk.
func appendLine(path string, r org.Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open chain: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync chain: %w", err)
	}
	return f.Close()
}

// lock takes an exclusive flock on path, returning the release. The lock file
// is separate from the chain so a reader never contends with the append fsync.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock chain: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
