// Command org is the Baton home: the runtime that keeps role continuity
// chains on disk and lets sessions act as roles.
//
// The kernel — what a role is, which record may extend a chain, what the fold
// means — lives in contracts/org and is imported as types and laws, never
// wrapped or re-decided here. This binary owns the three things a pure kernel
// cannot: WHERE chains live (a state directory of JSONL files + content-
// addressed blobs), WHEN a record is stamped (the home's clock, the home's
// lock), and HOW a fresh session re-enters (the boot index, byte-capped for
// injection).
//
// Verbs map one-to-one onto record kinds; a session's lifecycle is:
//
//	org attach -role lead:x        # become the incarnation (refused if held)
//	org boot   -role lead:x        # the index a session starts from
//	org claim  -role lead:x -work dossier:org/p1/t3
//	org note   -role lead:x -body "found the bug in ..."
//	org yield  -role lead:x -work dossier:org/p1/t3 -body "..."
//	org release -role lead:x       # hand the role back cleanly
//
// Exit codes are a load-bearing seam: 0 ok · 1 the kernel refused the record
// (stderr carries the reason id) · 2 usage · 4 error. A refusal is not an
// error: it is the substrate doing its one job.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/org/internal/home"
	"github.com/itsHabib/workbench/cmd/org/internal/render"
	"github.com/itsHabib/workbench/cmd/org/internal/survey"
	"github.com/itsHabib/workbench/contracts/org"
)

const (
	codeOK      = 0
	codeRefused = 1
	codeUsage   = 2
	codeError   = 4
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// verbs maps each verb to its handler. Write verbs append exactly one record;
// read verbs never take the lock.
var verbs = map[string]func(*env, []string) error{
	"charter":    cmdCharter,
	"annul":      cmdAnnul,
	"attach":     cmdAttach,
	"assign":     cmdAssign,
	"transfer":   cmdTransfer,
	"unassign":   cmdWork(org.KindUnassign),
	"claim":      cmdWork(org.KindClaim),
	"yield":      cmdWork(org.KindYield),
	"complete":   cmdWork(org.KindComplete),
	"abandon":    cmdWork(org.KindAbandon),
	"release":    cmdRelease,
	"begin":      cmdBegin,
	"done":       cmdDone,
	"retire":     cmdBare(org.KindRetire),
	"seal":       cmdBare(org.KindSeal),
	"takeover":   cmdParty(org.KindTakeover),
	"revoke":     cmdParty(org.KindRevoke),
	"delegate":   cmdParty(org.KindDelegate),
	"intent":     cmdIntent,
	"resolve":    cmdResolve,
	"escalate":   cmdBare(org.KindEscalation),
	"note":       cmdAdvisory(org.KindNote),
	"mark":       cmdAdvisory(org.KindMark),
	"checkpoint": cmdAdvisory(org.KindCheckpoint),
	"report":     cmdAdvisory(org.KindReport),
	"message":    cmdAdvisory(org.KindMessage),
	"boot":       cmdBoot,
	"intake":     cmdIntake,
	"status":     cmdStatus,
	"sweep":      cmdSweep,
	"log":        cmdLog,
	"verify":     cmdVerify,
	"blob":       cmdBlob,
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return codeUsage
	}
	cmd, ok := verbs[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "org: unknown verb %q\n", args[0])
		usage(stderr)
		return codeUsage
	}
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr}
	err := cmd(e, args[1:])
	if err == nil {
		return codeOK
	}
	if org.RefusalReason(err) != "" {
		// The kernel's refusal message already names the reason id; print it
		// verbatim so scripted callers grep one string.
		fmt.Fprintf(stderr, "%v\n", err)
		return codeRefused
	}
	fmt.Fprintf(stderr, "org: %v\n", err)
	return codeError
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: org <verb> [flags]

lifecycle   charter · attach · release · retire · takeover · revoke · delegate
correction  annul (repudiate the tip; corrects forward, does not revert)
work        assign · transfer · unassign · claim · yield · complete · abandon
composite   begin (attach+assign+claim) · done (claim?+complete+release)
obligations intent · resolve · escalate · seal
narrative   note · mark · checkpoint · report · message   (-body "…" | -body -)
read        boot · intake · status · sweep · log · verify · blob

