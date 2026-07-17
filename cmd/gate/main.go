// Command gate decides whether a pull request may merge. One invocation
// gathers real evidence, runs the verifier ladder over it, composes the
// verdicts monotonically, checks the result against a signed grant, and
// records the outcome — every step an artifact in an append-only,
// hash-chained log, so the decision chain is reconstructable from state
// alone. All inter-package coupling is artifact ids; policy lives in grants
// and the reducer's law, not prose.
//
// Exit codes: 0 pass, 1 blocked, 2 escalated (parked for judgment),
// 3 capability refused, 4 error. See the code* constants for the full
// driver contract.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// The exit-code contract — the one surface the driver branches on. gate owns
// this code space: nothing else may emit one of these codes, or the driver
// misreads a malformed run as a decision. Flag parse errors and panics route
// to codeError — flagsets use ContinueOnError because ExitOnError's os.Exit(2)
// would collide with codeParked, making a typo read as a park — and each code
// must agree with the JSON outcome on stdout: a bare code with no matching
// outcome is an aborted run, not a decision.
//
//	code | outcome                                | driver action
//	  0  | would_merge / merged / already_merged  | land the PR
//	  1  | blocked                                | stop; do not merge
//	  2  | parked_for_judgment                    | re-mint a wider grant (ceiling) or judge (escalation)
//	  3  | capability_refused                     | mint/repair the grant, retry once
//	  4  | (hard error, no outcome)               | surface the error; no merge
const (
	codeMerge   = 0
	codeBlocked = 1
	codeParked  = 2
	codeRefused = 3
	codeError   = 4
)

// parkCodeCountUnreadable marks an escalation parked because the cycle count
// could not be derived. Authorization parks carry a machine-readable code so
// the cycle count can exclude them structurally, never by prose matching;
// the ceiling parks reuse the capability package's coded error strings.
const parkCodeCountUnreadable = "cycle_count_unreadable"

// errLogTampered fires when the state log fails its integrity check at the
// point the cycle count is derived. It is a hard fault (codeError), not a
// park: a rewritten log is corruption, not a judgment call, and the count
// derived from it cannot be trusted to bound the cap.
var errLogTampered = errors.New("log_integrity_failed")

// parseFlags parses a subcommand's flags under ContinueOnError. A help request
// (-h/-help) is a clean success, not an error — the flag package has already
// printed usage, so the caller returns nil and main exits 0. Any other parse
// error routes to codeError via main.
func parseFlags(fs *flag.FlagSet, args []string) (help bool, err error) {
	err = fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("parse flags: %w", err)
	}
	return false, nil
}

