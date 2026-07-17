package driverstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// Append records e on run's ledger under dir. It requires the live lease l,
// seals the hash chain, and is idempotent by e.ID (a retried append returns the
// original committed event). The returned Event carries the sealed Prev/Hash and
// the truncated time actually persisted.
//
// The write window (all under the per-append lock): revalidate the lease → heal
// a torn tail → read the head → validate (contract grammar, state machine,
// monotonicity, seq) → seal → write + fsync. A run_imported additionally holds a
// state-root import lock across the dedupe scan and its first append, so two
// concurrent imports of the same key into different runs cannot both mint a run.
// See the package doc for the chain rule.
func Append(dir string, l Lease, e Event) (Event, error) {
	// Bind the write to the lease's OWN dir: a caller passing a dir that
	// disagrees would validate ownership in one run root and write into another
	// (spec §6). The lease's dir is the single source of truth for where to write.
	if err := bindDir(dir, l); err != nil {
		return Event{}, err
	}
	if e.Run != "" && e.Run != l.run {
		return Event{}, fmt.Errorf("driverstate: append: event run %q does not match lease run %q", e.Run, l.run)
	}
	e.Run = l.run
	if e.V == "" {
		e.V = dsc.Version
	}
	// Writer-supplied time, truncated to whole UTC seconds so the RFC 3339 form
	// is byte-stable across the Go and TS emitters (spec §5).
	e.Time = e.Time.UTC().Truncate(time.Second)

	// A run_imported serializes its dedupe-scan-then-append under a state-root
	// import lock so concurrent imports of one key into DIFFERENT runs cannot
	// both observe "not found" and each commit. Only imports pay this cost; the
	// import lock is always taken before the per-run append lock, so no cycle.
	if e.Kind == dsc.KindRunImported {
		var out Event
		err := withLock(importLockPath(l.dir), func() error {
			var e2 error
			out, e2 = appendLocked(l, e)
			return e2
		})
		return out, err
	}
	return appendLocked(l, e)
}

// appendLocked performs the validated, hash-chained write under the per-run
// append lock. Callers appending a run_imported hold the state-root import lock
// around this call; every other kind takes only the append lock here.
func appendLocked(l Lease, e Event) (Event, error) {
	rd := runDir(l.dir, l.run)
	lockPath := appendLockPath(rd)
	if err := acquireLock(lockPath); err != nil {
		return Event{}, err
	}
	defer releaseLock(lockPath)

	// Revalidate the lease INSIDE the lock: a holder that passed a pre-lock
	// check could have waited behind another append until its lease expired and
	// was stolen. The lock serializes bytes; only a live-lease recheck here
	// preserves single-writer ownership (spec §8, review P1).
	if err := requireLease(l); err != nil {
		return Event{}, err
	}

	ledgerPath := filepath.Join(rd, "events.jsonl")
	events, validOffset, err := readAndHealLedger(ledgerPath)
	if err != nil {
		return Event{}, err
	}

	// Idempotency: a committed ID short-circuits before any validation, so a
	// retried terminal event is a no-op that returns the original.
	for _, prev := range events {
		if prev.ID == e.ID {
			return prev, nil
		}
	}

	// Import dedupe (spec §5): a run_imported whose (repo, source, generated_at)
	// already minted a run — in this dir or any sibling — returns that original
	// event, so a retried import with a fresh ID/run cannot mint a second run.
	// The state-root import lock (held by Append) makes this scan-then-write
	// atomic across runs.
	if e.Kind == dsc.KindRunImported {
		orig, found, err := dedupeImport(l.dir, e)
		if err != nil {
			return Event{}, err
		}
		if found {
			return orig, nil
		}
	}

	if err := dsc.ValidateEvent(e); err != nil {
		return Event{}, err
	}
	if err := checkTransition(events, e); err != nil {
		return Event{}, err
	}
	if err := checkMonotonic(events, e); err != nil {
		return Event{}, err
	}

	e.Prev = headHash(events)
	e.Hash = dsc.ComputeHash(e)

	line := append(dsc.EncodeEvent(e), '\n')
	if err := appendSynced(ledgerPath, line, validOffset); err != nil {
		return Event{}, err
	}
	return e, nil
}

func appendLockPath(rd string) string  { return filepath.Join(rd, "append.lock") }
func importLockPath(dir string) string { return filepath.Join(dir, "import.lock") }

