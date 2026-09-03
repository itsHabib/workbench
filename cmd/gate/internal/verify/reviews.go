package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// The local rung consolidates the bot review panel: per-comment extraction
// (never batch — batching mangles dense items), extract-don't-judge (small
// models read off structure reliably but confabulate judgments), and an
// escalate gate on low confidence. Wrong here only ever adds a judgment call.
const (
	ollamaURL      = "http://localhost:11434/api/chat"
	ollamaModel    = "qwen2.5:7b"
	confidenceGate = 0.6

	// reasonConsolidationUnavailable names the one failure that is not about
	// the review panel at all: the consolidator could not reach its model, so
	// it read nothing. Reporting that as a low-confidence extraction tells the
	// judge there were ambiguous findings when none were ever extracted — the
	// escalation must name the infrastructure fault instead.
	reasonConsolidationUnavailable = "consolidation_unavailable"

	extractPrompt = `You EXTRACT structure from ONE AI code-review comment. Do NOT judge whether it is valid or already handled. Read off: (headline) the bot's OWN title or first line, cleaned of markdown, badges, and HTML comments — quote its words, do not paraphrase; (severity) the severity the bot itself stated (High/Medium/P1/P2), else "unknown"; (verdict) actionable if it reports a problem, nit if trivial or style, question if it asks something, none if it reports no problem — an approval, a "no issues found", or the bot's own statement that its findings are resolved or the change is ready; (confidence) a 0.0-1.0 estimate that you extracted headline and severity correctly. Output JSON only.`
)

var extractSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "headline":   {"type": "string"},
    "severity":   {"type": "string"},
    "verdict":    {"type": "string", "enum": ["actionable", "nit", "question", "none"]},
    "confidence": {"type": "number"}
  },
  "required": ["headline", "severity", "verdict", "confidence"]
}`)

type extraction struct {
	Headline   string  `json:"headline"`
	Severity   string  `json:"severity"`
	Verdict    string  `json:"verdict"`
	Confidence float64 `json:"confidence"`
}

// knownVerdict mirrors ciclassify's knownBucket: the cloud backend's
// tool input_schema is a steer, not a hard grammar like Ollama's "format", so
// an out-of-enum verdict can slip through. Valid values are the extraction
// schema's enum. An unknown verdict must be treated as a failed extraction and
// escalate, never be silently counted as "not actionable".
func knownVerdict(v string) bool {
	return v == "actionable" || v == "nit" || v == "question" || v == "none"
}

// Reviews consolidates the bot panel's comments via the selected model.
// Producer class: local — by the ladder law it may pass or escalate, never
// block: actionable findings and low-confidence extractions both park the
// gate for judgment rather than deciding. A model call that never completed is
// a third, separate outcome — see reasonConsolidationUnavailable.
func Reviews(st *state.Store, run, commentsEvidenceID string, subject Subject, model Model) (state.Artifact, error) {
	if model == nil {
		model = newLocalModel(ollamaURL)
	}
	a, err := st.Get(commentsEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var body struct {
		Comments []reviewComment `json:"comments"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: parse comments evidence: %w", err)
	}

	v := Verdict{
		Subject:    subject,
		Source:     "review-consolidation",
		Producer:   Producer{Class: ClassLocal, Impl: model.impl()},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
	}
	// The tally's baselines are the verdict's: full confidence, floor tier,
	// lowered only by what a comment actually carries.
	t := panelTally{minConfidence: 1.0, maxTier: "T0"}
	for _, c := range body.Comments {
		if !c.IsBot || strings.Contains(c.Body, "review-coordinator-verdict") {
			continue
		}
		if staleComment(c.Resolved, c.CommitID, subject.HeadSHA) {
			t.stale++
			continue
		}
		t.add(consolidateComment(c, model))
	}
	v.Findings = t.findings
	v.Confidence = t.minConfidence
	v.Tier = t.maxTier

	suffix := ""
	if t.stale > 0 {
		suffix = fmt.Sprintf(" (%d stale/resolved comments from earlier cycles excluded)", t.stale)
	}
	switch {
	case t.processed == 0:
		// An empty panel is not a reviewed panel: a PR opened minutes ago,
		// before any bot has run, must not read as consolidated. Escalate
		// (the local rung's fail-closed) rather than pass — a judge can
		// confirm the panel is genuinely empty. An all-stale panel lands here
		// too: every recorded finding predates this head, so the judged head
		// has no live review yet.
		v.Decision = DecisionEscalate
		v.Why = "no bot review comments for this head — cannot consolidate a panel" + suffix
	case t.unavailable > 0:
		// Dominates the finding tallies below: whatever the readable comments
		// said, part of the panel was never consolidated. The judge is being
		// asked about a broken consolidator, not about ambiguous findings, and
		// confidence in this verdict is zero, not merely low.
		v.Decision = DecisionEscalate
		v.Confidence = 0
		v.Why = fmt.Sprintf("%s: %d of %d bot comments were never read — the %s model call failed to complete, so nothing was extracted from them; this is an infrastructure fault, not an ambiguous review%s", reasonConsolidationUnavailable, t.unavailable, t.processed, model.impl(), suffix)
	case t.actionable > 0 || t.lowConf > 0:
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf("%d bot comments: %d actionable, %d low-confidence extractions — needs judgment%s", t.processed, t.actionable, t.lowConf, suffix)
	default:
		v.Why = fmt.Sprintf("%d bot comments, none actionable (nits, questions, or no-problem)%s", t.processed, suffix)
	}
	return Record(st, run, []string{commentsEvidenceID}, v)
}