shape: org <verb> -state <dir> -tenant <id> -role <id> [verb flags]
       flags follow the verb — org -state … <verb> is an unknown verb, and any
       flag after a positional argument is silently ignored
       -state defaults to ORG_STATE, -tenant to ORG_TENANT`)
}

// env carries the streams so every handler is testable without the process.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// scope is the flag set every verb shares: which home, which chain, which
// identity, and how to speak.
type scope struct {
	fs          *flag.FlagSet
	state       string
	tenant      string
	role        string
	incarnation string
	strict      bool
	asJSON      bool
}

func newScope(name string) *scope {
	s := &scope{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	s.fs.StringVar(&s.state, "state", envOr("ORG_STATE", defaultState()), "state directory")
	s.fs.StringVar(&s.tenant, "tenant", envOr("ORG_TENANT", "mh"), "tenant id")
	s.fs.StringVar(&s.role, "role", "", "role id, e.g. lead:agentic-development")
	s.fs.StringVar(&s.incarnation, "incarnation", os.Getenv("ORG_INCARNATION"),
		"present the writer's incarnation id (the digest attach printed); defaults to writing as the current holder")
	s.fs.BoolVar(&s.strict, "strict", os.Getenv("ORG_STRICT") != "",
		"refuse to write without an explicitly presented incarnation")
	s.fs.BoolVar(&s.asJSON, "json", false, "emit a JSON receipt instead of text")
	return s
}

func defaultState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "org-state"
	}
	return filepath.Join(home, "dev", "org", "state")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (s *scope) open(args []string, needRole bool) (*home.Home, error) {
	if err := s.fs.Parse(args); err != nil {
		return nil, err
	}
	if needRole && s.role == "" {
		return nil, fmt.Errorf("-role is required")
	}
	return home.Open(s.state)
}

// body reads a -body flag value: "-" is stdin, anything else is literal.
func body(e *env, v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	if v != "-" {
		return []byte(v), nil
	}
	b, err := io.ReadAll(e.stdin)
	if err != nil {
		return nil, fmt.Errorf("read body from stdin: %w", err)
	}
	return b, nil
}

// receipt is the machine-readable result of one append: the record's identity
// plus the state summary a caller decides its next move from. It is the JSON
// half of the exit-code seam.
type receipt struct {
	Kind     string    `json:"kind"`
	Seq      int64     `json:"seq"`
	Digest   string    `json:"digest"`
	Phase    org.Phase `json:"phase"`
	Tip      string    `json:"tip"`
	Holder   string    `json:"holder,omitempty"`
	Active   string    `json:"active,omitempty"`
	Dangling string    `json:"dangling,omitempty"`
	Held     int       `json:"held"`
	Fence    int64     `json:"fence"`
}

// cmdRelease is cmdBare(release) plus the warning §4.3 asked for: releasing
// while still holding unfinished work is the moment the wrong exit verb is
// detectable, so say so — on stderr, without blocking, because pausing a
// lane (yield then release) is legitimate and only the operator knows.
func cmdRelease(e *env, args []string) error {
	s := newScope("release")
	text := s.fs.String("body", "", `narrative body ("-" reads stdin)`)
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	b, err := body(e, *text)
	if err != nil {
		return err
	}
	_, state, loadErr := h.Load(s.tenant, s.role)
	if n := len(state.Held); loadErr == nil && n > 0 {
		fmt.Fprintf(e.stderr, "warning: releasing while still holding %d item(s); finished work exits via complete (or done), yield means pausing\n", n)
	}
	return appendAndReport(e, h, s, home.Draft{Kind: org.KindRelease, Body: b})
}

// step appends one record to the session's own role inside a composite verb,
// honoring strict identity exactly as appendAndReport does, and returns the
// receipt for the roll-up.
func step(h *home.Home, s *scope, d home.Draft) (receipt, org.RoleState, error) {
	if d.Incarnation == "" {
		d.Incarnation = s.incarnation
	}
	return stepRole(h, s, s.role, d)
}

// stepRole appends to a NAMED role. The session's incarnation is deliberately
// not defaulted in: presenting one role's writer identity on another role's
// chain is not a convenience, it is a misattribution, and the kernel would be
// right to refuse it.
func stepRole(h *home.Home, s *scope, role string, d home.Draft) (receipt, org.RoleState, error) {
	if s.strict && d.Incarnation == "" && !home.MintsIdentity(d.Kind) {
		return receipt{}, org.RoleState{}, fmt.Errorf("strict mode: %s requires -incarnation (or ORG_INCARNATION); writing as the holder is disabled", d.Kind)
	}
	r, state, err := h.Append(s.tenant, role, d)
	if err != nil {
		return receipt{}, state, nameTheMissingAttach(h, s.tenant, role, err)
	}
	digest, err := org.DigestOf(r)
	if err != nil {
		return receipt{}, state, err
	}
	return receipt{
		Kind: r.Kind, Seq: r.Seq, Digest: digest, Phase: state.Phase,
		Tip: state.Tip, Holder: state.Holder, Active: state.Active,
		Dangling: state.Dangling, Held: len(state.Held), Fence: state.Fence,
	}, state, nil
}

// nameTheMissingAttach rewrites the detail on an incarnation_missing refusal
// when the chain has never been attached at all.
//
// The refusal is correct and the cause it names is not. On a charter-only lane
// nothing was missing from the command: no incarnation COULD exist yet, because
// none has ever been minted. An agent holding a digest from an earlier session
// reads "must name the incarnation that wrote it" as "pass -incarnation" and
// presents a dead one, which is exactly the impersonation strict identity
// exists to stop. The fix was always `attach` (or `begin`), which the message
// never mentioned.
//
// The Reason is deliberately unchanged. It is the frozen identifier callers
// branch on, and a record-level law really did fire; what was wrong is the
// prose, so the prose is what this repairs. A read failure here leaves the
// original refusal alone — a worse message beats a message invented from a
// chain nobody could read.
func nameTheMissingAttach(h *home.Home, tenant, role string, err error) error {
	if org.RefusalReason(err) != org.ReasonIncarnationMissing {
		return err
	}
	records, readErr := h.Records(tenant, role)
	if readErr != nil {
		return err
	}
	if slices.ContainsFunc(records, func(r org.Record) bool { return r.Kind == org.KindAttach }) {
		return err
	}
	return &org.Refusal{Reason: org.ReasonIncarnationMissing, Seq: int64(len(records)) + 1, Detail: fmt.Sprintf(
		"%s has never been attached, so no incarnation exists to name — run org attach -role %s first (or org begin, which attaches for you). Do not present a digest from an earlier session",
		role, role)}
}

// reportSteps renders a composite's receipts: one line per record, or the
// receipt array under -json.
func reportSteps(e *env, s *scope, steps []receipt) error {
	if s.asJSON {
		return printJSON(e, map[string]any{"steps": steps})
	}
	for _, r := range steps {
		fmt.Fprintf(e.stdout, "%s seq %d %s (phase %s)\n", r.Kind, r.Seq, r.Digest, r.Phase)
	}
	return nil
}

// cmdBegin brackets the entry of small work: attach when the role is
// unheld, assign when the work is unassigned (only then requiring a pin),
// claim. The field report's §4.2 finding — seven bookkeeping writes around
// twenty minutes of work — answered without weakening anything: the same
// records land on the chain, one command writes them.
func cmdBegin(e *env, args []string) error {
	s := newScope("begin")
	work := s.fs.String("work", "", "work URI to begin")
	digest := s.fs.String("digest", "", "content digest pinning the work item (sha256:…), when it needs assigning")
	pin := s.fs.String("pin", "", "text to pin instead of -digest, when it needs assigning")
	due := s.fs.Duration("next-due", 0, "declare the next append deadline, e.g. 90m")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	if *work == "" {
		return fmt.Errorf("-work is required")
	}
	_, state, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	if held(state, *work) < 0 && *digest == "" && *pin == "" {
		return fmt.Errorf("-digest or -pin is required: %s is not yet assigned, and an unpinned assignment cannot detect drift", *work)
	}
	if err := claimable(state, *work); err != nil {
		return err
	}
	var steps []receipt
	inc := s.incarnation
	if state.Holder == "" {
		d := home.Draft{Kind: org.KindAttach}
		if *due > 0 {
			d.NextDue = time.Now().Add(*due)
		}
		r, st, err := step(h, s, d)
		if err != nil {
			return err
		}
		steps, state, inc = append(steps, r), st, st.Holder
	}
	if held(state, *work) < 0 {
		if *digest == "" {
			*digest = org.DigestBytes([]byte(*pin))
		}
		r, st, err := step(h, s, home.Draft{
			Kind:        org.KindAssign,
			Subject:     org.Subject{Work: *work, Digest: *digest},
			Incarnation: inc,
		})
		if err != nil {
			return err
		}
		steps, state = append(steps, r), st
	}
	r, _, err := step(h, s, home.Draft{
		Kind: org.KindClaim, Subject: org.Subject{Work: *work}, Incarnation: inc,
	})
	if err != nil {
		return err
	}
	return reportSteps(e, s, append(steps, r))
}

// cmdDone brackets the exit of finished work: claim when the item is held
// but unclaimed (§4.3's uncompletable trap — the claim is inferred, on the
// chain, instead of hand-reconstructed), complete, release. The kernel's
// no-claim law stands untouched; the composite writes the claim it implies.
func cmdDone(e *env, args []string) error {
	s := newScope("done")
	work := s.fs.String("work", "", "work URI to finish (optional when one is active or exactly one is held)")
	text := s.fs.String("body", "", `narrative body for the complete ("-" reads stdin)`)
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	b, err := body(e, *text)
	if err != nil {
		return err
	}
	_, state, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	target, err := doneTarget(state, *work)
	if err != nil {
		return err
	}
	var steps []receipt
	var inc string
	dangling := state.Dangling == target
	if state.Holder == "" {
		r, st, err := step(h, s, home.Draft{Kind: org.KindAttach})
		if err != nil {
			return err
		}
		steps, state, inc = append(steps, r), st, st.Holder
	}
	// A dangling claim is already open — the predecessor's. Completing it IS
	// the discharge the kernel demands; claiming first would be refused, and
	// would also misrepresent a successor's close as a fresh claim.
	if state.Active == "" && !dangling {
		r, st, err := step(h, s, home.Draft{Kind: org.KindClaim, Subject: org.Subject{Work: target}, Incarnation: inc})
		if err != nil {
			return err
		}
		steps, state = append(steps, r), st
	}
	r, state, err := step(h, s, home.Draft{
		Kind: org.KindComplete, Subject: org.Subject{Work: target}, Body: b,
		Incarnation: inc,
	})
	if err != nil {
		return err
	}
	steps = append(steps, r)
	if n := len(state.Held); n > 0 {
		fmt.Fprintf(e.stderr, "warning: %d other item(s) still held; they stay assigned across the release\n", n)
	}
	r, _, err = step(h, s, home.Draft{Kind: org.KindRelease, Incarnation: inc})
	if err != nil {
		return err
	}
	return reportSteps(e, s, append(steps, r))
}

// doneExplicit checks an explicitly named target against the record done
// would write next, and refuses with the reason the kernel would name for THAT
// record — not for a claim done never writes:
//
//   - another item is active: done skips the claim and writes `complete
//     <work>`, which checkTerminal refuses as claim_subject_mismatch;
//   - a predecessor's claim dangles on something else: done would claim
//     <work>, and checkClaim refuses every claim as dangling_claim before it
//     looks at what is held;
//   - otherwise the claim names work the role does not hold: work_not_held.
func doneExplicit(state org.RoleState, work string) (string, error) {
	if state.Active != "" && state.Active != work {
		return "", preflight(org.ReasonClaimSubjectMismatch,
			"done would complete %s but the active claim is %s; finish that first or name it explicitly", work, state.Active)
	}
	if state.Active == "" && state.Dangling != "" && state.Dangling != work {
		return "", preflight(org.ReasonDanglingClaim,
			"a predecessor's claim on %s is unresolved; finish it before finishing %s", state.Dangling, work)
	}
	if state.Active != work && state.Dangling != work && held(state, work) < 0 {
		return "", preflight(org.ReasonWorkNotHeld, "%s is not held by this role; nothing to finish", work)
	}
	return work, nil
}

