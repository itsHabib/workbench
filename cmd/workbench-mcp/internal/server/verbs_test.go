package server

import (
	"encoding/json"
	"strings"
	"testing"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/driverstate"
)

// importEvent is a minimal valid run_imported event body for one stream. A
// minted (run-omitted) import must carry generated_at, so the minimal fixture
// does too.
func importEvent(stream, actor string) json.RawMessage {
	return importEventKeyed(stream, actor, "2026-07-16T12:00:00Z/"+stream+"/"+actor)
}

// importEventKeyed is importEvent carrying a generated_at, so Append's
// (repo, source, generated_at) dedupe key is present — the retried-import case.
func importEventKeyed(stream, actor, generatedAt string) json.RawMessage {
	body := dsc.RunImportedBody{
		Repo:        "itsHabib/workbench",
		Source:      "driver.md",
		GeneratedAt: generatedAt,
		Manifest:    json.RawMessage(`{}`),
		Streams:     []dsc.StreamSpec{{Stream: stream, DocPath: "docs/x.md"}},
	}
	return event(dsc.KindRunImported, "", actor, body)
}

// event builds a record event JSON with a fixed id/time so tests are
// deterministic (the client mints these; supplying them pins the hash).
func event(kind dsc.Kind, stream, actor string, body any) json.RawMessage {
	bodyRaw, _ := json.Marshal(body)
	e := map[string]any{
		"id":    "evt_" + string(kind) + "_" + stream,
		"kind":  string(kind),
		"actor": actor,
		"time":  "2026-07-16T00:00:00Z",
		"body":  json.RawMessage(bodyRaw),
	}
	if stream != "" {
		e["stream"] = stream
	}
	raw, _ := json.Marshal(e)
	return raw
}

// callRecord drives a driver_record through the full tools/call path and returns
// the decoded tool result.
func callRecord(t *testing.T, s *Server, run string, ev json.RawMessage) toolResult {
	t.Helper()
	args, _ := json.Marshal(recordParams{Run: run, Event: ev})
	return callVerb(t, s, "driver_record", args)
}

func callVerb(t *testing.T, s *Server, name string, args json.RawMessage) toolResult {
	t.Helper()
	params, _ := json.Marshal(toolCallParams{Name: name, Arguments: args})
	req := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params}
	resp := s.dispatch(req)
	if resp.Error != nil {
		t.Fatalf("%s returned rpc error: %+v", name, resp.Error)
	}
	tr, ok := resp.Result.(toolResult)
	if !ok {
		t.Fatalf("%s result is not a toolResult: %T", name, resp.Result)
	}
	return tr
}

// resultText returns the single text content block's payload.
func resultText(t *testing.T, tr toolResult) string {
	t.Helper()
	if len(tr.Content) != 1 || tr.Content[0].Type != "text" {
		t.Fatalf("want one text content block, got %+v", tr.Content)
	}
	return tr.Content[0].Text
}

func TestRecordHappyPathMintsRunAndReturnsSealedEvent(t *testing.T) {
	s := New(t.TempDir())
	tr := callRecord(t, s, "", importEvent("dss_a", "session:x"))
	if tr.IsError {
		t.Fatalf("record errored: %s", resultText(t, tr))
	}
	var sealed driverstate.Event
	if err := json.Unmarshal([]byte(resultText(t, tr)), &sealed); err != nil {
		t.Fatalf("decode sealed event: %v", err)
	}
	if !strings.HasPrefix(sealed.Run, "dsr_") {
		t.Fatalf("run was not minted: %q", sealed.Run)
	}
	if sealed.Hash == "" || sealed.Prev != "" {
		t.Fatalf("first event chain wrong: prev=%q hash=%q", sealed.Prev, sealed.Hash)
	}
}

func TestRecordFullLifecycleThroughVerbs(t *testing.T) {
	s := New(t.TempDir())
	// Import mints the run.
	tr := callRecord(t, s, "", importEvent("dss_a", "session:x"))
	run := mustRun(t, tr)

	// Dispatch → terminal attempt (landed) → PR opened → merged.
	callOK(t, s, run, event(dsc.KindStreamDispatched, "dss_a", "session:x", struct{}{}))
	callOK(t, s, run, event(dsc.KindStreamAttempt, "dss_a", "session:x",
		dsc.StreamAttemptBody{Seq: 1, DocPath: "docs/x.md", Terminal: true}))
	callOK(t, s, run, event(dsc.KindStreamPROpened, "dss_a", "session:x",
		dsc.StreamPROpenedBody{PR: 12, URL: "http://pr/12", HeadSHA: "abc"}))
	callOK(t, s, run, event(dsc.KindStreamMerged, "dss_a", "session:x",
		dsc.StreamMergedBody{PR: 12, MergeCommit: "def", MergedAt: "2026-07-16T01:00:00Z"}))

	// State reflects the merged stream.
	stArgs, _ := json.Marshal(runParam{Run: run})
	state := callVerb(t, s, "driver_state", stArgs)
	var rs dsc.RunState
	if err := json.Unmarshal([]byte(resultText(t, state)), &rs); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if got := rs.Streams["dss_a"].Status; got != dsc.StatusMerged {
		t.Fatalf("stream status = %q, want merged", got)
	}
}

