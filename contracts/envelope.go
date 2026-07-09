package contracts

import (
	"encoding/json"
	"time"
)

// Envelope is the append-only artifact wrapper every workbench producer writes
// to its JSONL log: a hash-chained record (Prev links the chain, Hash seals the
// line) whose Body is one per-kind payload. Only Kind == KindVerdict reads
// against Verdict; other kinds carry their own shapes and must never be required
// to look like a verdict.
//
// Read it tolerantly. Body stays raw until a reader knows the kind, and
// json.Unmarshal ignores unknown fields — so a producer may add a field without
// breaking a reader that predates it.
type Envelope struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Run     string          `json:"run"`
	Time    time.Time       `json:"time"`
	Parents []string        `json:"parents,omitempty"`
	Body    json.RawMessage `json:"body"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

// Artifact kinds. A reader dispatches on Kind before touching Body; only
// KindVerdict reads against Verdict.
const (
	KindEvidence   = "evidence"
	KindVerdict    = "verdict"
	KindGrant      = "grant"
	KindAction     = "action"
	KindEscalation = "escalation"
	KindJudgment   = "judgment"
)

// Verdict decodes the envelope body as a Verdict when the envelope carries one.
//
// ok reports whether Kind names a verdict at all, so a caller can dispatch on
// the kind without reaching into Body itself: a non-verdict envelope returns
// ok=false and no error. A verdict envelope whose body will not parse returns
// ok=true and the decode error. Decoding is tolerant — unknown fields are
// ignored and absent optional fields stay zero.
func (e Envelope) Verdict() (Verdict, bool, error) {
	if e.Kind != KindVerdict {
		return Verdict{}, false, nil
	}
	var v Verdict
	if err := json.Unmarshal(e.Body, &v); err != nil {
		return Verdict{}, true, err
	}
	return v, true, nil
}