// doneTarget resolves which work item done finishes: the explicit -work, the
// active claim, or the single held item — refusing to guess between several.
//
// Where a case mirrors a kernel law, it refuses with that law's reason (see
// preflight). Where it is genuinely the caller's mistake — an ambiguous target
// — it stays an error, because no law was broken.
func doneTarget(state org.RoleState, work string) (string, error) {
	if work != "" {
		return doneExplicit(state, work)
	}
	if state.Active != "" {
		return state.Active, nil
	}
	// A dangling claim outranks the held set: the kernel refuses every new
	// claim until it is discharged, so it is the only thing that CAN be
	// finished next, whatever else the role holds.
	if state.Dangling != "" {
		return state.Dangling, nil
	}
	if len(state.Held) == 1 {
		return state.Held[0].Work, nil
	}
	return "", fmt.Errorf("-work is required: %d items held, none active", len(state.Held))
}

// claimable reports why a claim on work would be refused, before any record is
// written. The kernel refuses these anyway; checking here keeps a composite
// from leaving a half-built entry (an attach, or an assign) behind a refusal
// that was knowable from state the caller already read.
func claimable(state org.RoleState, work string) error {
	if state.Dangling != "" {
		return preflight(org.ReasonDanglingClaim,
			"a predecessor's claim on %s is unresolved; finish it (org done -work %s) before beginning %s",
			state.Dangling, state.Dangling, work)
	}
	if len(state.OpenIntents) > 0 {
		return preflight(org.ReasonOpenIntent,
			"effect %s is still open; resolve it before beginning %s", state.OpenIntents[0], work)
	}
	if state.Active != "" && state.Active != work {
		return preflight(org.ReasonClaimActive,
			"%s is already active; finish or pause it before beginning %s", state.Active, work)
	}
	return nil
}

