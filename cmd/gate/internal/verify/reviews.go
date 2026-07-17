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

	extractPrompt = `You EXTRACT structure from ONE AI code-review comment. Do NOT judge whether it is valid or already handled. Read off: (headline) the bot's OWN title or first line, cleaned of markdown, badges, and HTML comments — quote its words, do not paraphrase; (severity) the severity the bot itself stated (High/Medium/P1/P2), else "unknown"; (verdict) actionable if it reports a problem, nit if trivial or style, question if it asks something; (confidence) a 0.0-1.0 estimate that you extracted headline and severity correctly. Output JSON only.`
)

var extractSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "headline":   {"type": "string"},
    "severity":   {"type": "string"},
    "verdict":    {"type": "string", "enum": ["actionable", "nit", "question"]},
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
	return v == "actionable" || v == "nit" || v == "question"
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
			Author string `json:"author"`
			IsBot  bool   `json:"is_bot"`
			Path   string `json:"path"`
			Line   int    `json:"line"`
			Body   string `json:"body"`
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
	actionable, lowConf, processed := 0, 0, 0
	for _, c := range body.Comments {
		if !c.IsBot || strings.Contains(c.Body, "review-coordinator-verdict") {
			continue
		}
		processed++
		ex, err := extractOne(context.Background(), c.Body, model)
		if err != nil {
			lowConf++
			v.Findings = append(v.Findings, Finding{Title: "extraction failed: " + err.Error(), Locus: locus(c.Path, c.Line)})
			continue
		}
		// An out-of-enum verdict from the cloud backend (whose schema is a
		// steer, not a grammar) is an unreadable extraction: escalate exactly
		// as a failed one, never fall through as "not actionable".
		if !knownVerdict(ex.Verdict) {
			lowConf++
			v.Findings = append(v.Findings, Finding{Title: "extraction failed: out-of-enum verdict " + ex.Verdict, Locus: locus(c.Path, c.Line)})
			continue
		}
		f := Finding{
			Title:      fmt.Sprintf("[%s] %s (%s)", strings.TrimSuffix(c.Author, "[bot]"), ex.Headline, ex.Verdict),
			Severity:   normSeverity(ex.Severity),
			Locus:      locus(c.Path, c.Line),
			Confidence: ex.Confidence,
		}
		v.Findings = append(v.Findings, f)
		if ex.Confidence < v.Confidence {
			v.Confidence = ex.Confidence
		}
		if ex.Verdict == "actionable" {
			actionable++
		}
		if ex.Confidence < confidenceGate {
			lowConf++
		}
		if severityTier(f.Severity) > tierRank(v.Tier) {
			v.Tier = "T" + fmt.Sprint(severityTier(f.Severity))
		}
	}

	switch {
	case processed == 0:
		// An empty panel is not a reviewed panel: a PR opened minutes ago,
		// before any bot has run, must not read as consolidated. Escalate
		// (the local rung's fail-closed) rather than pass — a judge can
		// confirm the panel is genuinely empty.
		v.Decision = DecisionEscalate
		v.Why = "no bot review comments yet — cannot consolidate a panel"
	case actionable > 0 || lowConf > 0:
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf("%d bot comments: %d actionable, %d low-confidence extractions — needs judgment", processed, actionable, lowConf)
	default:
		v.Why = fmt.Sprintf("%d bot comments, all nits/questions", processed)
	}
	return Record(st, run, []string{commentsEvidenceID}, v)
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
