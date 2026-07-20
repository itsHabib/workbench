package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

// verb is one compile-time-registered MCP tool: its name, human description, the
// JSON Schema clients validate arguments against, and its handler. Registration
// is a static slice (opt-in per verb); capability-mutating verbs are excluded by
// construction — there is simply no entry for them (spec §6).
type verb struct {
	name        string
	description string
	schema      json.RawMessage
	handle      func(s *Server, args json.RawMessage) (any, error)
}

// verbs is the exposed surface: exactly the four read/record driver verbs. This
// list IS the allowlist — nothing capability-mutating (grant minting) has an
// entry, so it cannot be reached over MCP.
var verbs = []verb{
	{
		name:        "driver_record",
		description: "Append a driver-state event to a run (minting the run on run_imported); returns the sealed event or a structured error. Low-level: the caller supplies the whole event and mints the evt_ id. Prefer driver_transition for new recording — it mints a deterministic id and the body for you.",
		schema:      json.RawMessage(`{"type":"object","properties":{"run":{"type":"string"},"event":{"type":"object"}},"required":["event"]}`),
		handle:      (*Server).recordVerb,
	},
	{
		name:        "driver_transition",
		description: "Record a lifecycle transition from flat facts — the ergonomic recorder. Give the kind, the stream, the actor, and the facts (the body fields for that kind, e.g. stream_pr_opened → {pr,url,head_sha}); the server builds the body, mints a DETERMINISTIC event id from the transition's natural key (so a retry with identical facts is idempotent — you never mint or reuse an id), and stamps the time. Omit run on run_imported to mint one (facts must carry repo/source/generated_at). Returns the sealed event or a structured error.",
		schema:      json.RawMessage(`{"type":"object","properties":{"run":{"type":"string"},"kind":{"type":"string"},"stream":{"type":"string"},"actor":{"type":"string"},"ext_ref":{"type":"string"},"facts":{"type":"object"}},"required":["kind","actor"]}`),
		handle:      (*Server).transitionVerb,
	},
	{
		name:        "driver_state",
		description: "Return the reduced RunState (run record + per-stream derived status) for a run.",
		schema:      json.RawMessage(`{"type":"object","properties":{"run":{"type":"string"}},"required":["run"]}`),
		handle:      (*Server).stateVerb,
	},
	{
		name:        "driver_runs",
		description: "List run summaries, optionally filtered by repo, to live (unfinished) runs, and to the children of a parent run (parent=<dsr_…>).",
		schema:      json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"},"live":{"type":"boolean"},"parent":{"type":"string"}}}`),
		handle:      (*Server).runsVerb,
	},
	{
		name:        "driver_verify",
		description: "Verify a run's hash chain; returns ok or the ErrChainBroken detail.",
		schema:      json.RawMessage(`{"type":"object","properties":{"run":{"type":"string"}},"required":["run"]}`),
		handle:      (*Server).verifyVerb,
	},
	{
		name:        "driver_rollup",
		description: "Join a parent run to its child sub-runs: one row per stream with the parent's mirrored status, the child's own status, the PR, per-child friction (gate cycles, retries, worktree conflict), and whether the mirror agrees with the child (false = the parent recorded ahead of the child's facts). The resume roster + parent↔child cross-check in one read — never touches a child's impl context.",
		schema:      json.RawMessage(`{"type":"object","properties":{"run":{"type":"string"}},"required":["run"]}`),
		handle:      (*Server).rollupVerb,
	},
}

func lookupVerb(name string) (verb, bool) {
	for _, v := range verbs {
		if v.name == name {
			return v, true
		}
	}
	return verb{}, false
}

// toolsListResult renders the registry as an MCP tools/list result.
func toolsListResult() map[string]any {
	tools := make([]map[string]any, 0, len(verbs))
	for _, v := range verbs {
		tools = append(tools, map[string]any{
			"name":        v.name,
			"description": v.description,
			"inputSchema": v.schema,
		})
	}
	return map[string]any{"tools": tools}
}

