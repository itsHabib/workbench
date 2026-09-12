package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/standup/internal/standup"
	"github.com/itsHabib/workbench/filelock"
)

func draftPlan(c standup.Card) standup.Draft {
	return standup.Draft{Cards: []standup.Card{c}, Roles: []standup.Role{}, Decisions: []standup.Decision{}, Deferred: []standup.Deferred{}}
}

func draftCall(t *testing.T, id, digest string, d standup.Draft) int {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runInput([]string{"draft", id, "--expect", digest, "--file", "-"}, bytes.NewReader(b), &out, &errOut)
	t.Log(out.String(), errOut.String())
	return code
}

func TestPrepareBeforeConfirmationDoesNotWrite(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	before, _ := os.ReadFile(path)
	out := r.must(0, "prepare", id)
	if !strings.Contains(out, `"plan_valid": true`) || !strings.Contains(out, "no deliver.json") {
		t.Fatal(out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || r.callLog() != "" {
		t.Fatal("prepare mutated record or Fleet")
	}
	r.must(1, "apply", id)
	r.setFile("work.json", `[{"repo":"ivy-12345678","change":"feat/seven","relationship":"draft","for":"author:other","state":"idle"}]`)
	out = r.must(1, "prepare", id)
	if !strings.Contains(out, "world moved") || !strings.Contains(out, `"plan_valid": false`) {
		t.Fatal(out)
	}
	if r.callLog() != "" {
		t.Fatal("stale prepare wrote Fleet")
	}
}

func TestDraftVersionAndConfirmation(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	initial := r.load(path).PlanDigest()
	if code := draftCall(t, id, initial, draftPlan(card)); code != 0 {
		t.Fatal(code)
	}
	first := r.load(path).PlanDigest()
	if code := draftCall(t, id, initial, draftPlan(card)); code != 1 {
		t.Fatal("stale edit accepted")
	}
	r.must(1, "confirm", id, "--expect", initial, "--phrase", "ship it")
	r.must(0, "confirm", id, "--expect", first, "--phrase", "ship it")
	edited := card
	edited.Brief = "Check only the docs and report evidence."
	if code := draftCall(t, id, first, draftPlan(edited)); code != 0 {
		t.Fatal(code)
	}
	if r.load(path).Confirm != nil {
		t.Fatal("edit retained confirmation")
	}
	r.must(1, "apply", id)
	r.must(0, "confirm", id, "--phrase", "ship it")
	r.must(1, "apply", id, "--expect", first)
	current := r.load(path).PlanDigest()
	r.must(0, "apply", id, "--expect", current)
	before := r.callLog()
	r.must(0, "apply", id, "--expect", current)
	if r.callLog() != before {
		t.Fatal("repeated apply repeated effects")
	}
	if code := draftCall(t, id, current, draftPlan(card)); code != 1 {
		t.Fatal("rewrote execution history")
	}
}

func TestDraftRejectsProtectedOrUnknownFields(t *testing.T) {
	for _, input := range []string{
		`{"confirm":{"phrase":"ship it"}}`,
		`{"tenant":"other"}`, `{"applied":[]}`, `{"cards":[{"typo":true}]}`,
		`{} {}`, `{"roles":[]} trailing`,
	} {
		if _, err := standup.DecodeDraft(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}

func TestStatusUsesReceiptsNotRunningWorker(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.setFile("runtime.json", fmt.Sprintf(`{"watcher":"running","workers":[{"address":"ivy-author-1","tenant":"acme","cwd":%q,"state":"running"},{"address":"ivy-author-1","tenant":"other","cwd":%q,"state":"running","secret":"other tenant"}]}`, r.seatDir, r.seatDir))
	out := r.must(0, "status", id)
	if !strings.Contains(out, `"receipt_state": "pending"`) || strings.Contains(out, "other tenant") {
		t.Fatal(out)
	}
	r.setFile("done.json", `{"ok":true,"sha":"abcdef","kinds":{"draft":{"verdict":"pass"}}}`)
	r.setFile("done.code", "0")
	if out = r.must(0, "status", id); !strings.Contains(out, `"receipt_state": "complete"`) {
		t.Fatal(out)
	}
	r.setFile("done.json", `{"ok":false,"sha":"abcdef","failed":["draft"]}`)
	r.setFile("done.code", "3")
	if out = r.must(0, "status", id); !strings.Contains(out, `"receipt_state": "failed"`) {
		t.Fatal(out)
	}
	r.setFile("done.json", `not json`)
	if out = r.must(0, "status", id); !strings.Contains(out, `"receipt_state": "unknown"`) {
		t.Fatal(out)
	}
	if r.callLog() != "" {
		t.Fatal("status wrote Fleet")
	}
}

func TestMCPConversation(t *testing.T) {
	r := newRig(t)
	call := func(name string, args map[string]any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(args)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		result, err := callTool(toolCall{Name: name, Arguments: fields})
		if err != nil {
			t.Fatal(err)
		}
		content := result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		var envelope map[string]any
		if err := json.Unmarshal([]byte(content), &envelope); err != nil {
			t.Fatal(err)
		}
		if int(envelope["exit_code"].(float64)) != want {
			t.Fatalf("%s: %s", name, content)
		}
		data, _ := envelope["data"].(map[string]any)
		return data
	}
	agenda := call("standup_agenda", map[string]any{}, 0)
	id := agenda["id"].(string)
	view := call("standup_new", map[string]any{"agenda": id}, 0)
	view = call("standup_draft", map[string]any{"record": id, "expect": view["plan_digest"], "plan": draftPlan(card)}, 0)
	call("standup_prepare", map[string]any{"record": id}, 0)
	call("standup_apply", map[string]any{"record": id, "expect": view["plan_digest"]}, 1)
	call("standup_confirm", map[string]any{"record": id, "expect": view["plan_digest"], "phrase": "sounds good", "surface": "voice"}, 1)
	if r.callLog() != "" {
		t.Fatal("Fleet effect before confirmation")
	}
	call("standup_show", map[string]any{"record": id}, 0)
	call("standup_confirm", map[string]any{"record": id, "expect": view["plan_digest"], "phrase": "ship it", "surface": "voice"}, 0)
	call("standup_apply", map[string]any{"record": id, "expect": view["plan_digest"]}, 0)
	call("standup_status", map[string]any{"record": id}, 0)
	if !strings.Contains(r.callLog(), "send ivy-author-1") {
		t.Fatal(r.callLog())
	}
	confirmation := r.load(filepath.Join(r.standup, "records", id+".json")).Confirm
	if confirmation.Surface != "voice" || confirmation.By != "human:acme" {
		t.Fatal("lost surface or incorrect attribution")
	}
}

func TestMCPWireAndInvalidArguments(t *testing.T) {
	newRig(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"standup_show","arguments":{"record":"../../config"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"standup_apply","arguments":{"record":"x"}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"standup_show","arguments":{"record":"x","cwd":"/tmp"}}}
`
	var out, errOut bytes.Buffer
	if code := serveMCP(strings.NewReader(input), &out, &errOut); code != 0 {
		t.Fatal(code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatal(out.String())
	}
	for i, line := range lines {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if i >= 2 && response["error"] == nil {
			t.Fatal("accepted unsafe/invalid arguments", line)
		}
	}
	if !strings.Contains(lines[1], "standup_prepare") || !strings.Contains(lines[0], "instructions") {
		t.Fatal(out.String())
	}
}

func TestStoreLockPreventsCompetingWrites(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	r.must(0, "new", "--agenda", id)
	f, err := os.OpenFile(filepath.Join(r.standup, ".lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := filelock.TryLock(f); err != nil {
		t.Fatal(err)
	}
	if out := r.must(4, "confirm", id, "--phrase", "ship it"); !strings.Contains(out, "store busy") {
		t.Fatal(out)
	}
	r.must(0, "show", id)
	if r.callLog() != "" {
		t.Fatal("locked command wrote Fleet")
	}
}

func TestMCPRejectsFileReferences(t *testing.T) {
	for _, id := range []string{"draft.json", "../draft", "/tmp/draft", "--help"} {
		raw, _ := json.Marshal(id)
		_, err := validateTool(map[string]json.RawMessage{"record": raw}, toolSpecs["standup_show"])
		if err == nil {
			t.Fatal("accepted path-like record", id)
		}
	}
}

func TestStatusSurvivesMissingRoleMap(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	checkout := card
	checkout.ID = "checkout"
	checkout.Seat = ""
	checkout.Checkout = r.seatDir
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card, checkout} })
	if err := os.Remove(filepath.Join(os.Getenv("ORG_STATE"), "roles.map")); err != nil {
		t.Fatal(err)
	}
	out := r.must(0, "status", id)
	if !strings.Contains(out, "seat lookup unavailable") || !strings.Contains(out, `"receipt_state": "pending"`) || !strings.Contains(out, `"runtime"`) {
		t.Fatal(out)
	}
	r.edit(path, func(rec *standup.Record) { rec.Cards = nil })
	r.must(0, "status", id)
}

func TestPreparePreservesLedgerAndScopesDelivery(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.must(0, "confirm", id, "--phrase", "ship it")
	r.must(0, "apply", id)
	r.setFile("runtime.json", fmt.Sprintf(`{"watcher":"running","workers":[{"address":"ivy-author-1","tenant":"other","cwd":%q,"configuration_error":"private-other-tenant"},{"address":"ivy-author-1","cwd":%q,"starts_paused":true}]}`, r.seatDir, r.seatDir))
	out := r.must(0, "prepare", id)
	if !strings.Contains(out, `"status": "done"`) || !strings.Contains(out, "starts are paused") || strings.Contains(out, "private-other-tenant") {
		t.Fatal(out)
	}
	if by := r.load(path).Confirm.By; by != "human:acme" {
		t.Fatal(by)
	}
}

func TestPrepareRepeatsUnavailableAgendaSources(t *testing.T) {
	r := newRig(t)
	script := filepath.Join(r.fake, "org")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho source-offline >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := agendaID(t, r.must(0, "agenda"))
	r.must(0, "new", "--agenda", id)
	if out := r.must(0, "prepare", id); !strings.Contains(out, "org status unavailable in pinned agenda: source-offline") {
		t.Fatal(out)
	}
}

func TestForeignRecordMutationRefused(t *testing.T) {
	for _, field := range []string{"tenant", "lead"} {
		t.Run(field, func(t *testing.T) {
			r := newRig(t)
			id := agendaID(t, r.must(0, "agenda"))
			path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
			r.edit(path, func(rec *standup.Record) {
				rec.Cards = []standup.Card{card}
				if field == "tenant" {
					rec.Tenant = "foreign"
					return
				}
				rec.Lead = "lead:foreign"
			})
			before, _ := os.ReadFile(path)
			digest := r.load(path).PlanDigest()
			if draftCall(t, id, digest, draftPlan(card)) != 1 {
				t.Fatal("foreign draft accepted")
			}
			r.must(1, "confirm", id, "--expect", digest, "--phrase", "ship it")
			assertForeignMCPRefused(t, id, digest)
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("foreign record changed")
			}
		})
	}
}

func TestNullDraftPreservesRecord(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	before, _ := os.ReadFile(path)
	var out bytes.Buffer
	code := runInput([]string{"draft", id, "--expect", r.load(path).PlanDigest(), "--file", "-"}, strings.NewReader("null"), &out, &out)
	if code == 0 {
		t.Fatal("null accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("null cleared record")
	}
}

func assertForeignMCPRefused(t *testing.T, id, digest string) {
	t.Helper()
	for _, name := range []string{"standup_draft", "standup_confirm"} {
		args := map[string]any{"record": id, "expect": digest}
		if name == "standup_draft" {
			args["plan"] = draftPlan(card)
		}
		if name == "standup_confirm" {
			args["phrase"] = "ship it"
			args["surface"] = "text"
		}
		raw, _ := json.Marshal(args)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		result, err := callTool(toolCall{Name: name, Arguments: fields})
		if err != nil {
			t.Fatal(err)
		}
		if result.(map[string]any)["isError"] != true {
			t.Fatalf("%s accepted foreign record", name)
		}
	}

}