// preflight builds the refusal a composite's early check must return so that
// checking early is invisible to a caller.
//
// The seam this repairs is documented in the package comment: 1 is the kernel
// refusing a record, 4 is the command failing. A pre-flight check returning a
// bare error reports a legitimate refusal as a crash, and the refusals a
// composite catches early are the common ones — so a caller branching on
// 1-vs-4, which the doc invites, got the wrong answer most of the time.
//
// It names a reason from the frozen vocabulary, which is the property the
// kernel's unexported constructor protects; what it must never do is invent
// one, and it must name the SAME reason the kernel would have. Seq is 0: no
// record was drafted, so there is no chain position to point at.
func preflight(reason, format string, args ...any) error {
	return &org.Refusal{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// orDashText renders an empty string as a dash, so an absent value reads as
// absent rather than as a gap in the line.
func orDashText(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// held reports the index of a work URI in a state's held set, or -1.
func held(state org.RoleState, work string) int {
	for i, a := range state.Held {
		if a.Work == work {
			return i
		}
	}
	return -1
}

// appendAndReport applies the identity policy, appends one draft, and prints
// the receipt — text for a human, `-json` for a machine.
func appendAndReport(e *env, h *home.Home, s *scope, d home.Draft) error {
	if s.strict && s.incarnation == "" && !home.MintsIdentity(d.Kind) {
		return fmt.Errorf("strict mode: %s requires -incarnation (or ORG_INCARNATION); writing as the holder is disabled", d.Kind)
	}
	if d.Incarnation == "" {
		d.Incarnation = s.incarnation
	}
	r, state, err := h.Append(s.tenant, s.role, d)
	if err != nil {
		return nameTheMissingAttach(h, s.tenant, s.role, err)
	}
	digest, err := org.DigestOf(r)
	if err != nil {
		return err
	}
	if !s.asJSON {
		fmt.Fprintf(e.stdout, "%s seq %d %s (phase %s)\n", r.Kind, r.Seq, digest, state.Phase)
		return nil
	}
	return printJSON(e, receipt{
		Kind: r.Kind, Seq: r.Seq, Digest: digest,
		Phase: state.Phase, Tip: state.Tip, Holder: state.Holder,
		Active: state.Active, Dangling: state.Dangling,
		Held: len(state.Held), Fence: state.Fence,
	})
}

func printJSON(e *env, v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, string(out))
	return nil
}

// termsFlags registers the charter-terms flags on a scope and returns the
// builder that assembles them. It is its own function so the terms vocabulary
// lives in one place for the second writer this chain needs and does not yet
// safely have (see FOLLOWUPS: recharter).
func termsFlags(s *scope) func() *org.Terms {
	var scopes, supervisors, effects multi
	retire := s.fs.String("retire-when", "", "the condition under which this role retires")
	spend := s.fs.Int64("spend-ceiling", 0, "spend ceiling")
	cycles := s.fs.Int64("cycle-ceiling", 0, "review-cycle ceiling")
	concurrency := s.fs.Int64("concurrency-ceiling", 0, "concurrent incarnation ceiling")
	minReader := s.fs.Int64("min-reader", 1, "monotone minimum reader version")
	s.fs.Var(&scopes, "scope", "work reference this role owns (repeatable)")
	s.fs.Var(&supervisors, "supervisor", "role that may take this one over (repeatable)")
	s.fs.Var(&effects, "effect-class", "effect class this role may perform (repeatable)")
	return func() *org.Terms {
		return &org.Terms{
			Scope: scopes, Supervisors: supervisors,
			EffectClasses: effects, Retire: *retire,
			SpendCeiling: *spend, CycleCeiling: *cycles, ConcurrencyCeiling: *concurrency,
			MinReader: *minReader,
		}
	}
}

func cmdCharter(e *env, args []string) error {
	s := newScope("charter")
	terms := termsFlags(s)
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	return appendAndReport(e, h, s, home.Draft{Kind: org.KindCharter, Terms: terms()})
}

// cmdTransfer moves one work item between two roles in the same tenant.
//
// Nothing here is atomic: two chains, two locks, no cross-chain transaction.
// The design makes the failure states RECOVERABLE instead of pretending they
// cannot happen, which is the whole reason this verb exists rather than a
// documented two-command recipe:
//
//   - ASSIGN FIRST, then unassign. A crash between the two leaves the item
//     held twice — which `org sweep` reports as an assign_conflict — instead
//     of held by nobody, which nothing can see. A visible conflict is a
//     recoverable state; a silent orphan is lost work.
//   - Each APPEND is fenced to the tip it was decided from, so a chain that
//     moved under that decision refuses the write. This is per-append, not a
//     transaction over both chains: a resume performs no destination append
//     and therefore fences nothing there, and the destination re-check below
//     asserts the work is still held at the same digest — not that the
//     destination chain sat still. A destination record that leaves work and
//     digest intact passes, correctly, because nothing this verb depends on
//     changed.
//   - It is IDEMPOTENT by state, not by flag. Re-running after a crash reads
//     the same four cases and finishes the half-done move; re-running after
//     success is a no-op that says so.
//   - One window survives all of it: a destination that drops the work between
//     the re-check and the source's append orphans the item. Closing that
//     needs a cross-chain transaction the substrate does not have (FOLLOWUPS).
//
// It manufactures no authority: both roles must already be held, and the
// destination's writer must be presented. Minting an incarnation for a role
// the caller may not be entitled to hold is the same self-signing hole that
// keeps `recharter` unexposed (see FOLLOWUPS).
func cmdTransfer(e *env, args []string) error {
	s := newScope("transfer")
	work := s.fs.String("work", "", "work URI to move")
	to := s.fs.String("to", "", "destination role id")
	toInc := s.fs.String("to-incarnation", "", "the destination holder's incarnation (from its attach)")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	if *work == "" || *to == "" {
		return fmt.Errorf("-work and -to are required")
	}
	// Unconditionally, not only under -strict: with an empty incarnation the
	// home substitutes the DESTINATION's own holder, so omitting this writes
	// an assignment onto another role's chain over that role's signature. That
	// is the misattribution stepRole exists to prevent, and -strict is an
	// operator preference, not a security boundary.
	if *toInc == "" {
		return fmt.Errorf("-to-incarnation is required: writing to %s's chain means presenting %s's writer identity, never borrowing it", *to, *to)
	}
	if *to == s.role {
		return fmt.Errorf("-to %s is the source role; a transfer moves work between two roles", *to)
	}
	_, src, err := h.Load(s.tenant, s.role)
	if err != nil {
		return fmt.Errorf("source %s: %w", s.role, err)
	}
	_, dst, err := h.Load(s.tenant, *to)
	if err != nil {
		return fmt.Errorf("destination %s: %w", *to, err)
	}

	srcHolds, dstHolds := held(src, *work) >= 0, held(dst, *work) >= 0
	if !srcHolds && !dstHolds {
		return fmt.Errorf("neither %s nor %s holds %s; there is nothing to transfer", s.role, *to, *work)
	}
	if !srcHolds && dstHolds {
		return reportNoOp(e, s, fmt.Sprintf("already transferred: %s holds %s", *to, *work))
	}
	if err := transferable(src, dst, s.role, *to, *work); err != nil {
		return err
	}
	pin := src.Held[held(src, *work)].Digest
	// Both holding is only a RESUME if they hold the same thing. Different
	// digests mean this is a pre-existing conflict over one URI, not a
	// half-finished move — and unassigning the source there would silently
	// bless a pin nobody transferred.
	if dstHolds {
		if got := dst.Held[held(dst, *work)].Digest; got != pin {
			return fmt.Errorf("%s already holds %s pinned to %s, not the %s this transfer would move: that is an assign_conflict to resolve, not a half-finished transfer",
				*to, *work, shortDigest(got), shortDigest(pin))
		}
	}
	// The destination inherits the SOURCE's pin, resolved above: the digest is
	// what makes drift detectable, so re-pinning here would quietly reset the
	// baseline the assignment was made against.
	var steps []receipt
	if !dstHolds {
		r, _, err := stepRole(h, s, *to, home.Draft{
			Kind:      org.KindAssign,
			Subject:   org.Subject{Work: *work, Digest: pin},
			ExpectTip: dst.Tip, Incarnation: *toInc,
		})
		if err != nil {
			return fmt.Errorf("assign to %s (nothing was moved): %w", *to, err)
		}
		steps = append(steps, r)
	}
	// Re-read the destination immediately before removing the source. The
	// per-append fence protects each write against ITS own chain moving; it
	// cannot protect the source's unassign against the DESTINATION changing,
	// and a destination that dropped the work between the two appends would
	// leave the item held by nobody — the silent orphan this ordering exists
	// to prevent. This narrows that window to the gap between this read and
	// the append below; closing it entirely needs a cross-chain transaction
	// the substrate does not have (FOLLOWUPS).
	if err := stillHolds(h, s.tenant, *to, *work, pin); err != nil {
		return fmt.Errorf("%w; the source keeps %s, so nothing is orphaned — re-run once the destination is settled", err, *work)
	}
	r, _, err := step(h, s, home.Draft{
		Kind:      org.KindUnassign,
		Subject:   org.Subject{Work: *work},
		ExpectTip: src.Tip,
	})
	if err != nil {
		return fmt.Errorf("unassign from %s — %s now holds %s TWICE, which org sweep reports as an assign_conflict; re-run this command to finish: %w", s.role, *to, *work, err)
	}
	steps = append(steps, r)
	if _, ok := org.MatchScope(dst.Terms.Scope, *work); !ok {
		fmt.Fprintf(e.stderr, "warning: %s is outside %s's charter scope %v; org sweep will report it as scope_drift\n",
			*work, *to, dst.Terms.Scope)
	}
	return reportSteps(e, s, steps)
}

// stillHolds re-reads a role and reports whether it holds work at the expected
// pin. It exists for the moment between a transfer's two appends, where the
// only honest answer to "did the destination keep it?" is to look again.
func stillHolds(h *home.Home, tenant, role, work, pin string) error {
	_, state, err := h.Load(tenant, role)
	if err != nil {
		return fmt.Errorf("re-read %s: %w", role, err)
	}
	i := held(state, work)
	if i < 0 {
		return fmt.Errorf("%s no longer holds %s", role, work)
	}
	if got := state.Held[i].Digest; got != pin {
		return fmt.Errorf("%s holds %s pinned to %s, not the %s just assigned", role, work, shortDigest(got), shortDigest(pin))
	}
	return nil
}

// reportNoOp renders a decided-nothing-to-do outcome. A machine caller that
// retries after an uncertain response must be able to decode the success it
// gets back, so -json returns the composite envelope with no steps rather than
// a line of prose.
func reportNoOp(e *env, s *scope, why string) error {
	if s.asJSON {
		return printJSON(e, map[string]any{"steps": []receipt{}, "note": why})
	}
	fmt.Fprintln(e.stdout, why)
	return nil
}

// shortDigest trims a digest for a message; the full pair buries the two
// values a reader is comparing in 128 characters of noise.
func shortDigest(d string) string {
	if len(d) <= 14 {
		return d
	}
	return d[:14] + "…"
}

// transferable reports why a transfer cannot proceed, before either chain is
// written. Both roles must be held: this verb moves work between two writers,
// and it will not mint either one.
func transferable(src, dst org.RoleState, srcRole, dstRole, work string) error {
	if src.Holder == "" {
		return fmt.Errorf("%s is not held; attach it before transferring out of it (org attach -role %s)", srcRole, srcRole)
	}
	if dst.Holder == "" {
		return fmt.Errorf("%s is not held; attach it before transferring into it (org attach -role %s), then pass -to-incarnation", dstRole, dstRole)
	}
	if src.Active == work {
		return fmt.Errorf("%s is the active claim on %s; end it (yield or done) before transferring it", work, srcRole)
	}
	if src.Dangling != "" {
		return fmt.Errorf("%s has an unresolved claim on %s; discharge it before transferring anything", srcRole, src.Dangling)
	}
	return nil
}

// cmdAnnul repudiates the record at the tip. It does NOT revert it: the fold
// appends the digest to Annulled and leaves Terms, Held, Active and NextDue
// exactly as that record left them, because an append-only chain corrects
// forward. So this verb records the repudiation and says plainly which effect
// is still standing — a caller who reads "annul" as "undo" would otherwise
// walk away believing state changed.
//
// The kernel admits an annul only against the tip, because a fold can verify
// nothing else, so -target defaults to the tip rather than asking the caller
// to copy a digest correctly.
func cmdAnnul(e *env, args []string) error {
	s := newScope("annul")
	target := s.fs.String("target", "", "digest of the record to repudiate (default: the tip)")
	text := s.fs.String("body", "", `why it is repudiated ("-" reads stdin)`)
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	b, err := body(e, *text)
	if err != nil {
		return err
	}
	_, state, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	if *target == "" {
		*target = state.Tip
	}
	fmt.Fprintf(e.stderr, "annul records a repudiation; it does not revert the record's effect\n")
	fmt.Fprintf(e.stderr, "  still standing: phase %s · held %d · active %s · next-due %s\n",
		state.Phase, len(state.Held), orDashText(state.Active), orDashText(state.NextDue))
	fmt.Fprintf(e.stderr, "  correct it forward with the verb that undoes it (unassign, yield, a new assign)\n")
	return appendAndReport(e, h, s, home.Draft{
		Kind: org.KindAnnul, Subject: org.Subject{Target: *target}, Body: b,
	})
}

func cmdAttach(e *env, args []string) error {
	s := newScope("attach")
	due := s.fs.Duration("next-due", 0, "declare the next append deadline, e.g. 90m")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	d := home.Draft{Kind: org.KindAttach}
	if *due > 0 {
		d.NextDue = time.Now().Add(*due)
	}
	r, state, err := h.Append(s.tenant, s.role, d)
	if err != nil {
		return err
	}
	if s.asJSON {
		digest, err := org.DigestOf(r)
		if err != nil {
			return err
		}
		return printJSON(e, receipt{
			Kind: r.Kind, Seq: r.Seq, Digest: digest, Phase: state.Phase,
			Tip: state.Tip, Holder: state.Holder, Active: state.Active,
			Dangling: state.Dangling, Held: len(state.Held), Fence: state.Fence,
		})
	}
	fmt.Fprintf(e.stdout, "attached: incarnation %s seq %d (phase %s)\n", state.Holder, r.Seq, state.Phase)
	return nil
}

func cmdAssign(e *env, args []string) error {
	s := newScope("assign")
	work := s.fs.String("work", "", "work URI, e.g. dossier:org/p1/t3 or github:owner/repo#88")
	digest := s.fs.String("digest", "", "content digest pinning the work item (sha256:…)")
	pin := s.fs.String("pin", "", "text to pin instead of -digest; its sha256 becomes the digest")
	party := s.fs.String("party", "", "assignee role, empty when self-assigned")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	if *digest == "" && *pin == "" {
		return fmt.Errorf("-digest or -pin is required: an unpinned assignment cannot detect drift")
	}
	if *digest == "" {
		*digest = org.DigestBytes([]byte(*pin))
	}
	return appendAndReport(e, h, s, home.Draft{
		Kind:    org.KindAssign,
		Subject: org.Subject{Work: *work, Digest: *digest, Party: *party},
	})
}

// cmdWork covers the kinds whose subject is one work URI: claim and its
// terminals, and unassign. Terminals accept an optional narrative body.
func cmdWork(kind string) func(*env, []string) error {
	return func(e *env, args []string) error {
		s := newScope(kind)
		work := s.fs.String("work", "", "work URI")
		text := s.fs.String("body", "", `narrative body ("-" reads stdin)`)
		due := s.fs.Duration("next-due", 0, "declare the next append deadline")
		h, err := s.open(args, true)
		if err != nil {
			return err
		}
		if *work == "" {
			return fmt.Errorf("-work is required")
		}
		b, err := body(e, *text)
		if err != nil {
			return err
		}
		d := home.Draft{Kind: kind, Subject: org.Subject{Work: *work}, Body: b}
		if *due > 0 {
			d.NextDue = time.Now().Add(*due)
		}
		return appendAndReport(e, h, s, d)
	}
}

// cmdBare covers the kinds with no subject: release, retire, seal, escalation.
func cmdBare(kind string) func(*env, []string) error {
	return func(e *env, args []string) error {
		s := newScope(kind)
		text := s.fs.String("body", "", `narrative body ("-" reads stdin)`)
		h, err := s.open(args, true)
		if err != nil {
			return err
		}
		b, err := body(e, *text)
		if err != nil {
			return err
		}
		return appendAndReport(e, h, s, home.Draft{Kind: kind, Body: b})
	}
}

// cmdParty covers the kinds whose subject is another role: takeover, revoke,
// delegate.
func cmdParty(kind string) func(*env, []string) error {
	return func(e *env, args []string) error {
		s := newScope(kind)
		party := s.fs.String("party", "", "the other role: supervisor on takeover, child on delegate")
		h, err := s.open(args, true)
		if err != nil {
			return err
		}
		return appendAndReport(e, h, s, home.Draft{Kind: kind, Subject: org.Subject{Party: *party}})
	}
}

func cmdIntent(e *env, args []string) error {
	s := newScope("intent")
	effect := s.fs.String("effect", "", "effect id being opened")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	if *effect == "" {
		return fmt.Errorf("-effect is required")
	}
	return appendAndReport(e, h, s, home.Draft{Kind: org.KindIntentRef, Subject: org.Subject{Effect: *effect}})
}

func cmdResolve(e *env, args []string) error {
	s := newScope("resolve")
	effect := s.fs.String("effect", "", "open effect id to close")
	target := s.fs.String("target", "", "open escalation digest to close")
	text := s.fs.String("body", "", `narrative body ("-" reads stdin)`)
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	b, err := body(e, *text)
	if err != nil {
		return err
	}
	return appendAndReport(e, h, s, home.Draft{
		Kind: org.KindResolution, Subject: org.Subject{Effect: *effect, Target: *target}, Body: b,
	})
}

// cmdAdvisory covers narrative kinds. Body is required: an advisory record
// with nothing to say is not worth a chain position.
func cmdAdvisory(kind string) func(*env, []string) error {
	return func(e *env, args []string) error {
		s := newScope(kind)
		text := s.fs.String("body", "", `the narrative ("-" reads stdin)`)
		class := s.fs.String("body-class", "narrative", "blob retention class")
		due := s.fs.Duration("next-due", 0, "declare the next append deadline")
		h, err := s.open(args, true)
		if err != nil {
			return err
		}
		b, err := body(e, *text)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return fmt.Errorf("-body is required for %s", kind)
		}
		d := home.Draft{Kind: kind, Body: b, BodyClass: *class}
		if *due > 0 {
			d.NextDue = time.Now().Add(*due)
		}
		return appendAndReport(e, h, s, d)
	}
}

func cmdBoot(e *env, args []string) error {
	s := newScope("boot")
	budget := s.fs.Int("max-bytes", 2048, "boot-index byte budget; depth is shed to fit")
	ctxBudget := s.fs.Int("context-bytes", 4096, "byte budget for operator context.d sources")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	records, state, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	if state.Phase == org.PhaseVoid {
		return fmt.Errorf("no chain for %s/%s under %s", s.tenant, s.role, h.Root())
	}
	b, err := render.NewBoot(state, records, h, time.Now())
	if err != nil {
		return err
	}
	files, err := h.Context(s.tenant, s.role)
	if err != nil {
		return err
	}
	if s.asJSON {
		return printJSON(e, bootJSON(b, files))
	}
	fmt.Fprint(e.stdout, b.Text(*budget))
	fmt.Fprint(e.stdout, contextText(files, *ctxBudget, h.ContextDir(s.tenant, s.role)))
	return nil
}

// bootJSON pairs the boot index with the operator context for machine callers.
func bootJSON(b render.Boot, files []home.ContextFile) map[string]any {
	ctx := make([]map[string]string, 0, len(files))
	for _, f := range files {
		ctx = append(ctx, map[string]string{"name": f.Name, "body": string(f.Body)})
	}
	return map[string]any{"boot": b, "context": ctx}
}

// contextText renders the operator's context.d sources under one budget,
// naming the directory when it truncates so the full files stay one read away.
func contextText(files []home.ContextFile, budget int, dir string) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## operator context (context.d)\n")
	spent := 0
	for _, f := range files {
		entry := fmt.Sprintf("### %s\n%s\n", f.Name, strings.TrimSpace(string(f.Body)))
		if spent+len(entry) > budget {
			fmt.Fprintf(&sb, "… context truncated at %d bytes — read the rest in %s\n", budget, dir)
			break
		}
		sb.WriteString(entry)
		spent += len(entry)
	}
	return sb.String()
}

