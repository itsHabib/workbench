package verify

import (
	"context"
	"encoding/json"
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
// gate for judgment rather than deciding.
func Reviews(st *state.Store, run, commentsEvidenceID string, subject Subject, model Model) (state.Artifact, error) {
	if model == nil {
		model = newLocalModel(ollamaURL)
	}
	a, err := st.Get(commentsEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var body struct {
		Comments []struct {
			Author   string `json:"author"`
			IsBot    bool   `json:"is_bot"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Body     string `json:"body"`
			CommitID string `json:"commit_id"`
			Resolved bool   `json:"resolved"`
		} `json:"comments"`
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
	actionable, lowConf, processed, stale := 0, 0, 0, 0
	for _, c := range body.Comments {
		if !c.IsBot || strings.Contains(c.Body, "review-coordinator-verdict") {
			continue
		}
		if staleComment(c.Resolved, c.CommitID, subject.HeadSHA) {
			stale++
			continue
		}
		processed++
		f, verdict, lowc := classifyComment(c.Author, c.Body, c.Path, c.Line, model)
		v.Findings = append(v.Findings, f)
		if verdict == "" { // extraction failed or out-of-enum: unreadable, escalate
			lowConf++
			continue
		}
		v.Confidence = min(v.Confidence, f.Confidence)
		if verdict == "actionable" {
			actionable++
		}
		// A low-confidence extraction normally escalates. Exception: codex's fixed
		// clean-pass sentinel is a deterministic clean signal, so an uncertain
		// non-actionable ("none") extraction of it must not park a clean review.
		// The model still READS the whole body, and an ACTIONABLE verdict is NEVER
		// rescued — so a comment that leads clean then raises a concern (or any
		// finding the model reads) still escalates. The rescue only overrides the
		// CONFIDENCE penalty on a model-confirmed-clean codex sentinel comment.
		rescued := verdict != "actionable" && cleanReviewSentinel(c.Author, c.Body, c.Path)
		if lowc && !rescued {
			lowConf++
		}
		// A no-problem comment (approval, ship-it, findings-resolved note) may
		// still quote a severity badge; it raises nothing.
		if verdict == "none" {
			continue
		}
		if severityTier(f.Severity) > tierRank(v.Tier) {
			v.Tier = "T" + fmt.Sprint(severityTier(f.Severity))
		}
	}

	suffix := ""
	if stale > 0 {
		suffix = fmt.Sprintf(" (%d stale/resolved comments from earlier cycles excluded)", stale)
	}
	switch {
	case processed == 0:
		// An empty panel is not a reviewed panel: a PR opened minutes ago,
		// before any bot has run, must not read as consolidated. Escalate
		// (the local rung's fail-closed) rather than pass — a judge can
		// confirm the panel is genuinely empty. An all-stale panel lands here
		// too: every recorded finding predates this head, so the judged head
		// has no live review yet.
		v.Decision = DecisionEscalate
		v.Why = "no bot review comments for this head — cannot consolidate a panel" + suffix
	case actionable > 0 || lowConf > 0:
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf("%d bot comments: %d actionable, %d low-confidence extractions — needs judgment%s", processed, actionable, lowConf, suffix)
	default:
		v.Why = fmt.Sprintf("%d bot comments, none actionable (nits, questions, or no-problem)%s", processed, suffix)
	}
	return Record(st, run, []string{commentsEvidenceID}, v)
}

// classifyComment extracts one bot comment into a Finding and its verdict. A
// failed or out-of-enum extraction (the cloud schema is a steer, not a grammar)
// returns an empty verdict with an explanatory finding — the caller treats that
// as a low-confidence, escalate-worthy signal, never as "not actionable". The
// third return is whether a readable extraction was below the confidence gate.
func classifyComment(author, body, path string, line int, model Model) (Finding, string, bool) {
	ex, err := extractOne(context.Background(), body, model)
	if err != nil {
		return Finding{Title: "extraction failed: " + err.Error(), Locus: locus(path, line)}, "", false
	}
	if !knownVerdict(ex.Verdict) {
		return Finding{Title: "extraction failed: out-of-enum verdict " + ex.Verdict, Locus: locus(path, line)}, "", false
	}
	f := Finding{
		Title:      fmt.Sprintf("[%s] %s (%s)", strings.TrimSuffix(author, "[bot]"), ex.Headline, ex.Verdict),
		Severity:   normSeverity(ex.Severity),
		Locus:      locus(path, line),
		Confidence: ex.Confidence,
	}
	return f, ex.Verdict, ex.Confidence < confidenceGate
}

// cleanReviewSentinel recognizes codex's fixed CLEAN-pass boilerplate — a
// deterministic "no findings" signal that must not be second-guessed by an
// uncertain model extraction, so a genuinely clean, reviewed PR does not park on
// a low-confidence extraction of an obviously-clean signal (the difference
// between the enforced check being usable and freezing every clean PR).
//
// The match is deliberately narrow on three axes, so it can never swallow a real
// finding — the fail-open an earlier, broader cut would have allowed:
//   - AUTHOR: only codex's exact login. The "this phrase means clean" invariant
//     is codex-specific; cursor/Copilot DO fold findings into prose comments, so
//     the phrase must never be honored from them.
//   - ISSUE-LEVEL ONLY (empty path): codex posts findings as separate INLINE
//     comments (which carry a path); its issue-level summary carries none. A
//     comment with a path is a potential finding and always goes to the model.
//   - ANCHORED: the trimmed body must START WITH the boilerplate, not merely
//     contain it, so a finding appended after a reassuring lead-in cannot hide.
func cleanReviewSentinel(author, body, path string) bool {
	if author != codexLogin || path != "" {
		return false
	}
	trimmed := strings.TrimSpace(body)
	for _, s := range cleanReviewSentinels {
		if strings.HasPrefix(trimmed, s) {
			return true
		}
	}
	return false
}

const codexLogin = "chatgpt-codex-connector[bot]"

var cleanReviewSentinels = []string{
	"Codex Review: Didn't find any major issues", // codex clean-pass issue comment
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