// bindDir rejects an Append whose dir does not name the lease's own state root,
// and whose run is not a safe single path component. A zero-value lease (no run)
// can never write.
func bindDir(dir string, l Lease) error {
	if l.run == "" {
		return ErrNotHolder
	}
	if err := validateRunID(l.run); err != nil {
		return fmt.Errorf("driverstate: append: %w", err)
	}
	if filepath.Clean(dir) != filepath.Clean(l.dir) {
		return fmt.Errorf("driverstate: append: dir %q does not match lease dir %q", dir, l.dir)
	}
	return nil
}

// importIdent is ship's proven import key: a retried import reusing the same
// (repo, source, generated_at) must not mint a second run.
type importIdent struct {
	repo        string
	source      string
	generatedAt string
}

// ImportHasDedupeKey reports whether a run_imported event carries the full
// (repo, source, generated_at) import key — the anchor that makes a retried
// import with a freshly minted run idempotent (Append dedupes on it). A client
// that mints a run for an omitted-run import uses this to refuse a key-less
// import a lost-response retry could duplicate. Non-import events, and imports
// missing any key component, report false.
func ImportHasDedupeKey(e Event) bool {
	if e.Kind != dsc.KindRunImported {
		return false
	}
	_, ok := importKey(e)
	return ok
}

// importKey extracts the dedupe key from a run_imported body. It reports false
// when any component is absent — an import missing generated_at carries no
// reliable key, so it is never deduped (two distinct imports of one repo/source
// must not collide).
func importKey(e Event) (importIdent, bool) {
	var b dsc.RunImportedBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return importIdent{}, false
	}
	if b.Repo == "" || b.Source == "" || b.GeneratedAt == "" {
		return importIdent{}, false
	}
	return importIdent{repo: b.Repo, source: b.Source, generatedAt: b.GeneratedAt}, true
}

// dedupeImport scans every run under dir for a committed run_imported carrying
// e's import key. The current run is included: a re-import into the same run
// returns the original rather than an illegal-transition rejection. Callers hold
// the state-root import lock across this scan; over many runs the scan can run
// long, so each iteration heartbeats that lock (touchLock) to keep another
// waiter from age-breaking a live lock.
func dedupeImport(dir string, e Event) (Event, bool, error) {
	key, ok := importKey(e)
	if !ok {
		return Event{}, false, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Event{}, false, nil
		}
		return Event{}, false, fmt.Errorf("driverstate: dedupe import: %w", err)
	}
	lock := importLockPath(dir)
	for _, entry := range entries {
		touchLock(lock)
		if !entry.IsDir() {
			continue
		}
		orig, found := importInRun(filepath.Join(dir, entry.Name()), key)
		if found {
			return orig, true, nil
		}
	}
	return Event{}, false, nil
}

// importInRun returns the run_imported event in one run dir matching key, if any.
// A missing or corrupt sibling ledger is not a hard error here — it simply holds
// no matching import, so a fresh import is never blocked by an unrelated bad run.
// The read is torn-tail tolerant: only bytes through the last complete newline
// are decoded (we do NOT heal — we don't hold that run's lock), so a sibling
// caught mid-append cannot hide its already-committed import from the scan.
func importInRun(rd string, key importIdent) (Event, bool) {
	data, err := os.ReadFile(filepath.Join(rd, "events.jsonl"))
	if err != nil {
		return Event{}, false
	}
	events, err := decodeLedger(trimTornTail(data))
	if err != nil {
		return Event{}, false
	}
	for _, e := range events {
		if e.Kind != dsc.KindRunImported {
			continue
		}
		if k, ok := importKey(e); ok && k == key {
			return e, true
		}
	}
	return Event{}, false
}

func headHash(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Hash
}

// checkMonotonic rejects an event older than the head — catches clock skew and
// replayed writes (spec §8).
func checkMonotonic(events []Event, e Event) error {
	if len(events) == 0 {
		return nil
	}
	head := events[len(events)-1]
	if e.Time.Before(head.Time) {
		return fmt.Errorf("driverstate: append: time %s is older than head %s (per-run monotonicity)", e.Time.Format(time.RFC3339), head.Time.Format(time.RFC3339))
	}
	return nil
}