// cmdIntake answers "where does this work belong" before anything is written:
// which chartered lanes' scopes cover the URI (contracts/org.InScope), which
// lanes already hold it — in or out of scope — and, when nothing covers it,
// says so with the fix. Read-only, so it is safe as the reflex before assign.
func cmdIntake(e *env, args []string) error {
	s := newScope("intake")
	work := s.fs.String("work", "", "work URI to route, e.g. github:owner/repo#88")
	h, err := s.open(args, false)
	if err != nil {
		return err
	}
	if *work == "" {
		return fmt.Errorf("-work is required")
	}
	if !org.ValidWorkURI(*work) {
		return fmt.Errorf("-work %q is not a valid work URI (scheme:reference); the kernel could never record it", *work)
	}
	pairs, err := h.RolesForTenant(s.tenant)
	if err != nil {
		return err
	}
	in := render.Intake{Work: *work, Tenant: s.tenant}
	for _, p := range pairs {
		lane, keep := intakeLane(h, p[0], p[1], *work)
		if !keep {
			continue
		}
		in.Lanes = append(in.Lanes, lane)
		if lane.ScopeMatch != "" {
			in.Covered = true
		}
	}
	in.Resolve()
	if s.asJSON {
		return printJSON(e, in)
	}
	fmt.Fprint(e.stdout, render.IntakeText(in))
	return nil
}

