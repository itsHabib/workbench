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
	"attach":     cmdAttach,
	"assign":     cmdAssign,
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
work        assign · unassign · claim · yield · complete · abandon
composite   begin (attach+assign+claim) · done (claim?+complete+release)
obligations intent · resolve · escalate · seal
narrative   note · mark · checkpoint · report · message   (-body "…" | -body -)
read        boot · intake · status · sweep · log · verify · blob

every verb: -state <dir> (or ORG_STATE) · -tenant <id> (or ORG_TENANT) · -role <id>`)
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
	_, state, _ := h.Load(s.tenant, s.role)
	if n := len(state.Held); n > 0 {
		fmt.Fprintf(e.stderr, "warning: releasing while still holding %d item(s); finished work exits via complete (or done), yield means pausing\n", n)
	}
	return appendAndReport(e, h, s, home.Draft{Kind: org.KindRelease, Body: b})
}

// step appends one record inside a composite verb, honoring strict identity
// exactly as appendAndReport does, and returns the receipt for the roll-up.
func step(h *home.Home, s *scope, d home.Draft) (receipt, org.RoleState, error) {
	if d.Incarnation == "" {
		d.Incarnation = s.incarnation
	}
	if s.strict && d.Incarnation == "" && !home.MintsIdentity(d.Kind) {
		return receipt{}, org.RoleState{}, fmt.Errorf("strict mode: %s requires -incarnation (or ORG_INCARNATION); writing as the holder is disabled", d.Kind)
	}
	r, state, err := h.Append(s.tenant, s.role, d)
	if err != nil {
		return receipt{}, state, err
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
		if *digest == "" && *pin == "" {
			return fmt.Errorf("-digest or -pin is required: %s is not yet assigned, and an unpinned assignment cannot detect drift", *work)
		}
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
	if state.Holder == "" {
		r, st, err := step(h, s, home.Draft{Kind: org.KindAttach})
		if err != nil {
			return err
		}
		steps, state, inc = append(steps, r), st, st.Holder
	}
	if state.Active == "" {
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

// doneTarget resolves which work item done finishes: the explicit -work, the
// active claim, or the single held item — refusing to guess between several.
func doneTarget(state org.RoleState, work string) (string, error) {
	if work != "" {
		if state.Active != "" && state.Active != work {
			return "", fmt.Errorf("%s is active; finish it or name it explicitly before finishing %s", state.Active, work)
		}
		return work, nil
	}
	if state.Active != "" {
		return state.Active, nil
	}
	if len(state.Held) == 1 {
		return state.Held[0].Work, nil
	}
	return "", fmt.Errorf("-work is required: %d items held, none active", len(state.Held))
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
		return err
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

func cmdCharter(e *env, args []string) error {
	s := newScope("charter")
	var scopes, supervisors, effects multi
	tier := s.fs.String("tier", "", "risk tier ceiling, e.g. T2")
	retire := s.fs.String("retire-when", "", "the condition under which this role retires")
	spend := s.fs.Int64("spend-ceiling", 0, "spend ceiling")
	cycles := s.fs.Int64("cycle-ceiling", 0, "review-cycle ceiling")
	concurrency := s.fs.Int64("concurrency-ceiling", 0, "concurrent incarnation ceiling")
	s.fs.Var(&scopes, "scope", "work reference this role owns (repeatable)")
	s.fs.Var(&supervisors, "supervisor", "role that may take this one over (repeatable)")
	s.fs.Var(&effects, "effect-class", "effect class this role may perform (repeatable)")
	h, err := s.open(args, true)
	if err != nil {
		return err
	}
	return appendAndReport(e, h, s, home.Draft{
		Kind: org.KindCharter,
		Terms: &org.Terms{
			Scope: scopes, Tier: *tier, Supervisors: supervisors,
			EffectClasses: effects, Retire: *retire,
			SpendCeiling: *spend, CycleCeiling: *cycles, ConcurrencyCeiling: *concurrency,
			MinReader: 1,
		},
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
	if state.Phase == org.PhaseRetired {
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
	if s.asJSON {
		return printJSON(e, map[string]any{
			"roles": roles, "totals": totals, "assign_conflicts": conflicts,
		})
	}
	fmt.Fprint(e.stdout, render.Sweep(roles, totals, conflicts))
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

func cmdBlob(e *env, args []string) error {
	s := newScope("blob")
	h, err := s.open(args, false)
	if err != nil {
		return err
	}
	if s.fs.Arg(0) == "" {
		return fmt.Errorf("usage: org blob <digest>")
	}
	bodyBytes, found, err := h.Blob(s.fs.Arg(0))
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("blob %s is erased or unknown", s.fs.Arg(0))
	}
	_, err = e.stdout.Write(bodyBytes)
	return err
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