// checkTransition enforces the state machine at write time (spec §5 table): the
// run-scoped kinds by their run-level rules, every other kind by folding its
// stream's prior events to a current status and applying the transition.
func checkTransition(events []Event, e Event) error {
	// run_finished is the no-retry declaration: once it is on the ledger, no
	// further event — a reopening stream transition or a second run_finished —
	// is legal (spec §5). Idempotent same-ID re-appends short-circuit earlier.
	if runFinished(events) {
		return ErrIllegalTransition{From: "run_finished", Event: string(e.Kind)}
	}
	switch e.Kind {
	case dsc.KindRunImported:
		if len(events) > 0 {
			return ErrIllegalTransition{From: "run_open", Event: string(e.Kind)}
		}
		return nil
	case dsc.KindRunFinished:
		return checkRunFinished(events)
	}
	if len(events) == 0 {
		// A stream event with no run_imported yet: the run was never opened.
		return ErrIllegalTransition{From: "run_absent", Event: string(e.Kind)}
	}
	cur := streamStatus(events, e.Stream)
	if _, err := applyStream(cur, e); err != nil {
		return err
	}
	if e.Kind == dsc.KindStreamAttempt {
		return checkSeq(events, e)
	}
	return nil
}

// checkRunFinished allows run_finished only when every known stream is terminal
// ({merged, skipped, failed}); finishing is the no-retry declaration (spec §5).
func checkRunFinished(events []Event) error {
	if len(events) == 0 {
		return ErrIllegalTransition{From: "run_absent", Event: string(dsc.KindRunFinished)}
	}
	for _, s := range gatherStreams(events) {
		st := streamStatus(events, s)
		if !terminalStatus(st) {
			return ErrIllegalTransition{From: st, Event: string(dsc.KindRunFinished)}
		}
	}
	return nil
}

// runFinished reports whether the run has already been closed by a run_finished
// event.
func runFinished(events []Event) bool {
	for _, e := range events {
		if e.Kind == dsc.KindRunFinished {
			return true
		}
	}
	return false
}

func terminalStatus(status string) bool {
	switch status {
	case dsc.StatusMerged, dsc.StatusSkipped, dsc.StatusFailed:
		return true
	default:
		return false
	}
}

// gatherStreams is the full stream set of a run: the manifest snapshot from
// run_imported plus any stream that later carried an event.
func gatherStreams(events []Event) []string {
	seen := make(map[string]bool)
	var streams []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		streams = append(streams, s)
	}
	for _, e := range events {
		if e.Kind == dsc.KindRunImported {
			var b dsc.RunImportedBody
			if err := json.Unmarshal(e.Body, &b); err == nil {
				for _, s := range b.Streams {
					add(s.Stream)
				}
			}
		}
		add(e.Stream)
	}
	return streams
}

// streamStatus folds a stream's prior events to its current derived status.
// Stored events were validated on write, so applyStream never errors here; a
// stray illegal fold is ignored rather than corrupting the status.
func streamStatus(events []Event, stream string) string {
	status := dsc.StatusPending
	for _, e := range events {
		if e.Stream != stream {
			continue
		}
		if ns, err := applyStream(status, e); err == nil {
			status = ns
		}
	}
	return status
}

// applyStream is the stream-scoped transition table (spec §5). It returns the
// next status or ErrIllegalTransition{From, Event}.
func applyStream(cur string, e Event) (string, error) {
	illegal := ErrIllegalTransition{From: cur, Event: string(e.Kind)}
	switch e.Kind {
	case dsc.KindStreamDispatched:
		if cur == dsc.StatusPending || cur == dsc.StatusFailed {
			return dsc.StatusDispatched, nil
		}
	case dsc.KindStreamAttempt:
		if cur != dsc.StatusDispatched {
			return "", illegal
		}
		var b dsc.StreamAttemptBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return "", fmt.Errorf("driverstate: stream_attempt body: %w", err)
		}
		if !b.Terminal {
			return dsc.StatusDispatched, nil
		}
		if b.FailureCategory != "" {
			return dsc.StatusFailed, nil
		}
		return dsc.StatusLanded, nil
	case dsc.KindStreamFailed:
		if cur == dsc.StatusDispatched {
			return dsc.StatusFailed, nil
		}
	case dsc.KindStreamPROpened:
		if cur == dsc.StatusLanded {
			return dsc.StatusPROpen, nil
		}
	case dsc.KindReviewCycle:
		if cur == dsc.StatusPROpen {
			return dsc.StatusPROpen, nil
		}
	case dsc.KindStreamMerged:
		if cur == dsc.StatusPROpen {
			return dsc.StatusMerged, nil
		}
	case dsc.KindStreamSkipped:
		if cur == dsc.StatusPending || cur == dsc.StatusFailed {
			return dsc.StatusSkipped, nil
		}
	}
	return "", illegal
}