// recordParams is driver_record's input: an optional run (omitted on
// run_imported → minted) and the event to append.
type recordParams struct {
	Run   string          `json:"run"`
	Event json.RawMessage `json:"event"`
}

// recordVerb appends event to its run, claiming and holding the run lease for the
// session. run_imported with no run mints one; a missing event id is minted (the
// idempotency key); a zero time defaults to now. Structured ledger errors
// (illegal transition, chain break, locked) propagate for the caller to classify.
func (s *Server) recordVerb(args json.RawMessage) (any, error) {
	var p recordParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_record params: %w", err)
	}
	e, minted, err := prepareEvent(p)
	if err != nil {
		return nil, err
	}
	return s.commitEvent(e, minted)
}

// commitEvent claims the run lease, appends e, and cleans up after a speculative
// run mint that Append deduped away — the shared commit tail for driver_record
// and driver_transition. minted reports whether the run id was speculatively
// minted for a run_imported (so a dedupe to an existing run can be unwound).
func (s *Server) commitEvent(e driverstate.Event, minted bool) (any, error) {
	lease, err := s.leaseFor(e.Run, e.Actor)
	if err != nil {
		return nil, err
	}
	out, err := driverstate.Append(s.dir, lease, e)
	if err != nil {
		// A cached lease that lost ownership mid-session (expired after a
		// suspend, or stolen) must be dropped now, so the NEXT record re-Claims
		// immediately instead of reusing the dead lease until the renew tick
		// evicts it (up to TTL/2 later).
		if driverstate.OwnershipLost(err) {
			s.evictLease(e.Run)
		}
		return nil, err
	}
	// A run we speculatively minted for a run_imported that Append then deduped
	// to an existing run is an orphan (its empty run dir + lease): drop it so a
	// lost-response retry leaves nothing behind. The response carries the
	// original run (out.Run), per Append's idempotent-import contract.
	if minted && out.Run != e.Run {
		s.discardMintedRun(e.Run)
	}
	return out, nil
}

// transitionParams is driver_transition's input: the kind, the stream (empty for
// run-scoped kinds), the actor, an optional ext_ref, and the flat facts that
// become the event body. run is optional only for run_imported (minted).
type transitionParams struct {
	Run    string          `json:"run"`
	Kind   string          `json:"kind"`
	Stream string          `json:"stream"`
	Actor  string          `json:"actor"`
	ExtRef string          `json:"ext_ref"`
	Facts  json.RawMessage `json:"facts"`
}

// transitionVerb records a transition from flat facts (spec §6, §4 D3). It builds
// the event, mints the run for a run-less run_imported, derives a DETERMINISTIC
// event id from the transition's natural key so a retry with identical facts is
// idempotent, stamps the time, and commits through the shared tail. The caller
// never mints an id or reuses one on retry — the identity IS the facts.
func (s *Server) transitionVerb(args json.RawMessage) (any, error) {
	var p transitionParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_transition params: %w", err)
	}
	if p.Actor == "" {
		return nil, fmt.Errorf("driver_transition: actor is required")
	}
	facts := p.Facts
	if len(facts) == 0 {
		facts = nil
	}
	e := driverstate.Event{
		Run:    p.Run,
		Kind:   dsc.Kind(p.Kind),
		Stream: p.Stream,
		Actor:  p.Actor,
		ExtRef: p.ExtRef,
		Body:   facts,
	}
	minted, err := ensureRun(&e)
	if err != nil {
		return nil, err
	}
	id, err := deterministicEventID(e)
	if err != nil {
		return nil, err
	}
	e.ID = id
	e.Time = time.Now().UTC()
	return s.commitEvent(e, minted)
}

// deterministicEventID derives an evt_ id from the transition's natural key, so
// two DISTINCT legitimate events never collide (seq/cycle discriminate the
// repeatable kinds; the one-per-stream kinds key on run+stream) while a RETRY of
// one event always maps to the same id — the idempotency Append dedupes on. The
// kind prefix + NUL separator keeps keys from different kinds disjoint.
func deterministicEventID(e driverstate.Event) (string, error) {
	key, err := naturalKey(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(string(e.Kind) + "\x00" + key))
	return "evt_" + hex.EncodeToString(sum[:])[:32], nil
}