// reviewComment is the shape the panel evidence records per comment.
type reviewComment struct {
	Author   string `json:"author"`
	IsBot    bool   `json:"is_bot"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
	CommitID string `json:"commit_id"`
	Resolved bool   `json:"resolved"`
}

// consolidated is one comment's contribution to the panel: its finding plus
// which escalation gate it trips. The three failure modes are deliberately
// distinct — unavailable means the call never completed (nothing was read),
// lowConf means the model answered and the answer cannot be trusted.
type consolidated struct {
	finding     Finding
	confidence  float64
	actionable  bool
	lowConf     bool
	unavailable bool
	raisesTier  bool
}

// consolidateComment extracts one bot comment. It decides how the comment
// counts; the tally does the accumulating and Reviews does the composing.
func consolidateComment(c reviewComment, model Model) consolidated {
	ex, err := extractOne(context.Background(), c.Body, model)
	// A call that never completed is not an uncertain reading of this comment:
	// the comment was not read. Keep it out of the low-confidence tally so the
	// escalation can name the real fault. A failed read lowers no confidence —
	// there is no reading to be confident about — so it carries 1.
	if errors.Is(err, ErrModelUnavailable) {
		f := Finding{Title: reasonConsolidationUnavailable + ": " + err.Error(), Locus: locus(c.Path, c.Line)}
		return consolidated{finding: f, confidence: 1, unavailable: true}
	}
	if err != nil {
		f := Finding{Title: "extraction failed: " + err.Error(), Locus: locus(c.Path, c.Line)}
		return consolidated{finding: f, confidence: 1, lowConf: true}
	}
	// An out-of-enum verdict from the cloud backend (whose schema is a steer,
	// not a grammar) is an unreadable extraction: escalate exactly as a failed
	// one, never fall through as "not actionable".
	if !knownVerdict(ex.Verdict) {
		f := Finding{Title: "extraction failed: out-of-enum verdict " + ex.Verdict, Locus: locus(c.Path, c.Line)}
		return consolidated{finding: f, confidence: 1, lowConf: true}
	}
	return consolidated{
		finding: Finding{
			Title:      fmt.Sprintf("[%s] %s (%s)", strings.TrimSuffix(c.Author, "[bot]"), ex.Headline, ex.Verdict),
			Severity:   normSeverity(ex.Severity),
			Locus:      locus(c.Path, c.Line),
			Confidence: ex.Confidence,
		},
		confidence: ex.Confidence,
		actionable: ex.Verdict == "actionable",
		lowConf:    ex.Confidence < confidenceGate,
		// A no-problem comment (approval, ship-it, findings-resolved note) may
		// still quote a severity badge; it raises nothing.
		raisesTier: ex.Verdict != "none",
	}
}

// panelTally accumulates consolidated comments monotonically: worst tier wins,
// lowest confidence carries, each gate counts its own trips.
type panelTally struct {
	findings                                           []Finding
	maxTier                                            string
	minConfidence                                      float64
	processed, stale, actionable, lowConf, unavailable int
}

func (t *panelTally) add(c consolidated) {
	t.processed++
	t.findings = append(t.findings, c.finding)
	if c.confidence < t.minConfidence {
		t.minConfidence = c.confidence
	}
	if c.actionable {
		t.actionable++
	}
	if c.lowConf {
		t.lowConf++
	}
	if c.unavailable {
		t.unavailable++
	}
	if c.raisesTier && severityTier(c.finding.Severity) > tierRank(t.maxTier) {
		t.maxTier = "T" + fmt.Sprint(severityTier(c.finding.Severity))
	}
}

// staleComment reports whether a bot comment is a prior cycle's finding, not
// evidence about the judged head. Bot comments layer across review cycles —
// nothing is overwritten — so an inline comment whose thread is resolved, or
// whose anchor commit differs from the judged head, would re-litigate fixed
// findings and bury fresh ones in stale noise. A comment with no commit anchor
// (issue-level, or evidence recorded before anchors existed) and any comment
// judged without a known head are never stale — fail toward consolidating.
func staleComment(resolved bool, commitID, headSHA string) bool {
	if resolved {
		return true
	}
	return commitID != "" && headSHA != "" && commitID != headSHA
}

func extractOne(ctx context.Context, comment string, model Model) (extraction, error) {
	content, err := model.chat(ctx, extractPrompt, comment, extractSchema)
	if err != nil {
		return extraction{}, err
	}
	var ex extraction
	if err := json.Unmarshal([]byte(content), &ex); err != nil {
		return extraction{}, fmt.Errorf("bad model json: %w", err)
	}
	return ex, nil
}

func normSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "Severity")))
	if s == "" {
		return "unknown"
	}
	return s
}

func locus(path string, line int) string {
	if path == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", path, line)
}