// intakeLane judges one role against the work URI. An unreadable chain is
// kept — it might cover the work, and saying "cannot judge" beats omitting it.
func intakeLane(h *home.Home, tenant, role, work string) (render.IntakeLane, bool) {
	lane := render.IntakeLane{Role: role}
	_, state, err := h.Load(tenant, role)
	if err != nil {
		lane.Err = err.Error()
		return lane, true
	}
	lane.Phase = state.Phase
	if state.Phase == org.PhaseVoid || state.Phase == org.PhaseRetired {
		return lane, false
	}
	lane.ScopeMatch, _ = org.MatchScope(state.Terms.Scope, work)
	for _, a := range state.Held {
		if a.Work == work {
			lane.Holds = true
		}
	}
	return lane, lane.ScopeMatch != "" || lane.Holds
}

func cmdStatus(e *env, args []string) error {
	s := newScope("status")
	h, err := s.open(args, false)
	if err != nil {
		return err
	}
	pairs, err := h.Roles()
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		fmt.Fprintln(e.stdout, "no roles chartered")
		return nil
	}
	rows := make([]render.Row, 0, len(pairs))
	for _, p := range pairs {
		_, state, err := h.Load(p[0], p[1])
		if err != nil {
			return err
		}
		rows = append(rows, render.NewRow(state, time.Now()))
	}
	if s.asJSON {
		return printJSON(e, rows)
	}
	fmt.Fprint(e.stdout, render.Board(rows))
	return nil
}

