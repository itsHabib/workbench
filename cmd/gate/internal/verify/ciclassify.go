package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// The ci-classify rung enriches a red check with its cause: deterministic
// signature floor first, local-model advisory for the residual, escalation
// when neither produces a trusted answer. Classification never gates — a pass
// means "no judgment needed from me", and the readiness block on the same red
// check dominates in Reduce regardless. Three causes, three next actions:
// flake (retry fixes it), real-break (fix the repo — the block already demands
// that), infra (an environment owner must act — escalate).

// sourceCIClassify is the stable handle consumers find red-CI cause findings
// under. Compare against the constant, never a bare string.
const sourceCIClassify = "ci-classify"

// ciFloorImpl names the deterministic signature floor as a producer impl.
const ciFloorImpl = "ci-signature-floor"

// Classification buckets. There is no severity mapping on purpose: a bucket
// is a cause, not a risk — the verdict tier stays T0 so classification can
// never move approval requirements.
const (
	bucketFlake     = "flake"
	bucketRealBreak = "real-break"
	bucketInfra     = "infra"
)

func knownBucket(b string) bool {
	return b == bucketFlake || b == bucketRealBreak || b == bucketInfra
}

// ciSignatures is the shipping floor: first match wins, most specific first.
// The floor claims only flake/infra on unambiguous signatures — real-break is
// the advisory's strong suit. ETIMEDOUT/ECONNREFUSED are deliberately absent
// (advisory-only): both fire routinely inside flaky integration tests, so as
// floor signatures they would misread flake as infra.
var ciSignatures = []struct {
	re     *regexp.Regexp
	bucket string
}{
	{regexp.MustCompile(`(?i)the database system is (starting up|shutting down)`), bucketFlake},
	{regexp.MustCompile(`(?i)connection to server on socket .*(failed|no such file)`), bucketFlake},
	{regexp.MustCompile(`(?i)\bEBUSY\b|resource busy or locked`), bucketFlake},
	{regexp.MustCompile(`(?i)EADDRINUSE|address already in use|port .* already in use`), bucketFlake},
	{regexp.MustCompile(`(?i)passed on retry|retried and passed|passe[sd] on rerun|attempt \d+; passed`), bucketFlake},
	{regexp.MustCompile(`(?i)failed to authenticate`), bucketInfra},
	{regexp.MustCompile(`(?i)workflow initiated by non.?human actor`), bucketInfra},
	{regexp.MustCompile(`(?i)429 too many requests`), bucketInfra},
	{regexp.MustCompile(`(?i)could not resolve host`), bucketInfra},
	{regexp.MustCompile(`(?i)no space left on device`), bucketInfra},
	{regexp.MustCompile(`(?i)received a shutdown signal and disconnected`), bucketInfra},
	{regexp.MustCompile(`(?i)go version file .* does not exist`), bucketInfra},
	{regexp.MustCompile(`(?i)/installation/token`), bucketInfra},
}

// ciWrapExclusions are literal substrings — never regexes, so a sentinel's
// punctuation can never act as a metacharacter — naming wrapper/relay/teardown
// lines that can never fire a floor signature: they sit near the cause, not at
// it, and a signature word inside one must not shadow the real cause line.
// Matched case-insensitively against the lowered line.
var ciWrapExclusions = []string{
	"elifecycle",
	"err_pnpm_recursive",
	"make: ***",
	"process completed with exit code",
	"command failed with exit code",
	"exit status ",
	"npm error code",
	"waiting for other jobs",
	"post job cleanup",
	"cleaning up orphan",
	"terminate orphan process",
	"docker rm",
	"docker network rm",
	"stop and remove container",
}

// ciTeardownSentinel opens a step's teardown region: nothing after it may fire
// a floor signature. Teardown noise floods step tails and shadows causes.
const ciTeardownSentinel = "post job cleanup"

// ciFloorMatch is a floor hit: the bucket, the signature substring that fired,
// and the verbatim line it fired on.
type ciFloorMatch struct {
	bucket    string
	signature string
	line      string
}

// ciFloor runs the signature table over one chunk. It abstains (nil) unless a
// signature fires on a live line — non-wrapper, before the teardown region.
func ciFloor(text string) *ciFloorMatch {
	teardown := false
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSuffix(line, "\r"))
		if strings.Contains(lower, ciTeardownSentinel) {
			teardown = true
		}
		if teardown || wrapperLine(lower) {
			continue
		}
		if m := matchSignature(line); m != nil {
			return m
		}
	}
	return nil
}