// checkSeq enforces append-only monotone stream_attempt seq per stream: a new
// attempt's seq must exceed every prior attempt's on the same stream.
func checkSeq(events []Event, e Event) error {
	var b dsc.StreamAttemptBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return fmt.Errorf("driverstate: stream_attempt body: %w", err)
	}
	maxSeq := 0
	for _, prev := range events {
		if prev.Stream != e.Stream || prev.Kind != dsc.KindStreamAttempt {
			continue
		}
		var pb dsc.StreamAttemptBody
		if err := json.Unmarshal(prev.Body, &pb); err != nil {
			continue
		}
		if pb.Seq > maxSeq {
			maxSeq = pb.Seq
		}
	}
	if b.Seq <= maxSeq {
		return fmt.Errorf("driverstate: append: stream_attempt seq %d must exceed prior max %d", b.Seq, maxSeq)
	}
	return nil
}

// readAndHealLedger reads a run's ledger, healing a torn final line: bytes after
// the last newline are a crash's partial write and are truncated from the file
// before parsing. It returns the decoded events and the byte offset the next
// append must write at (the healed length).
func readAndHealLedger(path string) ([]Event, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("driverstate: read ledger: %w", err)
	}
	validLen := lastNewline(data)
	if validLen != int64(len(data)) {
		if err := os.Truncate(path, validLen); err != nil {
			return nil, 0, fmt.Errorf("driverstate: heal torn tail: %w", err)
		}
	}
	events, err := decodeLedger(data[:validLen])
	if err != nil {
		return nil, 0, err
	}
	return events, validLen, nil
}

// lastNewline returns the length of data up to and including its last newline —
// the boundary of the last complete line. Zero if there is none.
func lastNewline(data []byte) int64 {
	i := bytes.LastIndexByte(data, '\n')
	if i < 0 {
		return 0
	}
	return int64(i + 1)
}

// trimTornTail returns data through its last complete line, dropping any partial
// bytes a crash left after the final newline. Unlike readAndHealLedger it does
// NOT truncate the file — the read-only dedupe scan uses it on sibling ledgers
// whose lock it does not hold.
func trimTornTail(data []byte) []byte {
	return data[:lastNewline(data)]
}

// decodeLedger parses complete lines into events AND verifies the persisted hash
// chain: every line must decode, each event's Hash must seal its own canonical
// bytes, and each Prev must link to the prior event's Hash. Any failure is a loud
// ErrChainBroken — Append never seals a new event onto an unverified head, and
// the torn FINAL line was already healed away before this runs (spec §8).
func decodeLedger(data []byte) ([]Event, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var events []Event
	prev := ""
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		e, err := dsc.DecodeEvent(line)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChainBroken, err)
		}
		if err := verifyLink(e, prev); err != nil {
			return nil, err
		}
		events = append(events, e)
		prev = e.Hash
	}
	return events, nil
}

// verifyLink checks one event seals its own bytes and links to prev — the two
// halves of the hash-chain guarantee.
func verifyLink(e Event, prev string) error {
	if e.Prev != prev {
		return fmt.Errorf("%w: event %q prev %q does not link to prior hash %q", ErrChainBroken, e.ID, e.Prev, prev)
	}
	if got := dsc.ComputeHash(e); got != e.Hash {
		return fmt.Errorf("%w: event %q hash %q does not seal its bytes (computed %q)", ErrChainBroken, e.ID, e.Hash, got)
	}
	return nil
}

// appendSynced appends line at offset (the healed length) and fsyncs. Opening
// with O_APPEND after a heal-truncate is race-free because the whole window runs
// under the append lock.
func appendSynced(path string, line []byte, offset int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("driverstate: open ledger: %w", err)
	}
	if _, err := f.WriteAt(line, offset); err != nil {
		f.Close()
		return fmt.Errorf("driverstate: write event: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("driverstate: fsync event: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("driverstate: close ledger: %w", err)
	}
	return nil
}