// naturalKey is the per-kind identity a retry reproduces. run_imported keys on
// its (repo, source, generated_at) import key — independent of the run id, which
// may not exist yet — matching Append's own import dedupe. stream_attempt and
// review_cycle add their seq/cycle so repeated rounds are distinct; every other
// stream kind is one-per-stream; run_finished is one-per-run.
func naturalKey(e driverstate.Event) (string, error) {
	switch e.Kind {
	case dsc.KindRunImported:
		var b dsc.RunImportedBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return "", fmt.Errorf("driver_transition: run_imported facts: %w", err)
		}
		if b.Repo == "" || b.Source == "" || b.GeneratedAt == "" {
			return "", fmt.Errorf("driver_transition: run_imported facts must carry repo, source, generated_at (the retry-safe import key)")
		}
		return b.Repo + "|" + b.Source + "|" + b.GeneratedAt, nil
	case dsc.KindStreamAttempt:
		var b dsc.StreamAttemptBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return "", fmt.Errorf("driver_transition: stream_attempt facts: %w", err)
		}
		return e.Run + "|" + e.Stream + "|attempt|" + strconv.Itoa(b.Seq), nil
	case dsc.KindReviewCycle:
		var b dsc.ReviewCycleBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return "", fmt.Errorf("driver_transition: review_cycle facts: %w", err)
		}
		return e.Run + "|" + e.Stream + "|cycle|" + strconv.Itoa(b.Cycle), nil
	case dsc.KindRunFinished:
		return e.Run + "|finished", nil
	default:
		return e.Run + "|" + e.Stream + "|" + string(e.Kind), nil
	}
}

// evictLease drops a run's cached lease so the next record re-Claims. It does
// NOT touch the on-disk run — the ledger and any live successor lease stay put.
func (s *Server) evictLease(run string) {
	s.mu.Lock()
	delete(s.leases, run)
	s.mu.Unlock()
}

// discardMintedRun evicts the cached lease AND removes the empty run dir of a
// run this session minted but that Append deduped away. Best-effort: any
// leftover self-expires, but cleaning up keeps import retries from littering run
// dirs.
func (s *Server) discardMintedRun(run string) {
	s.evictLease(run)
	_ = os.RemoveAll(filepath.Join(s.dir, run))
}

// prepareEvent decodes the record event and fills the client-minted defaults:
// the run (explicit param, else minted for run_imported), the event id, and the
// time. It reports whether the run was minted (vs supplied), so the caller can
// unwind a speculative run that Append deduped away. A stream event with no run
// is rejected — there is nothing to append to.
func prepareEvent(p recordParams) (driverstate.Event, bool, error) {
	var e driverstate.Event
	if err := json.Unmarshal(p.Event, &e); err != nil {
		return e, false, fmt.Errorf("invalid driver_record event: %w", err)
	}
	if p.Run != "" {
		e.Run = p.Run
	}
	minted, err := ensureRun(&e)
	if err != nil {
		return e, false, err
	}
	if e.ID == "" {
		id, err := driverstate.NewEventID()
		if err != nil {
			return e, false, err
		}
		e.ID = id
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	return e, minted, nil
}

// ensureRun mints a run id for a run_imported that omitted one (reporting true),
// and rejects any other kind that names no run. A minted import must carry
// generated_at: without the full (repo, source, generated_at) dedupe key a
// retry can never be recognized and every attempt would mint a genuine second
// run — the server refuses rather than silently duplicating (spec §5).
func ensureRun(e *driverstate.Event) (bool, error) {
	if e.Run != "" {
		return false, nil
	}
	if e.Kind != dsc.KindRunImported {
		return false, fmt.Errorf("driver_record: event kind %q requires a run", e.Kind)
	}
	// Minting a run for an omitted-run import is only retry-safe if the import
	// carries its (repo, source, generated_at) dedupe key — otherwise a
	// lost-response retry mints a second genuine run. Refuse rather than
	// duplicate (the shared predicate is the same one the CLI uses).
	if !driverstate.ImportHasDedupeKey(*e) {
		return false, fmt.Errorf("driver_record: a run_imported without an explicit run must carry (repo, source, generated_at) so a retried import cannot mint a duplicate run")
	}
	id, err := driverstate.NewRunID()
	if err != nil {
		return false, err
	}
	e.Run = id
	return true, nil
}

type runParam struct {
	Run string `json:"run"`
}

func (s *Server) stateVerb(args json.RawMessage) (any, error) {
	var p runParam
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_state params: %w", err)
	}
	return driverstate.Reduce(s.dir, p.Run)
}

