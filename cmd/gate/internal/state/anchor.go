package state

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// The keyed tip anchor is what makes tamper-*evidence* survive an adversary who
// can write the state dir. The hash chain alone is unkeyed SHA-256: anyone with
// file-write can rewrite the log end-to-end, recomputing every Prev/Hash, and a
// pure replay still says "intact". The anchor binds the chain head and entry
// count under an HMAC keyed by a secret held OUTSIDE the state dir, so a
// state-dir-only writer cannot forge a matching anchor for a rewritten or
// truncated log. It also records the expected count, which is how truncation and
// whole-log deletion become detectable — a shorter (or absent) log no longer
// matches the count the anchor pinned.
//
// The anchor file lives outside log.jsonl's directory precisely so the trust
// boundary is different: writing the log must not grant the ability to rewrite
// the anchor.

// anchor binds a store's chain head and count under a key held outside the
// state dir. A nil *anchor means the store keeps only the unkeyed chain.
type anchor struct {
	path    string
	keyPath string
}

// anchorRecord is the persisted anchor: the expected head and count, plus an
// HMAC over them. The HMAC guards against forgery by a state-dir-only writer
// and against silent corruption of the anchor file itself.
type anchorRecord struct {
	Head  string `json:"head"`
	Count int    `json:"count"`
	MAC   string `json:"mac"`
}

// ErrAnchorKeyMissing fires when the anchor key is absent where one must
// already exist — a coded error so a deleted or misplaced key is diagnosable
// instead of surfacing as an anchor mismatch on every audit. Mirrors
// capability's loud-missing-key discipline: a verify path never mints a key.
var ErrAnchorKeyMissing = errors.New("anchor_key_missing")

// ErrAnchorMissing fires when the log holds entries but no anchor is present.
// A verify path treats a missing anchor as suspicious (the anchor could have
// been deleted alongside a rewrite), never as "nothing to check".
var ErrAnchorMissing = errors.New("anchor_missing")

// ErrRebindTruncation fires when an append would advance the anchor over a log
// shorter than the anchor already pinned — entries were removed. Resealing at
// the shorter count would launder truncation into "intact", so the append
// refuses instead and Audit keeps reporting the loss.
var ErrRebindTruncation = errors.New("rebind_truncation")

// ErrRebindRewrite fires when an append would advance the anchor over a log
// whose entry at the pinned count no longer carries the pinned head — the
// anchored prefix was rewritten (and rehashed into a self-consistent chain).
// Resealing would HMAC-bind the forged history as crash recovery, so the
// append refuses instead and Audit keeps reporting the mismatch.
var ErrRebindRewrite = errors.New("rebind_rewrite")

// ErrRebindUnprovenSuffix fires when an append would advance the anchor over
// more unanchored entries than the one-append crash window can leave. The
// chain is unkeyed, so entries beyond the pinned head are unauthenticated;
// recovery may seal at most the single entry whose anchor update crashed plus
// the entry being appended now. Anything further was not left by a crash —
// refuse, and let Audit keep reporting the gap.
var ErrRebindUnprovenSuffix = errors.New("rebind_unproven_suffix")

// bind records the current head and count under the anchor's key. It runs on
// the append path inside the store lock, so it may create the key on first use
// — that is the one place a fresh anchor key is legitimate.
//
// The write is atomic (temp + rename) so a crash never leaves a torn anchor.
// The log is fsync'd before this call, so the anchor never claims more than is
// durably logged; a crash strictly between the log fsync and the rename leaves
// the anchor lagging by one entry, which the next Audit reports as a mismatch
// rather than silently accepting — the conservative failure for an audit log.
func (a *anchor) bind(head string, count int) error {
	key, err := loadOrCreateAnchorKey(a.keyPath)
	if err != nil {
		return err
	}
	rec := anchorRecord{Head: head, Count: count, MAC: anchorMAC(key, head, count)}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("state: marshal anchor: %w", err)
	}
	// 0o700: the anchor record shares the key-custody dir, a tighter trust
	// domain than the state dir — keep it out of world traversal.
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("state: anchor dir: %w", err)
	}
	return writeFileAtomic(a.path, raw)
}