func main() {
	// Panics must not escape as bare runtime exit codes (which gate does not
	// own): route every one to codeError.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		fmt.Fprintln(os.Stderr, "gate: panic:", r)
		os.Exit(codeError)
	}()
	if len(os.Args) < 2 {
		usage()
		os.Exit(codeError)
	}
	var err error
	switch os.Args[1] {
	case "grant":
		err = cmdGrant(os.Args[2:])
	case "gate":
		err = cmdGate(os.Args[2:])
	case "judge":
		err = cmdJudge(os.Args[2:])
	case "explain":
		err = cmdExplain(os.Args[2:])
	case "audit":
		err = cmdAudit(os.Args[2:])
	case "backtest":
		err = cmdBacktest(os.Args[2:])
	case "stress":
		err = cmdStress(os.Args[2:])
	default:
		usage()
		os.Exit(codeError)
	}
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "gate:", err)
	os.Exit(codeError)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: gate <grant|gate|judge|explain|audit|backtest|stress> [flags]
  common   [-state state] [-key DIR] [-floor path]  (-key holds the signing + anchor keys, outside -state)
  grant    -repo R [-action merge] [-max-tier T1] [-max-cycles 3] [-ttl 24h]
  gate     -repo R -pr N -grant grt_x [-live]
  judge    -run run_x -grant grt_x (-decision pass|block -why "..." | -auto)
  explain  -run run_x [-json]
  audit
  backtest -repo R -prs 174,175,...
  stress   [-n 50] [-tag w]`)
}

type env struct {
	st       *state.Store
	stateDir string
	keyPath  string
	floorBin string
}

func newEnv(stateDir, floorBin, keyDir string) (env, error) {
	if keyDir == "" {
		keyDir = defaultKeyDir()
	}
	// Key custody only means anything if the key dir is a trust domain distinct
	// from the state dir. A key dir equal to or nested under it silently
	// restores the co-location this design removes — refuse it rather than
	// pretend the keys are protected.
	within, err := dirWithin(keyDir, stateDir)
	if err != nil {
		return env{}, err
	}
	if within {
		return env{}, fmt.Errorf("gate: key dir %q must be outside state dir %q", keyDir, stateDir)
	}
	// The signing key and the anchor key live OUTSIDE the state dir on purpose:
	// an actor who can write log.jsonl must not thereby be able to read or
	// forge the key that signs grants and anchors the chain. Same reason the
	// anchor record itself lives here, not beside the log it pins.
	keyPath := filepath.Join(keyDir, "grant.key")
	anchorKeyPath := filepath.Join(keyDir, "anchor.key")
	// The anchor record is per-state-dir: a shared key dir must not alias one
	// anchor across independent logs, or appending to one would falsely fail
	// the other's audit. The key stays shared (a secret can't be forged
	// regardless); only the record is namespaced by which log it pins.
	anchorPath := filepath.Join(keyDir, "anchor-"+stateDirTag(stateDir)+".json")
	st, err := state.OpenAnchored(stateDir, time.Now, anchorPath, anchorKeyPath)
	if err != nil {
		return env{}, err
	}
	if floorBin == "" {
		floorBin = defaultFloorBin()
	}
	return env{st: st, stateDir: stateDir, keyPath: keyPath, floorBin: floorBin}, nil
}

// stateDirTag is a stable, filesystem-safe tag for a state dir, so its anchor
// record is distinct from every other state dir sharing the key dir. The
// absolute path is hashed so two spellings of the same dir map to one anchor.
func stateDirTag(stateDir string) string {
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		abs = stateDir
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return hex.EncodeToString(sum[:8])
}

// dirWithin reports whether sub is the same directory as base or nested under
// it, comparing cleaned absolute paths so spelling, ".", and ".." resolve
// before the check. Used to refuse a key dir that would sit inside the state
// dir it is meant to be a separate trust domain from.
func dirWithin(sub, base string) (bool, error) {
	absSub, err := filepath.Abs(sub)
	if err != nil {
		return false, fmt.Errorf("gate: resolve key dir %q: %w", sub, err)
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false, fmt.Errorf("gate: resolve state dir %q: %w", base, err)
	}
	rel, err := filepath.Rel(absBase, absSub)
	if err != nil {
		// Different volumes have no relative path — genuinely outside.
		return false, nil
	}
	if rel == "." {
		return true, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// defaultKeyDir puts key custody under the user config dir — a trust domain
// distinct from the state dir. Falls back to a sibling of the working dir when
// no config home is resolvable, still keeping keys out of the state tree.
func defaultKeyDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ".gate-keys"
	}
	return filepath.Join(cfg, "gate")
}

// defaultFloorBin finds the triage floor binary in its sibling-checkout home,
// falling back to PATH.
func defaultFloorBin() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "triage-floor"
	}
	p := filepath.Join(home, "pers", "triage", "bin", "triage-floor.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "triage-floor"
}

func commonFlags(fs *flag.FlagSet) (stateDir, floorBin, keyDir *string) {
	stateDir = fs.String("state", "state", "state directory (the substrate)")
	floorBin = fs.String("floor", "", "path to triage-floor binary")
	keyDir = fs.String("key", "", "key custody dir for the signing + anchor keys (default: user config dir; must be outside -state)")
	return
}

func cmdGrant(args []string) error {
	fs := flag.NewFlagSet("grant", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	repo := fs.String("repo", "", "owner/repo the grant is scoped to")
	action := fs.String("action", "merge", "action the grant authorizes")
	maxTier := fs.String("max-tier", "T1", "highest risk tier the grant may auto-land")
	// 3 is the canonical review-cycle policy — an opinion, not a knob. The
	// field's zero value stays unbounded for grants that never set it; only the
	// CLI default is opinionated. 0 mints an explicitly unbounded grant.
	maxCycles := fs.Int("max-cycles", 3, "review-cycle ceiling the grant may consume (0 = unbounded)")
	ttl := fs.Duration("ttl", 24*time.Hour, "grant lifetime")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *repo == "" {
		return errors.New("grant: -repo required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	a, err := capability.Mint(e.st, e.keyPath, *repo, *action, *maxTier, *maxCycles, "operator", *ttl, time.Now)
	if err != nil {
		return err
	}
	fmt.Println(a.ID)
	return nil
}

type gateResult struct {
	Run      string `json:"run"`
	PR       string `json:"pr"`
	Decision string `json:"decision"`
	Tier     string `json:"tier"`
	Outcome  string `json:"outcome"`
	Why      string `json:"why"`
	Action   string `json:"action,omitempty"`
	// HeadSHA is the head commit gate actually read and judged (the live
	// headRefOid from evidence, carried on the reduced verdict). A caller that
	// posts an out-of-band verdict — a commit status branch protection consumes
	// — MUST bind that status to this SHA, not to a SHA it captured earlier: the
	// live head can move under a run, and a verdict is only valid for the head
	// it was computed against. Empty on paths that finalize before evidence
	// (a pre-evidence capability refusal, a hard error with no verdict).
	HeadSHA string `json:"head_sha"`
}

func cmdGate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "PR number")
	grantID := fs.String("grant", "", "grant artifact id")
	live := fs.Bool("live", false, "actually merge instead of dry-run")
	modelBackend := fs.String("model-backend", "local", "model backend for advisory rungs: local|cloud")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *repo == "" || *pr == 0 || *grantID == "" {
		return errors.New("gate: -repo, -pr, -grant required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	res, code, err := runGate(e, *repo, *pr, *grantID, *live, *modelBackend)
	if err != nil {
		return err
	}
	printJSON(res)
	os.Exit(code)
	return nil
}

// runGate is one thin vertical pass: capability, evidence, verification,
// reduction, outcome.
func runGate(e env, repo string, pr int, grantID string, live bool, modelBackend string) (gateResult, int, error) {
	subject := verify.Subject{Repo: repo, Number: pr}
	res := gateResult{PR: fmt.Sprintf("%s#%d", repo, pr)}

	// No live grant, no gate: coded refusal, exit 3, nothing gathered. This
	// precedes model construction so a missing/invalid grant refuses (codeRefused)
	// before a missing ANTHROPIC_API_KEY could hard-error the model backend.
	if _, err := capability.Check(e.st, e.keyPath, grantID, repo, "merge", time.Now); err != nil {
		res.Outcome = "capability_refused"
		res.Why = err.Error()
		return res, codeRefused, nil
	}

	model, err := verify.ModelBackend(modelBackend)
	if err != nil {
		return res, codeError, err
	}

	run := state.NewRunID()
	res.Run = run

	bundle, err := evidence.Gather(e.st, run, evidence.PRRef{Repo: repo, Number: pr})
	if err != nil {
		return res, codeError, err
	}

	// The verifier ladder: three rungs, each a verdict artifact.
	readinessArt, subject, err := verify.Readiness(e.st, run, bundle.View, subject)
	if err != nil {
		return res, codeError, err
	}
	floorArt, err := verify.Floor(e.st, run, bundle.Diff, e.floorBin, subject)
	if err != nil {
		return res, codeError, err
	}
	reviewsArt, err := verify.Reviews(e.st, run, bundle.Comments, subject, model)
	if err != nil {
		return res, codeError, err
	}
	verdictIDs := []string{readinessArt.ID, floorArt.ID, reviewsArt.ID}
	ciID, err := ciClassifyIfRed(e, run, bundle.View, repo, pr, subject, model)
	if err != nil {
		return res, codeError, err
	}
	if ciID != "" {
		verdictIDs = append(verdictIDs, ciID)
	}
	verdicts, err := loadVerdicts(e.st, verdictIDs...)
	if err != nil {
		return res, codeError, err
	}
	reduced, err := verify.Reduce(subject, verdicts)
	if err != nil {
		return res, codeError, err
	}
	reducedArt, err := verify.Record(e.st, run, verdictIDs, reduced)
	if err != nil {
		return res, codeError, err
	}

	return act(e, run, grantID, reduced, reducedArt.ID, res, live)
}

// ciClassifyIfRed runs the conditional enrichment rung, last before
// reduction: only a head with at least one red-concluded check has a failing
// log to classify, and this is the one rung that makes fresh network reads
// mid-run, so nothing waits behind it. Returns the verdict artifact id, or ""
// when the rung did not run. Composition is monotone, so the ordering cannot
// change the outcome.
func ciClassifyIfRed(e env, run, viewEvidenceID, repo string, pr int, subject verify.Subject, model verify.Model) (string, error) {
	red, err := verify.RedChecks(e.st, viewEvidenceID)
	if err != nil || !red {
		return "", err
	}
	logsID, err := evidence.FailedRunLogs(e.st, run, evidence.PRRef{Repo: repo, Number: pr}, subject.HeadSHA)
	if err != nil {
		return "", err
	}
	art, err := verify.CIClassify(e.st, run, logsID, subject, model)
	if err != nil {
		return "", err
	}
	return art.ID, nil
}

// act turns the composed verdict plus the grant into an outcome artifact.
func act(e env, run string, grantID string, reduced verify.Verdict, reducedID string, res gateResult, live bool) (gateResult, int, error) {
	res.Decision = reduced.Decision
	res.Tier = reduced.Tier
	res.Why = reduced.Why
	// The judged head: the live headRefOid readiness read, carried through the
	// reduction on the subject. Every outcome act finalizes is a verdict about
	// this exact commit, so it travels with the result — a caller posting an
	// out-of-band status binds to it, never to a head captured before the run.
	res.HeadSHA = reduced.Subject.HeadSHA

	record := func(kind, outcome string, extra map[string]any) error {
		body := map[string]any{"outcome": outcome, "verdict": reducedID, "grant": grantID}
		for k, v := range extra {
			body[k] = v
		}
		// Parents[0] = the reduced verdict is a contract: cycleCount joins
		// outcome → Parents[0] → Subject, and fails closed on anything else.
		_, err := e.st.Append(kind, run, []string{reducedID, grantID}, body)
		return err
	}

	// The grant was live when the run started, but evidence gathering and
	// verification take time. Re-check here so the TTL bounds the effect,
	// not just the start of the run.
	grant, err := capability.Check(e.st, e.keyPath, grantID, reduced.Subject.Repo, "merge", time.Now)
	if err != nil {
		res.Outcome = "capability_refused"
		res.Why = err.Error()
		return res, codeRefused, record(state.KindAction, "capability_refused", map[string]any{"error": err.Error()})
	}

	if reduced.Decision == verify.DecisionBlock {
		res.Outcome = "blocked"
		return res, codeBlocked, record(state.KindAction, "blocked", nil)
	}
	if reduced.Decision == verify.DecisionEscalate {
		res.Outcome = "parked_for_judgment"
		return res, codeParked, record(state.KindEscalation, "parked_for_judgment",
			map[string]any{"question": reduced.Why})
	}
	if !grant.TierWithin(reduced.Tier) {
		res.Outcome = "parked_for_judgment"
		res.Why = fmt.Sprintf("verdict tier %s exceeds grant ceiling %s; %s", reduced.Tier, grant.MaxTier, reduced.Why)
		return res, codeParked, record(state.KindEscalation, "parked_for_judgment",
			map[string]any{"question": res.Why, "code": capability.ErrTierCeiling.Error()})
	}
	// The cycle number is state-derived, never caller-passed — the driver is
	// the identity the cap bounds. An unreadable count parks: absence never
	// reads as "0 cycles consumed, proceed". An unbounded grant skips the
	// scan — it has no ceiling to enforce, so there is nothing to park on.
	n := 0
	if grant.MaxCycles != 0 {
		n, err = cycleCount(e.st, reduced.Subject, run)
	}
	// A tampered log is corruption, not a judgment call: fail hard (codeError,
	// no merge), never a park a re-mint could clear.
	if errors.Is(err, errLogTampered) {
		res.Outcome = "error"
		res.Why = err.Error()
		return res, codeError, err
	}
	if err != nil {
		res.Outcome = "parked_for_judgment"
		res.Why = fmt.Sprintf("cycle count unreadable: %v; %s", err, reduced.Why)
		return res, codeParked, record(state.KindEscalation, "parked_for_judgment",
			map[string]any{"question": res.Why, "code": parkCodeCountUnreadable})
	}
	if !grant.CyclesWithin(n + 1) {
		// A ceiling park resolves by re-minting a wider -max-cycles grant —
		// an authorization decision. A judgment pass decides content and
		// cannot launder a ceiling; this same check re-applies after judgment.
		res.Outcome = "parked_for_judgment"
		res.Why = fmt.Sprintf("%s: cycle %d exceeds grant ceiling %d; re-mint a wider -max-cycles grant to proceed; %s",
			capability.ErrCycleExceeded, n+1, grant.MaxCycles, reduced.Why)
		return res, codeParked, record(state.KindEscalation, "parked_for_judgment",
			map[string]any{"question": res.Why, "code": capability.ErrCycleExceeded.Error()})
	}

	// --match-head-commit pins the merge to the exact SHA the evidence was
	// gathered against: a push after verification makes the merge refuse
	// instead of landing unverified code.
	mergeCmd := fmt.Sprintf("gh pr merge %d -R %s --squash --delete-branch --match-head-commit %s",
		subjectNumber(reduced), reduced.Subject.Repo, reduced.Subject.HeadSHA)
	res.Action = mergeCmd
	if !live {
		res.Outcome = "would_merge"
		return res, codeMerge, record(state.KindAction, "would_merge", map[string]any{"command": mergeCmd, "dry_run": true})
	}
	res.Outcome = "merge_not_implemented"
	return res, codeMerge, record(state.KindAction, "merge_not_implemented", map[string]any{"command": mergeCmd})
}

func subjectNumber(v verify.Verdict) int { return v.Subject.Number }

// cycleCount derives how many review cycles this repo+PR has consumed, from
// state alone — a caller-passed count is never trusted, because the caller is
// the very identity the cap bounds and could under-report to sneak past its
// ceiling. Outcome bodies carry no repo/PR, so the join is outcome → parent
// reduced verdict (Parents[0]) → Subject; each distinct prior run with a
// matching subject and a counting outcome is one consumed cycle. curRun is
// excluded so a judgment resolving this run's park does not count the run
// against itself.
func cycleCount(st *state.Store, subject verify.Subject, curRun string) (int, error) {
	// The count is only a tamper-resistant backstop if the log it reads is
	// itself intact. Audit replays the chain and checks the anchor (whose key
	// lives outside the state dir, so a log writer cannot forge it); a failure
	// here fails closed rather than counting a doctored log. The count then
	// reads the audit's own verified snapshot — never a second scan, so there
	// is no window for a state-dir writer to swap the log between the
	// verification and the count.
	audit, err := st.Audit()
	if err != nil {
		return 0, fmt.Errorf("cycle count: audit: %w", err)
	}
	if !audit.OK {
		return 0, fmt.Errorf("%w: %s", errLogTampered, audit.Reason)
	}
	all := audit.All
	byID := make(map[string]state.Artifact, len(all))
	for _, a := range all {
		byID[a.ID] = a
	}
	runs := make(map[string]struct{})
	for _, a := range all {
		if !isOutcome(a) || a.Run == curRun {
			continue
		}
		counts, err := countsAsCycle(a)
		if err != nil {
			return 0, err
		}
		if !counts {
			continue
		}
		match, err := outcomeSubjectMatches(byID, a, subject)
		if err != nil {
			return 0, err
		}
		if match {
			runs[a.Run] = struct{}{}
		}
	}
	return len(runs), nil
}

func isOutcome(a state.Artifact) bool {
	return a.Kind == state.KindAction || a.Kind == state.KindEscalation
}

// outcomeBody is the slice of an action/escalation body the cycle count
// reads: the outcome string and the machine-readable park code.
type outcomeBody struct {
	Outcome string `json:"outcome"`
	Code    string `json:"code"`
}

// countsAsCycle reports whether an outcome artifact represents a consumed
// review cycle. A consumed cycle is a run that produced a ladder decision —
// blocked, parked for content judgment, or a merge outcome. Authorization
// parks (a tier or cycle ceiling, an unreadable count — the escalations that
// carry a code) and capability refusals are policy exhaustion, not
// consumption: counting them would make the re-mint resolution self-defeating,
// because every failed retry would burn the cycle the wider grant was minted
// to free. Excluding them can only lower the count by gate-authored
// authorization artifacts, never hide a run that did review work.
func countsAsCycle(a state.Artifact) (bool, error) {
	var b outcomeBody
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return false, fmt.Errorf("cycle count: parse outcome %s: %w", a.ID, err)
	}
	if a.Kind == state.KindEscalation {
		return b.Code == "", nil
	}
	return b.Outcome != "capability_refused", nil
}

// outcomeSubjectMatches follows an outcome artifact to its parent reduced
// verdict and reports whether that verdict names the given repo+PR. A dangling
// or malformed parent is an error, not a non-match: an unreadable count must
// park upstream, never read as fewer cycles consumed.
func outcomeSubjectMatches(byID map[string]state.Artifact, a state.Artifact, subject verify.Subject) (bool, error) {
	if len(a.Parents) == 0 {
		return false, fmt.Errorf("cycle count: outcome %s has no parent verdict", a.ID)
	}
	parent, ok := byID[a.Parents[0]]
	if !ok {
		return false, fmt.Errorf("cycle count: outcome %s parent %s not in log", a.ID, a.Parents[0])
	}
	if parent.Kind != state.KindVerdict {
		return false, fmt.Errorf("cycle count: outcome %s parent %s is kind %s, want verdict", a.ID, parent.ID, parent.Kind)
	}
	v, err := verify.Load(parent)
	if err != nil {
		return false, fmt.Errorf("cycle count: %w", err)
	}
	return v.Subject.Repo == subject.Repo && v.Subject.Number == subject.Number, nil
}

// validateJudgeFlags enforces the judge subcommand's flag contract: a run and
// grant are always required; a manual decision needs both a -why and a
// pass/block -decision, while -auto supplies both from the artifacts.
func validateJudgeFlags(run, grantID, decision, why string, auto bool) error {
	if run == "" || grantID == "" {
		return errors.New("judge: -run and -grant required")
	}
	if auto {
		return nil
	}
	if why == "" {
		return errors.New("judge: -why required (or -auto)")
	}
	if decision != verify.DecisionPass && decision != verify.DecisionBlock {
		return errors.New("judge: -decision must be pass or block (or use -auto)")
	}
	return nil
}

func cmdJudge(args []string) error {
	fs := flag.NewFlagSet("judge", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "gate run to judge")
	grantID := fs.String("grant", "", "grant artifact id")
	decision := fs.String("decision", "", "pass or block")
	why := fs.String("why", "", "the judgment's reasoning")
	auto := fs.Bool("auto", false, "let a frontier model judge from the artifacts alone")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if err := validateJudgeFlags(*run, *grantID, *decision, *why, *auto); err != nil {
		return err
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}

	arts, err := e.st.Run(*run)
	if err != nil {
		return err
	}
	verdicts, escalationID, subject, err := runVerdicts(arts)
	if err != nil {
		return err
	}
	if escalationID == "" {
		return fmt.Errorf("judge: run %s has no escalation to resolve", *run)
	}

	// Capability bounds judgment too — resolving an escalation is effectful.
	if _, err := capability.Check(e.st, e.keyPath, *grantID, subject.Repo, "merge", time.Now); err != nil {
		return fmt.Errorf("capability_refused: %w", err)
	}

	judgment := verify.Verdict{
		Subject:    subject,
		Source:     "operator-judgment",
		Producer:   verify.Producer{Class: verify.ClassJudgment, Impl: "operator"},
		Decision:   *decision,
		Tier:       "T0",
		Confidence: 1.0,
		Why:        *why,
	}
	if *auto {
		judgment, err = verify.AutoJudge(arts, subject)
		if err != nil {
			return err
		}
	}
	jArt, err := e.st.Append(state.KindJudgment, *run, []string{escalationID}, judgment)
	if err != nil {
		return err
	}
	reduced, err := verify.Reduce(subject, append(verdicts, judgment))
	if err != nil {
		return err
	}
	reducedArt, err := verify.Record(e.st, *run, []string{jArt.ID}, reduced)
	if err != nil {
		return err
	}
	res, code, err := act(e, *run, *grantID, reduced, reducedArt.ID, gateResult{Run: *run, PR: fmt.Sprintf("%s#%d", subject.Repo, subject.Number)}, false)
	if err != nil {
		return err
	}
	printJSON(res)
	os.Exit(code)
	return nil
}

// runVerdicts loads a run's non-reduced verifier verdicts, its escalation id,
// and the subject, from state alone.
func runVerdicts(arts []state.Artifact) ([]verify.Verdict, string, verify.Subject, error) {
	var out []verify.Verdict
	var escalationID string
	var subject verify.Subject
	for _, a := range arts {
		if a.Kind == state.KindEscalation {
			escalationID = a.ID
		}
		if a.Kind != state.KindVerdict {
			continue
		}
		v, err := verify.Load(a)
		if err != nil {
			return nil, "", subject, err
		}
		if v.Source == "reducer" {
			continue
		}
		subject = v.Subject
		out = append(out, v)
	}
	return out, escalationID, subject, nil
}

func cmdExplain(args []string) error {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	run := fs.String("run", "", "run id")
	asJSON := fs.Bool("json", false, "emit JSON projection")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *run == "" {
		return errors.New("explain: -run required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	if *asJSON {
		return observe.ExplainJSON(os.Stdout, e.st, *run)
	}
	return observe.Explain(os.Stdout, e.st, *run)
}

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	res, err := e.st.Audit()
	if err != nil {
		return err
	}
	if res.OK {
		fmt.Println("chain intact")
		return nil
	}
	at := ""
	if res.Artifact != "" {
		at = " (at " + res.Artifact + ")"
	}
	fmt.Printf("TAMPERED: %s%s\n", res.Reason, at)
	// codeError, not a bare 1: exit 1 belongs to codeBlocked in the decision
	// contract, and a tamper finding is a hard fault, not a PR decision.
	os.Exit(codeError)
	return nil
}

func cmdBacktest(args []string) error {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	// backtest ignores -state/-key for writes on purpose (see runBacktest);
	// only -floor is honoured. They are still parsed so the common flag surface
	// is uniform across verbs and passing them is not an error.
	_, floorBin, _ := commonFlags(fs)
	repo := fs.String("repo", "", "owner/repo")
	prs := fs.String("prs", "", "comma-separated PR numbers")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	if *repo == "" || *prs == "" {
		return errors.New("backtest: -repo and -prs required")
	}
	return runBacktest(*repo, *prs, *floorBin)
}

// runBacktest replays the gate over historical PRs as a pure dry run. It runs
// against a throwaway, ephemeral state dir — never the operator's durable
// -state — so a read-only preview leaves nothing spendable behind: the grant
// it must mint to satisfy the capability check, and every evidence/verdict/
// action artifact the passes produce, all land in a temp dir that is deleted on
// return. backtest is -live-free (it only ever yields would_merge/blocked/
// parked), so an ephemeral grant is all a dry run needs.
func runBacktest(repo, prs, floorBin string) error {
	e, cleanup, err := newEphemeralEnv(floorBin)
	if err != nil {
		return err
	}
	defer cleanup()
	// The grant is test-only: it exists only in the ephemeral store and is
	// erased with it, so no spendable grant is ever written to durable state.
	grantArt, err := capability.Mint(e.st, e.keyPath, repo, "merge", "T2", 0, "backtest-ephemeral", time.Hour, time.Now)
	if err != nil {
		return err
	}

	fmt.Printf("%-6s %-10s %-6s %-22s %s\n", "PR", "decision", "tier", "outcome", "run")
	for _, s := range strings.Split(prs, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("backtest: bad pr %q", s)
		}
		res, _, err := runGate(e, repo, n, grantArt.ID, false, "local")
		if err != nil {
			fmt.Printf("#%-5d error: %v\n", n, err)
			continue
		}
		fmt.Printf("#%-5d %-10s %-6s %-22s %s\n", n, res.Decision, res.Tier, res.Outcome, res.Run)
	}
	return nil
}

// newEphemeralEnv builds an env backed by throwaway temp dirs for state and
// keys, plus a cleanup that removes them. It is the test-only capability path
// for dry runs: nothing it writes — grant, evidence, verdicts, actions —
// touches the durable state log. The key dir is a sibling of the state dir (not
// nested under it), so newEnv's key-dir-outside-state invariant holds.
func newEphemeralEnv(floorBin string) (env, func(), error) {
	root, err := os.MkdirTemp("", "gate-backtest-*")
	if err != nil {
		return env{}, func() {}, fmt.Errorf("backtest: temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(root) }
	stateDir := filepath.Join(root, "state")
	keyDir := filepath.Join(root, "keys")
	e, err := newEnv(stateDir, floorBin, keyDir)
	if err != nil {
		cleanup()
		return env{}, func() {}, err
	}
	return e, cleanup, nil
}

// cmdStress appends n artifacts as fast as possible — the cross-process half
// of the multi-writer safety check. Run several of these concurrently against
// one -state dir, then `gate audit` must still report an intact chain.
func cmdStress(args []string) error {
	fs := flag.NewFlagSet("stress", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	n := fs.Int("n", 50, "artifacts to append")
	tag := fs.String("tag", "w", "writer tag")
	help, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if help {
		return nil
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	for i := 0; i < *n; i++ {
		if _, err := e.st.Append(state.KindEvidence, "run_stress", nil, map[string]any{"writer": *tag, "seq": i}); err != nil {
			return fmt.Errorf("stress %s seq %d: %w", *tag, i, err)
		}
	}
	fmt.Printf("%s: %d appends ok\n", *tag, *n)
	return nil
}

func loadVerdicts(st *state.Store, ids ...string) ([]verify.Verdict, error) {
	var out []verify.Verdict
	for _, id := range ids {
		a, err := st.Get(id)
		if err != nil {
			return nil, err
		}
		v, err := verify.Load(a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