// cmdSweep is the continuity instrument: it replays every chain in the selected
// tenant and reports what the substrate is a bet on — whether sessions leave
// distilled conclusions, and whether inherited obligations get discharged.
func cmdSweep(e *env, args []string) error {
	s := newScope("sweep")
	h, err := s.open(args, false)
	if err != nil {
		return err
	}
	pairs, err := h.RolesForTenant(s.tenant)
	if err != nil {
		return err
	}
	now := time.Now()
	roles := make([]survey.Role, 0, len(pairs))
	for _, p := range pairs {
		// Records, not Load: Load folds internally and is all-or-nothing, so a
		// chain that stops folding would arrive here empty and the sweep would
		// report zero of the work recorded before the break. The replay in
		// survey.Of is what decides admissibility, and it keeps what it counted.
		records, err := h.Records(p[0], p[1])
		row := survey.Of(p[0], p[1], records, now)
		if err != nil && row.Err == "" {
			row.Err = err.Error()
		}
		roles = append(roles, row)
	}
	totals := survey.Sum(roles)
	conflicts := survey.AssignConflicts(s.tenant, roles)
	drifts := survey.ScopeDrifts(s.tenant, roles)
	if s.asJSON {
		return printJSON(e, map[string]any{
			"roles": roles, "totals": totals,
			"assign_conflicts": conflicts, "scope_drift": drifts,
		})
	}
	fmt.Fprint(e.stdout, render.Sweep(roles, totals, conflicts, drifts))
	return nil
}