// writeFileAtomic writes raw to path via a temp file and rename, so a reader
// never observes a partially written file.
func writeFileAtomic(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".anchor-*.tmp")
	if err != nil {
		return fmt.Errorf("state: anchor temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("state: write anchor: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("state: sync anchor: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("state: close anchor: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("state: chmod anchor: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("state: rename anchor: %w", err)
	}
	return nil
}

// verify checks a replayed head and count against the anchor. The key is
// loaded, never created: a missing key on the verify path is a loud error.
// A missing anchor when the log has entries is reported as tampering, since a
// rewrite could have deleted the anchor to dodge the check.
func (a *anchor) verify(head string, count int) (auditFault, error) {
	rec, ok, err := a.read()
	if err != nil {
		return auditFault{}, err
	}
	if !ok {
		// No anchor record + an empty log is a fresh, never-appended store:
		// nothing is pinned yet, so it is intact — and saying so needs no key,
		// so `gate audit` on a brand-new state dir must not error on a missing
		// key. A non-empty log with no record is a deletion (the record could
		// have been removed to dodge the check); that too is nameable without
		// the key.
		if count == 0 {
			return auditFault{}, nil
		}
		return faultDeleted(fmt.Sprintf("anchor missing but log holds %d entries", count)), nil
	}
	key, err := loadAnchorKey(a.keyPath)
	if err != nil {
		return auditFault{}, err
	}
	if !hmac.Equal([]byte(anchorMAC(key, rec.Head, rec.Count)), []byte(rec.MAC)) {
		return faultAnchor("anchor mac invalid (anchor file corrupt or forged)"), nil
	}
	if count == 0 && rec.Count > 0 {
		return faultDeleted(fmt.Sprintf("anchor expects %d entries, log is empty", rec.Count)), nil
	}
	if count < rec.Count {
		return faultTruncated(fmt.Sprintf("anchor expects %d entries, log has %d", rec.Count, count)), nil
	}
	if count == rec.Count && head == rec.Head {
		return auditFault{}, nil
	}
	// count > rec.Count by exactly one is the benign crash window: the log
	// entry synced but the anchor rename didn't land. Name it for what it is
	// rather than crying "rewrite".
	if count == rec.Count+1 {
		return faultIncomplete(fmt.Sprintf("log has %d entries, anchor pinned %d — append interrupted before the anchor updated", count, rec.Count)), nil
	}
	return faultAnchor(fmt.Sprintf("anchor head/count mismatch: log has (%s, %d), anchor pinned (%s, %d)", short(head), count, short(rec.Head), rec.Count)), nil
}

// read returns the persisted anchor record and whether one exists.
func (a *anchor) read() (anchorRecord, bool, error) {
	raw, err := os.ReadFile(a.path)
	if os.IsNotExist(err) {
		return anchorRecord{}, false, nil
	}
	if err != nil {
		return anchorRecord{}, false, fmt.Errorf("state: read anchor: %w", err)
	}
	var rec anchorRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return anchorRecord{}, false, fmt.Errorf("state: parse anchor: %w", err)
	}
	return rec, true, nil
}

func anchorMAC(key []byte, head string, count int) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(head))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.Itoa(count)))
	return hex.EncodeToString(mac.Sum(nil))
}

// loadAnchorKey reads the anchor key; it never creates one.
func loadAnchorKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return key, nil
	}
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrAnchorKeyMissing, path)
	}
	return nil, fmt.Errorf("state: read anchor key: %w", err)
}

// loadOrCreateAnchorKey reads the anchor key, minting a fresh one only when
// none exists yet. Only the append (bind) path may take this branch.
func loadOrCreateAnchorKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("state: read anchor key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("state: rand: %w", err)
	}
	// 0o700 on the key-custody dir: its whole reason for living outside the
	// state dir is to be a tighter trust domain, so it should not be
	// world-traversable even though the key file itself is 0o600.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("state: anchor key dir: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("state: write anchor key: %w", err)
	}
	return key, nil
}

func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
