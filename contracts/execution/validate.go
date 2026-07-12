package execution

import (
	"fmt"
	"regexp"
	"strings"
)

// Grammar the schema documents state but no runtime JSON-Schema validator
// enforces — admission repeats it in Go (D8 pins this doubling for secret
// refs; the digest and revision shapes follow the same rule).
var (
	secretRefPattern = regexp.MustCompile(`^env:[A-Za-z_][A-Za-z0-9_]*$`)
	hex64Pattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	hex40Pattern     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	drivePattern     = regexp.MustCompile(`^[A-Za-z]:`)
)

// ValidateWorkSpec enforces the work-spec admission laws JSON Schema cannot
// express at runtime: path and traversal laws over every path position,
// executable/arg union exclusivity, workspace immutability, secret-ref
// grammar, and digest shape. Pure contract law — it decides nothing about
// routing or lifecycle.
func ValidateWorkSpec(w WorkSpec) error {
	if err := checkVersion(w.SchemaVersion); err != nil {
		return err
	}
	if err := checkExecutable(w.Command.Executable); err != nil {
		return err
	}
	for i, a := range w.Command.Args {
		if err := checkArg(i, a); err != nil {
			return err
		}
	}
	if err := checkPathRef("cwd", w.Cwd); err != nil {
		return err
	}
	if err := checkWorkspace(w.Workspace); err != nil {
		return err
	}
	for i, in := range w.Inputs {
		if err := checkInput(i, in); err != nil {
			return err
		}
	}
	for i, s := range w.Secrets {
		if err := checkSecret(i, s); err != nil {
			return err
		}
	}
	for i, o := range w.Outputs {
		if err := checkOutput(i, o); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRequest enforces the placed-request admission laws: manifest path
// law, work digest shape, placement profile hygiene (FR15), and policy
// bounds. Backend stays open vocabulary — hygiene constrains its shape, never
// its value.
func ValidateRequest(r Request) error {
	if err := checkVersion(r.SchemaVersion); err != nil {
		return err
	}
	if r.RequestID == "" {
		return fmt.Errorf("execution: request_id is empty")
	}
	if err := checkRelPath("work.manifest", r.Work.Manifest); err != nil {
		return err
	}
	if err := checkHex64("work.sha256", r.Work.SHA256); err != nil {
		return err
	}
	if err := checkLogicalName("placement.backend", r.Placement.Backend); err != nil {
		return err
	}
	if err := checkLogicalName("placement.profile", r.Placement.Profile); err != nil {
		return err
	}
	if r.Policy.DeadlineMS < 1 {
		return fmt.Errorf("execution: policy.deadline_ms %d must be at least 1", r.Policy.DeadlineMS)
	}
	if r.Policy.CancelGraceMS < 0 {
		return fmt.Errorf("execution: policy.cancel_grace_ms %d must not be negative", r.Policy.CancelGraceMS)
	}
	return nil
}

// ValidateResult enforces the terminal-receipt laws: digest shapes, the
// (status, terminal_phase, reason_code) combination table derived from TDD
// §7 Flows A–F, cause-pair vocabulary, placement-receipt hygiene, and
// artifact shape.
func ValidateResult(r Result) error {
	if err := checkVersion(r.SchemaVersion); err != nil {
		return err
	}
	if r.RunID == "" {
		return fmt.Errorf("execution: run_id is empty; a result without identity cannot be correlated")
	}
	if r.RequestID == "" {
		return fmt.Errorf("execution: request_id is empty; a result without identity cannot be correlated")
	}
	if err := checkHex64("request_sha256", r.RequestSHA256); err != nil {
		return err
	}
	if err := checkHex64("work_sha256", r.WorkSHA256); err != nil {
		return err
	}
	if err := checkTerminalCombination(r.Status, r.TerminalPhase, r.ReasonCode); err != nil {
		return err
	}
	if r.Status == StatusSucceeded && (r.WorkloadExitCode == nil || *r.WorkloadExitCode != 0) {
		return fmt.Errorf("execution: a succeeded result requires workload_exit_code 0; workload exit is necessary but not sufficient for success (D7)")
	}
	if err := checkPlacementReceipt(r.Placement); err != nil {
		return err
	}
	for i, c := range r.Causes {
		if err := checkCause(i, c); err != nil {
			return err
		}
	}
	for i, a := range r.Artifacts {
		if err := checkArtifact(i, a); err != nil {
			return err
		}
	}
	return nil
}

// checkRelPath enforces the path law shared by every path position (FR3,
// Gate A): non-empty, relative, never traversing above its root. It applies
// to structured {root, value} references and bare fixed-root strings alike —
// the two positions differ in shape, not in law.
func checkRelPath(position, path string) error {
	if path == "" {
		return fmt.Errorf("execution: %s is empty", position)
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) || drivePattern.MatchString(path) {
		return fmt.Errorf("execution: %s %q is absolute; paths are relative to a logical root", position, path)
	}
	for _, seg := range strings.FieldsFunc(path, isPathSep) {
		if seg == ".." {
			return fmt.Errorf("execution: %s %q traverses above its root", position, path)
		}
	}
	return nil
}

func isPathSep(r rune) bool { return r == '/' || r == '\\' }

func checkPathRef(position string, ref PathRef) error {
	if ref.Root != RootWorkspace && ref.Root != RootInputs && ref.Root != RootOut {
		return fmt.Errorf("execution: %s root %q is not a logical root (workspace|inputs|out)", position, ref.Root)
	}
	return checkRelPath(position, ref.Value)
}

func checkHex64(position, digest string) error {
	if !hex64Pattern.MatchString(digest) {
		return fmt.Errorf("execution: %s %q is not a 64-hex sha256", position, digest)
	}
	return nil
}

func checkExecutable(e Executable) error {
	if (e.Name == nil) == (e.Path == nil) {
		return fmt.Errorf("execution: command.executable must set exactly one of name or path")
	}
	if e.Path != nil {
		return checkPathRef("command.executable.path", *e.Path)
	}
	name := *e.Name
	if name == "" {
		return fmt.Errorf("execution: command.executable.name is empty")
	}
	if strings.ContainsFunc(name, isPathSep) || drivePattern.MatchString(name) {
		return fmt.Errorf("execution: command.executable.name %q must be a bare name resolved via the placement profile's PATH, never a path", name)
	}
	return nil
}

func checkArg(i int, a Arg) error {
	if (a.Literal == nil) == (a.Path == nil) {
		return fmt.Errorf("execution: command.args[%d] must set exactly one of literal or path", i)
	}
	if a.Path != nil {
		return checkPathRef(fmt.Sprintf("command.args[%d].path", i), *a.Path)
	}
	return nil
}

func checkWorkspace(w Workspace) error {
	if w.Kind != WorkspaceKindGit {
		return fmt.Errorf("execution: workspace.kind %q is not a known kind (v0 defines only %q)", w.Kind, WorkspaceKindGit)
	}
	if w.URL == "" {
		return fmt.Errorf("execution: workspace.url is empty")
	}
	if !hex40Pattern.MatchString(w.Revision) {
		return fmt.Errorf("execution: workspace.revision %q is not an immutable full 40-hex commit; symbolic refs reject", w.Revision)
	}
	return nil
}

func checkInput(i int, in Input) error {
	if err := checkRelPath(fmt.Sprintf("inputs[%d].source", i), in.Source); err != nil {
		return err
	}
	if err := checkRelPath(fmt.Sprintf("inputs[%d].target", i), in.Target); err != nil {
		return err
	}
	return checkHex64(fmt.Sprintf("inputs[%d].sha256", i), in.SHA256)
}

func checkSecret(i int, s Secret) error {
	if s.Name == "" {
		return fmt.Errorf("execution: secrets[%d].name is empty", i)
	}
	if !secretRefPattern.MatchString(s.Ref) {
		return fmt.Errorf("execution: secrets[%d].ref %q must match ^env:[A-Za-z_][A-Za-z0-9_]*$; references are opaque names, never values (D8)", i, s.Ref)
	}
	return nil
}

func checkOutput(i int, o Output) error {
	return checkRelPath(fmt.Sprintf("outputs[%d].path", i), o.Path)
}

// checkLogicalName is profile hygiene (FR15): placement backend and profile
// are logical names resolved by configuration, never paths. A colon counts
// as a host-path shape only as a single-letter drive prefix; multi-character
// colon names (rooms:v2) remain legal open vocabulary.
func checkLogicalName(position, name string) error {
	if name == "" {
		return fmt.Errorf("execution: %s is empty", position)
	}
	if strings.ContainsFunc(name, isPathSep) || drivePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("execution: %s %q must be a logical name — no path separators, traversal, or host-path shapes", position, name)
	}
	return nil
}

// terminalLaw is the legal pairing for one reason code, derived from TDD §7
// Flows A–F and the §5 reason list. A nil phase set means the terminal phase
// is whichever phase the run was in when interrupted — the TDD deliberately
// does not pin it for deadline expiry and cancellation, so any canonical
// phase is legal.
type terminalLaw struct {
	status string
	phases map[string]bool
}

var terminalLaws = map[string]terminalLaw{
	ReasonCompleted:            {StatusSucceeded, phaseSet(PhaseTerminal)}, // Flow A
	ReasonPreparationFailed:    {StatusFailed, phaseSet(PhasePreparation)}, // §5
	ReasonStartupFailed:        {StatusFailed, phaseSet(PhaseStartup)},     // §5
	ReasonPlacementUnavailable: {StatusFailed, phaseSet(PhaseStartup)},     // Flow B
	ReasonWorkloadFailed:       {StatusFailed, phaseSet(PhaseWorkload)},    // D7
	ReasonCollectionFailed:     {StatusFailed, phaseSet(PhaseCollection)},  // Flow E
	ReasonCleanupFailed:        {StatusFailed, phaseSet(PhaseCleanup)},     // Flow C escalation / D7
	ReasonControllerLost:       {StatusFailed, phaseSet(PhaseTerminal)},    // Flow F (reconcile-written)
	ReasonDeadlineExceeded:     {StatusTimedOut, nil},                      // Flow C — phase at interruption
	ReasonCancelRequested:      {StatusCancelled, nil},                     // Flow D — phase at interruption
}

func phaseSet(phases ...string) map[string]bool {
	set := map[string]bool{}
	for _, p := range phases {
		set[p] = true
	}
	return set
}

func checkTerminalCombination(status, phase, reason string) error {
	law, ok := terminalLaws[reason]
	if !ok {
		return fmt.Errorf("execution: reason_code %q is not a stable reason", reason)
	}
	if status != law.status {
		return fmt.Errorf("execution: status %q with reason %q; the reason requires status %q", status, reason, law.status)
	}
	if _, known := phaseRank[phase]; !known {
		return fmt.Errorf("execution: terminal_phase %q is not a canonical phase", phase)
	}
	if law.phases != nil && !law.phases[phase] {
		return fmt.Errorf("execution: terminal_phase %q is illegal for reason %q", phase, reason)
	}
	return nil
}

// checkCause admits a prior (phase, reason_code) pair: canonical vocabulary,
// with the phase anchored to the reason by the same table the primary triple
// uses. A cause carries no status of its own, so only the phase half of the
// law applies — and the nil phase sets stay any-phase for deadline/cancel.
func checkCause(i int, c Cause) error {
	if _, ok := phaseRank[c.Phase]; !ok {
		return fmt.Errorf("execution: causes[%d].phase %q is not a canonical phase", i, c.Phase)
	}
	law, ok := terminalLaws[c.ReasonCode]
	if !ok {
		return fmt.Errorf("execution: causes[%d].reason_code %q is not a stable reason", i, c.ReasonCode)
	}
	if law.phases != nil && !law.phases[c.Phase] {
		return fmt.Errorf("execution: causes[%d] phase %q is illegal for reason %q", i, c.Phase, c.ReasonCode)
	}
	return nil
}

func checkPlacementReceipt(p PlacementReceipt) error {
	if err := checkLogicalName("placement.backend", p.Backend); err != nil {
		return err
	}
	if err := checkLogicalName("placement.profile", p.Profile); err != nil {
		return err
	}
	if p.ImageSHA256 != "" {
		if err := checkHex64("placement.image_sha256", p.ImageSHA256); err != nil {
			return err
		}
	}
	if p.StreamDelivery != StreamDeliveryTerminalReplay && p.StreamDelivery != StreamDeliveryNone {
		return fmt.Errorf("execution: placement.stream_delivery %q is not a v0 mode; live is reserved until an access contract exists (D11)", p.StreamDelivery)
	}
	return nil
}

func checkArtifact(i int, a Artifact) error {
	if err := checkRelPath(fmt.Sprintf("artifacts[%d].path", i), a.Path); err != nil {
		return err
	}
	if err := checkHex64(fmt.Sprintf("artifacts[%d].sha256", i), a.SHA256); err != nil {
		return err
	}
	if a.Size < 0 {
		return fmt.Errorf("execution: artifacts[%d].size %d must not be negative", i, a.Size)
	}
	return nil
}