func cmdLog(e *env, args []string) error {
	s := newScope("log")
	n := s.fs.Int("n", 20, "records to show, newest last")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	records, _, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	start := max(0, len(records)-*n)
	for _, r := range records[start:] {
		subject := strings.TrimSpace(strings.Join([]string{r.Subject.Work, r.Subject.Party, r.Subject.Effect}, " "))
		fmt.Fprintf(e.stdout, "%4d  %-11s  %-10s  %s\n", r.Seq, r.Kind, r.At, subject)
	}
	return nil
}

func cmdVerify(e *env, args []string) error {
	s := newScope("verify")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	records, state, err := h.Load(s.tenant, s.role)
	if err != nil {
		return err
	}
	if s.asJSON {
		return printJSON(e, map[string]any{
			"ok": true, "records": len(records), "phase": state.Phase, "tip": state.Tip,
		})
	}
	fmt.Fprintf(e.stdout, "ok: %d records fold to phase %s, tip %s\n", len(records), state.Phase, state.Tip)
	return nil
}

// cmdBlob reads one body out of the store. It is the only verb taking a
// positional argument, and that makes it the only one where a trailing flag is
// silently dropped: Go's flag package stops parsing at the first non-flag
// argument, so `org blob <digest> -state /tmp/x` reads the DEFAULT state root
// and reports the blob missing from a home the caller never named. Refusing the
// leftovers turns a wrong answer into a usage error, and -digest gives the
// order-independent form.
func cmdBlob(e *env, args []string) error {
	s := newScope("blob")
	digest := s.fs.String("digest", "", "blob digest to read (sha256:…); the same value may be passed positionally, but only AFTER every flag")
	h, err := s.open(args, false)
	if err != nil {
		return err
	}
	// Both forms at once is the same wrong-answer shape as a dropped flag:
	// `-digest A B` would read A and silently ignore B. Refuse it.
	if *digest != "" && s.fs.NArg() > 0 {
		return fmt.Errorf("%q was passed positionally alongside -digest; use one form or the other", s.fs.Arg(0))
	}
	if *digest == "" {
		*digest = s.fs.Arg(0)
	}
	if *digest == "" {
		return fmt.Errorf("usage: org blob -digest <sha256:…>  (or: org blob [flags] <sha256:…>)")
	}
	if n := s.fs.NArg(); n > 1 {
		return fmt.Errorf("%q follows a positional argument and was not parsed as a flag; put every flag before the digest, or use -digest", s.fs.Arg(1))
	}
	bodyBytes, found, err := h.Blob(*digest)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("blob %s is erased or unknown", *digest)
	}
	_, err = e.stdout.Write(bodyBytes)
	return err
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