// runsParams is driver_runs's input: optional repo, live, and parent filters.
type runsParams struct {
	Repo   string `json:"repo"`
	Live   bool   `json:"live"`
	Parent string `json:"parent"`
}

// runsVerb lists run summaries, applying the repo and live filters. live keeps
// only unfinished (open) runs — the resumable set a fresh session reads before
// driver_state (spec §7 F3). The result is always a non-nil slice so it encodes
// as [] not null.
func (s *Server) runsVerb(args json.RawMessage) (any, error) {
	var p runsParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_runs params: %w", err)
	}
	all, err := driverstate.Runs(s.dir)
	if err != nil {
		return nil, err
	}
	return filterRuns(all, p), nil
}

func filterRuns(all []driverstate.RunSummary, p runsParams) []driverstate.RunSummary {
	out := make([]driverstate.RunSummary, 0, len(all))
	for _, r := range all {
		if p.Repo != "" && r.Repo != p.Repo {
			continue
		}
		if p.Live && r.Status != driverstate.RunStatusOpen {
			continue
		}
		if p.Parent != "" && r.Parent != p.Parent {
			continue
		}
		out = append(out, r)
	}
	return out
}

// rollupVerb joins a parent run to its child sub-runs (spec §7 Resume, §4 D5).
// Pure read — no lease. Reduce errors on the parent (a broken chain) propagate;
// an unreadable child surfaces per-stream as agrees:false, never as a hard fail.
func (s *Server) rollupVerb(args json.RawMessage) (any, error) {
	var p runParam
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_rollup params: %w", err)
	}
	return driverstate.Rollup(s.dir, p.Run)
}

// verifyResult is driver_verify's ok payload.
type verifyResult struct {
	Run string `json:"run"`
	OK  bool   `json:"ok"`
}

func (s *Server) verifyVerb(args json.RawMessage) (any, error) {
	var p runParam
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("invalid driver_verify params: %w", err)
	}
	if err := driverstate.Verify(s.dir, p.Run); err != nil {
		return nil, err
	}
	return verifyResult{Run: p.Run, OK: true}, nil
}

// verbErrorPayload is the structured error surfaced to the client on the isError
// path: a stable Code plus any code-specific detail. It is what makes F2 work —
// the validator hands the agent a machine-branchable reason it can correct on
// (spec §6, §7 F2).
type verbErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	From    string `json:"from,omitempty"`
	Event   string `json:"event,omitempty"`
	Holder  string `json:"holder,omitempty"`
}

// classifyError maps a ledger error to its stable code and detail. The three
// contract error codes (spec §6) are recognised structurally; anything else is a
// generic "error" so an unexpected failure still reaches the client legibly.
func classifyError(err error) verbErrorPayload {
	var illegal driverstate.ErrIllegalTransition
	if errors.As(err, &illegal) {
		return verbErrorPayload{Code: "ErrIllegalTransition", Message: err.Error(), From: illegal.From, Event: illegal.Event}
	}
	var locked driverstate.ErrLocked
	if errors.As(err, &locked) {
		return verbErrorPayload{Code: "ErrLocked", Message: err.Error(), Holder: locked.Holder}
	}
	if errors.Is(err, driverstate.ErrChainBroken) {
		return verbErrorPayload{Code: "ErrChainBroken", Message: err.Error()}
	}
	return verbErrorPayload{Code: "error", Message: err.Error()}
}