func TestRecordIllegalTransitionSurfacesStructuredError(t *testing.T) {
	s := New(t.TempDir())
	tr := callRecord(t, s, "", importEvent("dss_a", "session:x"))
	run := mustRun(t, tr)

	// stream_merged on a dispatched-only stream is illegal (spec §7 F2).
	callOK(t, s, run, event(dsc.KindStreamDispatched, "dss_a", "session:x", struct{}{}))
	bad := callRecord(t, s, run, event(dsc.KindStreamMerged, "dss_a", "session:x",
		dsc.StreamMergedBody{PR: 1, MergeCommit: "x", MergedAt: "2026-07-16T02:00:00Z"}))
	if !bad.IsError {
		t.Fatalf("expected isError result, got %s", resultText(t, bad))
	}
	var ve verbErrorPayload
	if err := json.Unmarshal([]byte(resultText(t, bad)), &ve); err != nil {
		t.Fatalf("decode verb error: %v", err)
	}
	if ve.Code != "ErrIllegalTransition" || ve.Event != string(dsc.KindStreamMerged) {
		t.Fatalf("wrong structured error: %+v", ve)
	}
}

func TestRecordLockedSurfacesHolder(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	tr := callRecord(t, s, "", importEvent("dss_a", "session:x"))
	run := mustRun(t, tr)

	// The server holds the lease from the import. Drop it, then let a different
	// writer take a live lease on the run: the session's next record must fail
	// fast with ErrLocked naming that holder (spec §7 F4).
	s.releaseAll()
	other, err := driverstate.Claim(dir, run, "session:other")
	if err != nil {
		t.Fatalf("other claim: %v", err)
	}
	defer other.Release()

	bad := callRecord(t, s, run, event(dsc.KindStreamDispatched, "dss_a", "session:x", struct{}{}))
	if !bad.IsError {
		t.Fatalf("expected locked error, got %s", resultText(t, bad))
	}
	var ve verbErrorPayload
	_ = json.Unmarshal([]byte(resultText(t, bad)), &ve)
	if ve.Code != "ErrLocked" || ve.Holder != "session:other" {
		t.Fatalf("wrong locked error: %+v", ve)
	}
}

func TestVerifyDetectsBrokenChain(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	tr := callRecord(t, s, "", importEvent("dss_a", "session:x"))
	run := mustRun(t, tr)
	s.releaseAll()

	// Corrupt the ledger tail with a well-formed but chain-breaking line.
	corruptLedger(t, dir, run)

	args, _ := json.Marshal(runParam{Run: run})
	got := callVerb(t, s, "driver_verify", args)
	if !got.IsError {
		t.Fatalf("expected chain-broken error, got %s", resultText(t, got))
	}
	var ve verbErrorPayload
	_ = json.Unmarshal([]byte(resultText(t, got)), &ve)
	if ve.Code != "ErrChainBroken" {
		t.Fatalf("want ErrChainBroken, got %+v", ve)
	}
}

func TestRunsLiveFilterExcludesFinished(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	// Open run: import only (stays open).
	callRecord(t, s, "", importEvent("dss_a", "session:x"))

	// Finished run: import → skip the one stream → run_finished.
	tr := callRecord(t, s, "", importEvent("dss_b", "session:y"))
	run := mustRun(t, tr)
	callOK(t, s, run, event(dsc.KindStreamSkipped, "dss_b", "session:y", struct{}{}))
	callOK(t, s, run, event(dsc.KindRunFinished, "", "session:y", struct{}{}))

	args, _ := json.Marshal(runsParams{Live: true})
	got := callVerb(t, s, "driver_runs", args)
	var summaries []driverstate.RunSummary
	if err := json.Unmarshal([]byte(resultText(t, got)), &summaries); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Status != driverstate.RunStatusOpen {
		t.Fatalf("live filter wrong: %+v", summaries)
	}
}

// mustRun extracts the minted run id from a successful record result.
func mustRun(t *testing.T, tr toolResult) string {
	t.Helper()
	if tr.IsError {
		t.Fatalf("record errored: %s", resultText(t, tr))
	}
	var sealed driverstate.Event
	if err := json.Unmarshal([]byte(resultText(t, tr)), &sealed); err != nil {
		t.Fatalf("decode sealed: %v", err)
	}
	return sealed.Run
}

// callOK records an event and fails the test if it did not succeed.
func callOK(t *testing.T, s *Server, run string, ev json.RawMessage) {
	t.Helper()
	tr := callRecord(t, s, run, ev)
	if tr.IsError {
		t.Fatalf("record failed: %s", resultText(t, tr))
	}
}

// A minted import with no generated_at is refused: without the full dedupe key
// a retry could never be recognized and every attempt would mint a second run.
func TestRecordMintedImportRequiresGeneratedAt(t *testing.T) {
	s := New(t.TempDir())
	body := dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "driver.md",
		Manifest: json.RawMessage(`{}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_1", DocPath: "docs/x.md"}},
	}
	res := callRecord(t, s, "", event(dsc.KindRunImported, "", "session:t", body))
	if !res.IsError {
		t.Fatal("want generated_at rejection, got success")
	}
}
