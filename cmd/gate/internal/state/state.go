// Package state is the substrate every other package writes through: typed,
// append-only, content-hashed artifacts with explicit provenance refs. It is
// the bottom of the dependency graph — it imports nothing else in this module
// and knows nothing about verdicts, grants, or PRs beyond their serialized
// bodies.
package state

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Kind names the artifact families packages exchange.
const (
	KindEvidence   = "evidence"
	KindVerdict    = "verdict"
	KindGrant      = "grant"
	KindAction     = "action"
	KindEscalation = "escalation"
	KindJudgment   = "judgment"
)

var kindPrefix = map[string]string{
	KindEvidence:   "evd",
	KindVerdict:    "vrd",
	KindGrant:      "grt",
	KindAction:     "act",
	KindEscalation: "esc",
	KindJudgment:   "jdg",
}

// Artifact is the one unit of communication between packages. Parents carry
// provenance: a verdict names the evidence it judged, an action names the
// verdicts that authorized it. Hash chains each artifact to the previous
// log entry so tampering is detectable by replay.
type Artifact struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Run     string          `json:"run"`
	Time    time.Time       `json:"time"`
	Parents []string        `json:"parents,omitempty"`
	Body    json.RawMessage `json:"body"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

// Store is an append-only artifact log on the filesystem. An optional keyed
// anchor (key + record kept outside dir) upgrades the unkeyed chain from
// tamper-evident-against-accident to tamper-evident-against-rewrite: see
// anchor.go. A store with no anchor keeps only the chain.
type Store struct {
	dir    string
	now    func() time.Time
	anchor *anchor
}

// Open creates or attaches to a store rooted at dir, without a keyed anchor.
// The chain still catches naive edits, broken links, and reordering; it does
// not catch wholesale rewrite or truncation. Use OpenAnchored to close those.
func Open(dir string, now func() time.Time) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("state: create dir: %w", err)
	}
	return &Store{dir: dir, now: now}, nil
}

// OpenAnchored creates or attaches to a store whose chain head and count are
// bound under a keyed anchor. anchorPath and keyPath must live OUTSIDE dir: the
// whole point is that an actor who can write the state dir cannot forge the
// anchor. The key is loaded (or minted on first append), never silently created
// on the audit path.
func OpenAnchored(dir string, now func() time.Time, anchorPath, keyPath string) (*Store, error) {
	s, err := Open(dir, now)
	if err != nil {
		return nil, err
	}
	s.anchor = &anchor{path: anchorPath, keyPath: keyPath}
	return s, nil
}

func (s *Store) logPath() string { return filepath.Join(s.dir, "log.jsonl") }

// NewRunID mints a run identifier grouping one gate invocation's artifacts.
func NewRunID() string { return "run_" + randHex(8) }

// Append records a new artifact and returns it with id, time, prev, and hash
// filled. The read-chain-head + write pair is a critical section: without the
// exclusive lock two writers race on prev and fork the chain (the concurrency
// test pins exactly that failure).
func (s *Store) Append(kind, run string, parents []string, body any) (Artifact, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return Artifact{}, fmt.Errorf("state: marshal body: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return Artifact{}, err
	}
	defer unlock()
	prev, err := s.lastHash()
	if err != nil {
		return Artifact{}, err
	}
	a := Artifact{
		ID:      kindPrefix[kind] + "_" + randHex(8),
		Kind:    kind,
		Run:     run,
		Time:    s.now().UTC(),
		Parents: parents,
		Body:    raw,
		Prev:    prev,
	}
	a.Hash = hashArtifact(a)
	line, err := json.Marshal(a)
	if err != nil {
		return Artifact{}, fmt.Errorf("state: marshal artifact: %w", err)
	}
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Artifact{}, fmt.Errorf("state: open log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Artifact{}, fmt.Errorf("state: append: %w", err)
	}
	// An audit log must not lose its newest entry to a crash between the
	// kernel buffer and the disk — flush before the lock releases.
	if err := f.Sync(); err != nil {
		return Artifact{}, fmt.Errorf("state: sync log: %w", err)
	}
	// Bind the new head and count under the anchor, still inside the lock:
	// the anchor must move in lockstep with the chain head, or a reader could
	// observe a head the anchor hasn't yet pinned.
	if err := s.rebind(prev, a.Hash); err != nil {
		// The line is already fsync'd to the log, so return the populated
		// artifact alongside the error: the entry exists (an unbound-anchor or
		// refused-truncation state that Audit will catch), and a caller can name
		// the id that landed rather than seeing a zero Artifact that implies
		// nothing was written.
		return a, fmt.Errorf("state: anchor: %w", err)
	}
	return a, nil
}

// rebind advances the anchor to newHead. It is a no-op for an unanchored store.
//
// prevHead is the chain head this append was written onto. When it matches the
// anchor's recorded head the anchor was in sync, so the new count is exactly
// one more — the O(1) steady-state path, no full scan. When it does not match
// (no anchor yet, or a crash landed in the anchor-update window leaving the log
// ahead of the anchor), the count is reconciled from the real log length so a
// single crash cannot desync the anchor permanently.
func (s *Store) rebind(prevHead, newHead string) error {
	if s.anchor == nil {
		return nil
	}
	rec, ok, err := s.anchor.read()
	if err != nil {
		return err
	}
	if ok && rec.Head == prevHead {
		return s.anchor.bind(newHead, rec.Count+1)
	}
	count, err := s.count()
	if err != nil {
		return err
	}
	// The mismatch path reconciles a legitimately-ahead log — first-time
	// migration with no record, or a crash that left the log past the anchor.
	// It must never LOWER the pinned count: an anchor already counting >= the
	// surviving lines means entries were removed, and resealing at the shorter
	// count would launder truncation into "intact". Refuse, and let Audit keep
	// reporting the loss.
	if ok && rec.Count >= count {
		return fmt.Errorf("%w: anchor pinned %d entries, log has %d", ErrRebindTruncation, rec.Count, count)
	}
	// And it must never reseal a rewritten prefix: a state-dir writer who
	// rewrites the log in place (rehashing the chain into self-consistency)
	// and then lets one legitimate append land would otherwise have the
	// forgery HMAC-bound here as "crash recovery". Crash recovery is only
	// recovery when the entry at the pinned count still carries the pinned
	// head — prove it before resealing.
	if ok {
		head, err := s.headAt(rec.Count)
		if err != nil {
			return err
		}
		if head != rec.Head {
			return fmt.Errorf("%w: anchor pinned head %s at entry %d, log has %s", ErrRebindRewrite, short(rec.Head), rec.Count, short(head))
		}
	}
	// Bound recovery to the crash window's shape: one entry whose anchor
	// update crashed, plus the entry this append just wrote. The chain is
	// unkeyed, so entries past the pinned head are unauthenticated — sealing
	// an arbitrarily long unanchored suffix would let a state-dir writer
	// batch-forge history into the anchor. (One forged entry timed inside the
	// crash window is still indistinguishable from a real interrupted append;
	// closing that needs per-entry authentication — tracked in FOLLOWUPS.)
	if ok && count > rec.Count+2 {
		return fmt.Errorf("%w: anchor pinned %d entries, log has %d — beyond the one-append crash window", ErrRebindUnprovenSuffix, rec.Count, count)
	}
	return s.anchor.bind(newHead, count)
}

// headAt returns the chain hash of the nth log entry (1-based). Used only on
// rebind's reconcile path, to prove the anchored prefix survived intact before
// the anchor advances over it.
func (s *Store) headAt(n int) (string, error) {
	f, err := os.Open(s.logPath())
	if err != nil {
		return "", fmt.Errorf("state: open log: %w", err)
	}
	defer f.Close()
	i := 0
	var head string
	err = eachLine(f, func(line []byte) error {
		i++
		if i != n {
			return nil
		}
		var a Artifact
		if err := json.Unmarshal(line, &a); err != nil {
			return fmt.Errorf("state: parse log entry %d: %w", n, err)
		}
		head = a.Hash
		return nil
	})
	if err != nil {
		return "", err
	}
	if head == "" {
		return "", fmt.Errorf("state: log has fewer than %d entries", n)
	}
	return head, nil
}

// count returns the number of log lines. Used off the steady-state path —
// anchor migration (no record yet) or post-crash reconcile — never per
// steady-state append, which increments the anchor's stored count in O(1).
func (s *Store) count() (int, error) {
	f, err := os.Open(s.logPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("state: open log: %w", err)
	}
	defer f.Close()
	n := 0
	err = eachLine(f, func([]byte) error { n++; return nil })
	return n, err
}

// Get returns one artifact by id.
func (s *Store) Get(id string) (Artifact, error) {
	all, err := s.List(func(a Artifact) bool { return a.ID == id })
	if err != nil {
		return Artifact{}, err
	}
	if len(all) == 0 {
		return Artifact{}, fmt.Errorf("state: artifact %s not found", id)
	}
	return all[0], nil
}

// Run returns all artifacts belonging to a run, in log order.
func (s *Store) Run(run string) ([]Artifact, error) {
	return s.List(func(a Artifact) bool { return a.Run == run })
}

// List scans the log returning artifacts matching keep (nil = all), in order.
// It takes the store lock so a scan never observes a half-written entry from
// a concurrent Append — audit and explain must not fail spuriously under load.
func (s *Store) List(keep func(Artifact) bool) ([]Artifact, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.scan(keep)
}

// scan reads the log without taking the lock — the caller must already hold it.
// Splitting the scan from the lock lets Audit read the chain and verify the
// anchor under one lock, so the two observe a single consistent snapshot.
func (s *Store) scan(keep func(Artifact) bool) ([]Artifact, error) {
	f, err := os.Open(s.logPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: open log: %w", err)
	}
	defer f.Close()

	var out []Artifact
	err = eachLine(f, func(line []byte) error {
		var a Artifact
		if err := json.Unmarshal(line, &a); err != nil {
			return fmt.Errorf("state: corrupt log line: %w", err)
		}
		if keep != nil && !keep(a) {
			return nil
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// eachLine calls fn for each newline-terminated record in the log. Unlike
// bufio.Scanner it grows to the record size with no fixed ceiling, so a single
// large artifact — e.g. an oversized-PR diff evidence line — is read back
// intact rather than bricking every later scan with "token too long". Blank
// lines are skipped; the append path never writes one.
func eachLine(f *os.File, fn func([]byte) error) error {
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		trimmed := bytes.TrimRight(line, "\n")
		if len(trimmed) > 0 {
			if e := fn(trimmed); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("state: read log: %w", err)
		}
	}
}

// AuditResult reports an audit outcome. OK means the chain replays cleanly and,
// when the store is anchored, the keyed anchor matches. When !OK, Reason names
// the class of tampering. Artifact carries the id of the first tampered entry
// when one survives to be named (naive body edit, broken link, reorder);
// truncation and whole-log deletion have no surviving artifact id, so Artifact
// is empty and Reason alone carries the finding. All carries the artifacts the
// audit verified, populated only when OK: a caller deriving anything from the
// log reads the audited snapshot itself rather than re-scanning, so what it
// reads is exactly what was verified — no window for the log to change between
// the two.
type AuditResult struct {
	OK       bool
	Artifact string
	Reason   string
	All      []Artifact
}

// auditFault is the internal shape a check returns: a reason plus an optional
// artifact id. The zero value means "no fault".
type auditFault struct {
	artifact string
	reason   string
}

func (f auditFault) ok() bool { return f.reason == "" }

func faultChain(id, reason string) auditFault { return auditFault{artifact: id, reason: reason} }
func faultTruncated(reason string) auditFault { return auditFault{reason: "truncation: " + reason} }
func faultDeleted(reason string) auditFault   { return auditFault{reason: "deletion: " + reason} }
func faultAnchor(reason string) auditFault    { return auditFault{reason: "rewrite: " + reason} }
func faultIncomplete(reason string) auditFault {
	return auditFault{reason: "incomplete-append: " + reason}
}

// Audit replays the whole log, recomputing the hash chain, then — for an
// anchored store — verifies the replayed head and count against the keyed
// anchor. The chain replay catches naive body edits, broken Prev links, and
// reordering. The anchor catches what the chain cannot: a wholesale rewrite
// that recomputes every hash (the HMAC over head‖count won't match without the
// key), tail truncation, and whole-log deletion (the anchor's recorded count
// exceeds the survivors).
func (s *Store) Audit() (AuditResult, error) {
	// Hold the lock across both the chain scan and the anchor verify: a
	// concurrent Append rebinds the anchor, so reading the log and the anchor
	// under one lock is what keeps the two from a torn, false-positive view.
	unlock, err := s.lock()
	if err != nil {
		return AuditResult{}, err
	}
	defer unlock()
	all, err := s.scan(nil)
	if err != nil {
		return AuditResult{}, err
	}
	if fault := replayChain(all); !fault.ok() {
		return tampered(fault), nil
	}
	if s.anchor == nil {
		return AuditResult{OK: true, All: all}, nil
	}
	head := ""
	if n := len(all); n > 0 {
		head = all[n-1].Hash
	}
	fault, err := s.anchor.verify(head, len(all))
	if err != nil {
		return AuditResult{}, err
	}
	if !fault.ok() {
		return tampered(fault), nil
	}
	return AuditResult{OK: true, All: all}, nil
}

// replayChain walks the recomputed hash chain, returning the first fault.
func replayChain(all []Artifact) auditFault {
	prev := ""
	for _, a := range all {
		if a.Prev != prev {
			return faultChain(a.ID, "broken prev link")
		}
		if hashArtifact(a) != a.Hash {
			return faultChain(a.ID, "body hash mismatch")
		}
		prev = a.Hash
	}
	return auditFault{}
}

func tampered(f auditFault) AuditResult {
	return AuditResult{OK: false, Artifact: f.artifact, Reason: f.reason}
}

// lastHash reads the chain head from the log tail — O(tail), not O(log).
func (s *Store) lastHash() (string, error) {
	line, err := lastLine(s.logPath())
	if err != nil {
		return "", err
	}
	if line == nil {
		return "", nil
	}
	var a Artifact
	if err := json.Unmarshal(line, &a); err != nil {
		return "", fmt.Errorf("state: corrupt log tail: %w", err)
	}
	return a.Hash, nil
}

// lastLine returns the final non-empty line of the file, or nil when empty.
func lastLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: open log: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("state: stat log: %w", err)
	}
	size := fi.Size()
	if size == 0 {
		return nil, nil
	}
	for chunk := int64(64 * 1024); ; chunk *= 2 {
		start := size - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return nil, fmt.Errorf("state: read log tail: %w", err)
		}
		end := len(buf)
		for end > 0 && (buf[end-1] == '\n' || buf[end-1] == '\r') {
			end--
		}
		if end == 0 && start == 0 {
			return nil, nil
		}
		if idx := bytes.LastIndexByte(buf[:end], '\n'); idx >= 0 {
			return buf[idx+1 : end], nil
		}
		if start == 0 {
			return buf[:end], nil
		}
	}
}

func hashArtifact(a Artifact) string {
	h := sha256.New()
	fmt.Fprint(h, a.ID, "|", a.Kind, "|", a.Run, "|", a.Time.Format(time.RFC3339Nano), "|", a.Prev, "|")
	for _, p := range a.Parents {
		fmt.Fprint(h, p, ",")
	}
	h.Write(a.Body)
	return hex.EncodeToString(h.Sum(nil))
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("state: rand: %v", err))
	}
	return hex.EncodeToString(b)
}
