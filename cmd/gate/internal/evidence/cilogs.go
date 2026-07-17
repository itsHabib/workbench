package evidence

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// Failed-run log trimming mirrors the vendored eval extractor the shipped
// classification numbers were measured on: group lines by job/step, strip the
// timestamp prefix, keep each failed step's tail, cap the whole per-run
// excerpt — later steps win, because errors live at the end.
const (
	ciTailLines = 60   // per-step line tail; causes sit at a step's end
	ciMaxChars  = 8000 // per-run byte cap; a small model degrades past this
	ciMaxRuns   = 3    // most-recent red runs fetched; more is an infra smell
	// ciListLimit widens gh run list past its default of 20: a busy head's
	// red runs must not silently fall outside the listing window before the
	// red filter and the ciMaxRuns cap ever see them.
	ciListLimit = "100"
)

// ciTimestampRe strips an optional BOM plus the ISO-8601 timestamp GitHub
// Actions prefixes onto every log line.
var ciTimestampRe = regexp.MustCompile(`^\x{FEFF}?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z ?`)

// CIChunk is one failed step's trimmed log excerpt. Truncated is set whenever
// the line tail or the byte cap dropped content, so no trim is silent.
type CIChunk struct {
	Step      string `json:"step"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// CIRun is one red workflow run's failed-step excerpts. A run recorded with
// zero chunks means GitHub returned no failed-step log for it — recorded, not
// skipped, so the verifier can escalate the absence.
type CIRun struct {
	ID           int64     `json:"id"`
	Workflow     string    `json:"workflow"`
	Conclusion   string    `json:"conclusion"`
	Chunks       []CIChunk `json:"chunks"`
	DroppedSteps int       `json:"dropped_steps,omitempty"`
}

type ciLogsBody struct {
	PR          PRRef   `json:"pr"`
	Runs        []CIRun `json:"runs"`
	OmittedRuns int     `json:"omitted_runs,omitempty"`
	ListError   string  `json:"list_error,omitempty"`
}

// redConclusion reports whether a workflow-run conclusion is a red terminal
// state worth classifying. A bare --status failure filter would miss
// timed-out, startup-failure, and cancelled runs — prime flake/infra material.
func redConclusion(c string) bool {
	switch c {
	case "failure", "startup_failure", "timed_out", "cancelled":
		return true
	}
	return false
}

// FailedRunLogs resolves the head's red workflow runs, fetches each one's
// failed-step log, trims it, and records one evidence artifact. Mechanism
// only — no judging; the ci-classify verifier reads the artifact, never gh.
// A failed or unparseable run listing is recorded (list_error) rather than
// returned: this rung is enrichment, and the gating evidence is already
// gathered — the verifier escalates the recorded failure instead of the
// whole gate run aborting on it.
func FailedRunLogs(st *state.Store, run string, pr PRRef, headSHA string) (string, error) {
	raw, err := gh("run", "list", "-R", pr.Repo, "--commit", headSHA, "-L", ciListLimit, "--json", "databaseId,workflowName,conclusion")
	if err != nil {
		return recordCILogs(st, run, ciLogsBody{PR: pr, ListError: err.Error()})
	}
	var listed []struct {
		DatabaseID int64  `json:"databaseId"`
		Workflow   string `json:"workflowName"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return recordCILogs(st, run, ciLogsBody{PR: pr, ListError: fmt.Sprintf("parse run list: %v", err)})
	}

	body := ciLogsBody{PR: pr}
	seen := map[int64]bool{}
	for _, r := range listed {
		if !redConclusion(r.Conclusion) || seen[r.DatabaseID] {
			continue
		}
		seen[r.DatabaseID] = true
		if len(body.Runs) == ciMaxRuns {
			body.OmittedRuns++
			continue
		}
		body.Runs = append(body.Runs, fetchRunChunks(pr.Repo, r.DatabaseID, r.Workflow, r.Conclusion))
	}
	return recordCILogs(st, run, body)
}

func recordCILogs(st *state.Store, run string, body ciLogsBody) (string, error) {
	a, err := st.Append(state.KindEvidence, run, nil, body)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// fetchRunChunks reads one run's failed-step log. GitHub sometimes has no log
// for a red run and gh exits non-zero for it; that records the run with zero
// chunks rather than failing the whole gate — absence is the verifier's to
// escalate, and an escalation can never read as green.
func fetchRunChunks(repo string, id int64, workflow, conclusion string) CIRun {
	out := CIRun{ID: id, Workflow: workflow, Conclusion: conclusion}
	raw, err := gh("run", "view", fmt.Sprint(id), "-R", repo, "--log-failed")
	if err != nil {
		return out
	}
	out.Chunks, out.DroppedSteps = chunkFailedLog(string(raw))
	return out
}

// chunkFailedLog turns a --log-failed transcript into per-step excerpts:
// lines group by their job/step prefix, each step keeps its final ciTailLines
// lines with the timestamp prefix stripped, then the run keeps its final
// ciMaxChars bytes.
func chunkFailedLog(raw string) ([]CIChunk, int) {
	byStep := map[string][]string{}
	var order []string
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSuffix(line, "\r"), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		key := parts[0] + " / " + parts[1]
		if _, ok := byStep[key]; !ok {
			order = append(order, key)
		}
		byStep[key] = append(byStep[key], ciTimestampRe.ReplaceAllString(parts[2], ""))
	}

	var chunks []CIChunk
	for _, key := range order {
		lines := byStep[key]
		trimmed := false
		if len(lines) > ciTailLines {
			lines = lines[len(lines)-ciTailLines:]
			trimmed = true
		}
		chunks = append(chunks, CIChunk{Step: key, Text: strings.Join(lines, "\n"), Truncated: trimmed})
	}
	return capRunBytes(chunks)
}

// capRunBytes enforces the per-run byte budget from the newest step backward:
// a partially fitting step keeps only the tail of its text, older steps drop
// entirely. Returns the surviving chunks and how many whole steps were
// dropped — counted, never silently gone.
func capRunBytes(chunks []CIChunk) ([]CIChunk, int) {
	budget := ciMaxChars
	cut := 0
	for i := len(chunks) - 1; i >= 0; i-- {
		if budget == 0 {
			cut = i + 1
			break
		}
		if len(chunks[i].Text) > budget {
			chunks[i].Text = chunks[i].Text[len(chunks[i].Text)-budget:]
			chunks[i].Truncated = true
		}
		budget -= len(chunks[i].Text)
	}
	return chunks[cut:], cut
}