func wrapperLine(lower string) bool {
	for _, w := range ciWrapExclusions {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func matchSignature(line string) *ciFloorMatch {
	for _, s := range ciSignatures {
		if s.re.MatchString(line) {
			return &ciFloorMatch{bucket: s.bucket, signature: s.re.FindString(line), line: strings.TrimSpace(line)}
		}
	}
	return nil
}

// The advisory prompt and output schema ship byte-identical to the vendored
// eval bundle: the measured numbers hold only for exactly this contract.
const ciPrompt = `You classify ONE failed CI log excerpt into exactly one bucket. Decide with a single question: if the SAME commit were re-run unchanged, what would happen?
- "flake" = it would likely PASS on a plain retry; the failure was transient. Examples: a test timeout or step that passes on rerun; a race; a port already in use; a database or service still starting up / not ready yet (connection refused, "the database system is starting up", a missing/again-later socket) that a retry would fix; a file lock (EBUSY, "resource busy or locked", common on Windows); an intermittent network hiccup mid-test.
- "infra" = it would FAIL again and a human must fix the CI ENVIRONMENT, not the repo. Examples: the runner or checkout failed; an auth or installation-token exchange failed, or the action refused to run; a dependency registry is down or rate-limited (HTTP 429 or a connect timeout to npm / the terraform / a package registry); disk full; a setup action cannot find a required file it is configured to read.
- "real-break" = it would FAIL again and the fix is a change to the REPOSITORY's code, tests, config, or dependencies. Examples: a compile or type error; a lint or formatting failure (eslint, clippy, prettier, gofmt, revive, an invalid golangci config); a test assertion failure; coverage below a threshold; a dependency security advisory the repo must address. Note: the last visible line is often a generic wrapper ("ELIFECYCLE", "ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL", "make: *** Error", "Process completed with exit code 1") — look ABOVE it for the real cause and classify by that.
Set "evidence" to the single most decisive line from the excerpt, copied VERBATIM exactly as it appears — no paraphrase, no truncation, no added markup — the line that proves your bucket (prefer the real cause over a generic wrapper line). Set "why" to a one-line plain explanation naming the failing test, file, or step (this may paraphrase). Set "confidence" to a 0.0-1.0 estimate (NOT a percentage) that your bucket is right; if the excerpt shows no clear cause, set it low. Output JSON only.`

var ciSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "bucket":     {"type": "string", "enum": ["flake", "real-break", "infra"]},
    "evidence":   {"type": "string"},
    "why":        {"type": "string"},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  },
  "required": ["bucket", "evidence", "why", "confidence"]
}`)

// ciAdvisoryURL is a variable so tests can fake the HTTP boundary.
var ciAdvisoryURL = ollamaURL

type ciAdvisory struct {
	Bucket     string  `json:"bucket"`
	Evidence   string  `json:"evidence"`
	Why        string  `json:"why"`
	Confidence float64 `json:"confidence"`
}

func ciAdvise(ctx context.Context, chunk string, model Model) (ciAdvisory, error) {
	content, err := model.chat(ctx, ciPrompt, chunk, ciSchema)
	if err != nil {
		return ciAdvisory{}, err
	}
	var adv ciAdvisory
	if err := json.Unmarshal([]byte(content), &adv); err != nil {
		return ciAdvisory{}, fmt.Errorf("bad model json: %w", err)
	}
	return adv, nil
}

// ciNormRe collapses everything outside [a-z0-9] so the verbatim check
// survives whitespace and punctuation drift without accepting a paraphrase.
var ciNormRe = regexp.MustCompile(`[^a-z0-9]+`)

func ciNorm(s string) string {
	return strings.TrimSpace(ciNormRe.ReplaceAllString(strings.ToLower(s), " "))
}

// ciTrusts is the verbatim-evidence verifier: an in-enum bucket plus a
// non-empty quote actually present in the chunk. Verbatim here means
// normalized token presence — punctuation and spacing may drift, invented
// content may not. The empty check runs first — the empty string is a
// substring of everything and would otherwise bypass the whole check. The
// advisory's self-reported confidence gates nothing; the quote is the signal.
func ciTrusts(adv ciAdvisory, chunk string) bool {
	ev := ciNorm(adv.Evidence)
	if ev == "" {
		return false
	}
	if !strings.Contains(ciNorm(chunk), ev) {
		return false
	}
	return knownBucket(adv.Bucket)
}

// Evidence-artifact shapes as this rung reads them (recorded by
// evidence.FailedRunLogs; state is the only channel, so these are separate
// deserialization targets on purpose). The JSON tags must stay in sync with
// evidence.CIRun/CIChunk — a renamed tag there would silently zero these
// fields.
type ciChunk struct {
	Step string `json:"step"`
	Text string `json:"text"`
}

type ciRunLogs struct {
	ID           int64     `json:"id"`
	Workflow     string    `json:"workflow"`
	Conclusion   string    `json:"conclusion"`
	Chunks       []ciChunk `json:"chunks"`
	DroppedSteps int       `json:"dropped_steps"`
}

type ciLogsEvidence struct {
	Runs        []ciRunLogs `json:"runs"`
	OmittedRuns int         `json:"omitted_runs"`
	ListError   string      `json:"list_error"`
}

// ciChunkVerdict is one chunk's classification outcome. A zero bucket with a
// non-empty distrust reason is an escalation: the chunk stays unenriched.
type ciChunkVerdict struct {
	step       string
	bucket     string
	cause      string
	evidence   string
	confidence float64
	advisory   bool
	distrust   string
}

// ciAdvisoryBudget bounds advisory calls per red run: each call rides a
// slow local-model HTTP round-trip, so a run with dozens of floor-abstaining
// chunks must not stall the merge step for an hour. Overflow escalates —
// fail-closed and finding-visible, never silently skipped.
const ciAdvisoryBudget = 4

// classifyChunk classifies one failed-step excerpt: floor first, advisory on
// abstain. Every advisory failure — transport, parse, or a distrusted
// answer — lands in distrust, never an error: a red check with no classifier
// is still a gateable state, just an unenriched one.
func classifyChunk(c ciChunk, allowAdvisory bool, model Model) ciChunkVerdict {
	out := ciChunkVerdict{step: c.Step, confidence: 1}
	if strings.TrimSpace(c.Text) == "" {
		out.distrust = "unclassifiable: empty log excerpt"
		return out
	}
	if m := ciFloor(c.Text); m != nil {
		out.bucket, out.cause, out.evidence = m.bucket, m.signature, m.line
		return out
	}
	if !allowAdvisory {
		out.distrust = "unclassifiable: advisory budget exhausted for this run"
		return out
	}
	out.advisory = true
	adv, err := ciAdvise(context.Background(), c.Text, model)
	if err != nil {
		out.distrust = "advisory unavailable: " + err.Error()
		return out
	}
	if !ciTrusts(adv, c.Text) {
		out.distrust = "unclassifiable: no adjudicable cause in excerpt (advisory distrusted)"
		return out
	}
	out.bucket, out.cause, out.evidence, out.confidence = adv.Bucket, adv.Why, adv.Evidence, adv.Confidence
	return out
}

// findingTitle maps a classified chunk onto its finding title: cause plus the
// next action the bucket implies.
func findingTitle(cv ciChunkVerdict) string {
	switch cv.bucket {
	case bucketFlake:
		return "flake: " + cv.cause + " — likely passes on retry"
	case bucketInfra:
		return "infra: " + cv.cause + " — environment owner must act"
	}
	return "real-break: " + cv.cause
}

// classifyRun classifies one red run's chunks into findings: one finding per
// run when its chunks agree, one per differing bucket when they disagree — a
// mixed run is never flattened to one misleading cause. Returns the findings,
// the escalation reasons, whether the advisory was invoked, and the minimum
// trusted confidence (1 when nothing lowered it).
func classifyRun(r ciRunLogs, model Model) ([]Finding, []string, bool, float64) {
	if len(r.Chunks) == 0 {
		f := Finding{Title: "unclassifiable: no failed-step log", Locus: r.Workflow}
		return []Finding{f}, []string{fmt.Sprintf("%s: no failed-step log for red run %d", r.Workflow, r.ID)}, false, 1
	}
	var findings []Finding
	var escalations []string
	// A step dropped whole by the byte cap contributed zero signal — its
	// cause (an infra auth failure, say) may be exactly what vanished, and
	// the surviving chunks would then read confidently wrong. Absence by cap
	// escalates like any other absence; it must never default to the
	// survivors' story.
	if r.DroppedSteps > 0 {
		title := fmt.Sprintf("unclassifiable: %d failed steps dropped by byte cap", r.DroppedSteps)
		findings = append(findings, Finding{Title: title, Locus: r.Workflow})
		escalations = append(escalations, r.Workflow+": "+title)
	}
	seen := map[string]bool{}
	advisory := false
	conf := 1.0
	advisoryCalls := 0
	for _, c := range r.Chunks {
		cv := classifyChunk(c, advisoryCalls < ciAdvisoryBudget, model)
		if cv.advisory {
			advisoryCalls++
		}
		advisory = advisory || cv.advisory
		if cv.distrust != "" {
			findings = append(findings, Finding{Title: cv.distrust, Locus: cv.step})
			escalations = append(escalations, cv.step+": "+cv.distrust)
			continue
		}
		if cv.confidence < conf {
			conf = cv.confidence
		}
		if cv.bucket == bucketInfra {
			escalations = append(escalations, cv.step+": infra: "+cv.cause)
		}
		if seen[cv.bucket] {
			continue
		}
		seen[cv.bucket] = true
		findings = append(findings, Finding{
			Title:      findingTitle(cv),
			Locus:      cv.step,
			Confidence: cv.confidence,
			Evidence:   cv.evidence,
		})
	}
	return findings, escalations, advisory, conf
}

// CIClassify is the enrichment rung over recorded red-run logs. It emits one
// verdict whose findings carry cause + verbatim evidence per failed run; the
// decision escalates only when attention is needed beyond the mechanical
// fix-or-retry the readiness block already forces — infra, or nothing
// adjudicable. It never blocks: blocking is readiness's job, and a classifier
// that blocked would gate merges on classification.
func CIClassify(st *state.Store, run, logsEvidenceID string, subject Subject, model Model) (state.Artifact, error) {
	if model == nil {
		model = newLocalModel(ciAdvisoryURL)
	}
	a, err := st.Get(logsEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var body ciLogsEvidence
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: parse ci-logs evidence: %w", err)
	}

	v := Verdict{
		Subject:    subject,
		Source:     sourceCIClassify,
		Producer:   Producer{Class: ClassCode, Impl: ciFloorImpl},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
	}
	var escalations []string
	advisory := false
	for _, r := range body.Runs {
		findings, esc, adv, conf := classifyRun(r, model)
		v.Findings = append(v.Findings, findings...)
		escalations = append(escalations, esc...)
		advisory = advisory || adv
		if conf < v.Confidence {
			v.Confidence = conf
		}
	}
	// The artifact claims no more authority than its least-trusted
	// contributor: any advisory involvement — trusted or distrusted — makes
	// the whole verdict local-model class, and the ladder constrains it
	// accordingly.
	if advisory {
		v.Producer = Producer{Class: ClassLocal, Impl: model.impl()}
	}
	// A failed run listing is recorded, not returned: the gate must not
	// abort its whole decision on an enrichment read — the absence escalates
	// like any other absence.
	if body.ListError != "" {
		v.Findings = append(v.Findings, Finding{Title: "unclassifiable: red-run list unavailable"})
		escalations = append(escalations, "red-run list unavailable: "+body.ListError)
	}
	if len(body.Runs) == 0 && body.ListError == "" {
		v.Findings = append(v.Findings, Finding{Title: "unclassifiable: no failed-step log"})
		escalations = append(escalations, "no red-run logs recorded for a red check")
	}
	if body.OmittedRuns > 0 {
		v.Findings = append(v.Findings, Finding{
			Title: fmt.Sprintf("capped: %d additional red runs not fetched", body.OmittedRuns),
		})
	}
	if len(escalations) > 0 {
		v.Decision = DecisionEscalate
		v.Why = strings.Join(escalations, "; ")
		return Record(st, run, []string{logsEvidenceID}, v)
	}
	v.Why = fmt.Sprintf("classified %d red runs: %s", len(body.Runs), summarize(v.Findings))
	return Record(st, run, []string{logsEvidenceID}, v)
}

func summarize(findings []Finding) string {
	var titles []string
	for _, f := range findings {
		titles = append(titles, f.Title)
	}
	return strings.Join(titles, "; ")
}

// RedChecks reports whether the recorded pr-view evidence carries at least
// one red-concluded check — the condition the ci-classify rung runs behind.
// Red means concluded-and-failed, not merely non-green: a pending check has
// no failure to classify and stays readiness's problem.
func RedChecks(st *state.Store, viewEvidenceID string) (bool, error) {
	a, err := st.Get(viewEvidenceID)
	if err != nil {
		return false, err
	}
	var body struct {
		Data prView `json:"data"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return false, fmt.Errorf("verify: parse view evidence: %w", err)
	}
	for _, c := range body.Data.StatusCheckRollup {
		if redCheck(c) {
			return true, nil
		}
	}
	return false, nil
}

func redCheck(c rollupCheck) bool {
	switch c.Conclusion {
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "CANCELLED":
		return true
	}
	return c.State == "FAILURE" || c.State == "ERROR"
}
