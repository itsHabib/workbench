package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/standup/internal/standup"
)

// rig is a fake world: fleet, org and gh are shell scripts that answer from files
// under fake/ and log every write verb to calls.log with the directory it ran in.
type rig struct {
	t       *testing.T
	fake    string
	leadDir string
	seatDir string
	standup string
	calls   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are sh scripts")
	}
	root := t.TempDir()
	r := &rig{t: t, fake: filepath.Join(root, "fake"), leadDir: filepath.Join(root, "lead-acme"),
		seatDir: filepath.Join(root, "ivy-author-1"), standup: filepath.Join(root, "standup")}
	r.calls = filepath.Join(r.fake, "calls.log")
	for _, d := range []string{r.fake, r.leadDir, r.seatDir, r.standup, filepath.Join(root, "fleet", "lanes", "author"), filepath.Join(root, "org")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.fake, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "fleet", "lanes", "author", "manifest.json"), `{"kind":"author","card":"card.md"}`)
	write(filepath.Join(root, "org", "roles.map"), r.leadDir+" acme lead:acme\n"+r.seatDir+" acme author:ivy ivy-author-1\n")
	write(filepath.Join(r.standup, "config.json"), `{"lead":"lead:acme","tenant":"acme","phrase":"ship it","repos":["acme/ivy"]}`)
	write(filepath.Join(r.fake, "work.json"), "[]")
	write(filepath.Join(r.fake, "prs.json"), `[{"number":7,"title":"Seven","headRefName":"feat/seven","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"","updatedAt":"2026-09-10T00:00:00Z","url":"u"}]`)
	write(filepath.Join(r.fake, "decisions.txt"), "")
	script("fleet", `case "$1" in
  work) if [ -e "$FAKE/work-fail" ]; then echo "fleet: state unavailable" >&2; exit 1; fi; cat "$FAKE/work.json" ;;
  receipts) if [ -e "$FAKE/receipts.json" ]; then cat "$FAKE/receipts.json"; else echo '[]'; fi ;;
  mail) echo '[{"id":"q1","kind":"question","from":"ivy-author-1","subject":"which unit?"}]' ;;
  decisions) cat "$FAKE/decisions.txt" ;;
  dispatch) printf '%s|%s\n' "$PWD" "$*" >> "$FAKE/calls.log"; if [ -e "$FAKE/dispatch-fail" ]; then echo "fleet dispatch: seat occupied" >&2; exit 1; fi; echo ok ;;
  pool) printf '%s|%s\n' "$PWD" "$*" >> "$FAKE/calls.log"; mkdir -p "$(dirname "$2")/$(basename "$2")-$3-1"; echo ok ;;
  send|decide) printf '%s|%s\n' "$PWD" "$(printf '%s' "$*" | tr '\n' ' ')" >> "$FAKE/calls.log"; echo ok ;;
  *) echo "fake fleet: $1" >&2; exit 2 ;;
esac`)
	script("git", `case "$1 $2" in
  "fetch origin") printf '%s|git %s\n' "$PWD" "$*" >> "$FAKE/calls.log" ;;
  "ls-remote --symref") printf 'ref: refs/heads/trunk\tHEAD\n' ;;
  "ls-remote --heads") grep -qx "$4" "$FAKE/branches.txt" 2>/dev/null && printf 'abc\trefs/heads/%s\n' "$4" ;;
  "push origin") printf '%s|git %s\n' "$PWD" "$*" >> "$FAKE/calls.log" ;;
  "remote get-url") cat "$FAKE/origin.txt" ;;
  *) echo "fake git: $*" >&2; exit 2 ;;
esac`)
	script("org", `echo '[{"tenant":"","role":"","card":""},{"tenant":"acme","role":"steward:ivy","card":"/cards/steward-ivy.md","parent":"lead:acme"}]'`)
	script("gh", `case "$1 $2" in
  "pr list") cat "$FAKE/prs.json" ;;
  "pr view") cat "$FAKE/pr7.txt" ;;
  *) echo "fake gh: $*" >&2; exit 2 ;;
esac`)
	write(filepath.Join(r.fake, "branches.txt"), "")
	write(filepath.Join(r.fake, "pr7.txt"), "feat/seven\n")
	write(filepath.Join(r.fake, "origin.txt"), "git@github.com:acme/ivy.git\n")
	t.Setenv("FAKE", r.fake)
	t.Setenv("PATH", r.fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FLEET_BIN", filepath.Join(r.fake, "fleet"))
	t.Setenv("ORG_BIN", filepath.Join(r.fake, "org"))
	t.Setenv("GH_BIN", filepath.Join(r.fake, "gh"))
	t.Setenv("FLEET_STATE", filepath.Join(root, "fleet"))
	t.Setenv("ORG_STATE", filepath.Join(root, "org"))
	t.Setenv("STANDUP_DIR", r.standup)
	t.Chdir(r.leadDir)
	return r
}

func (r *rig) run(args ...string) (int, string, string) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (r *rig) must(want int, args ...string) string {
	r.t.Helper()
	code, out, errOut := r.run(args...)
	if code != want {
		r.t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errOut)
	}
	return out + errOut
}

func (r *rig) callLog() string {
	b, _ := os.ReadFile(r.calls)
	return string(b)
}

func (r *rig) setFile(name, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.fake, name), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) edit(path string, f func(*standup.Record)) {
	r.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	var rec standup.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		r.t.Fatal(err)
	}
	f(&rec)
	if err := rec.Save(path); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) load(path string) *standup.Record {
	r.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	var rec standup.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		r.t.Fatal(err)
	}
	return &rec
}

// agendaID parses "agenda <id> written to" from stderr.
func agendaID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "agenda standup-") && strings.Contains(line, " written to ") {
			return strings.Fields(line)[1]
		}
	}
	t.Fatalf("no agenda id in output:\n%s", out)
	return ""
}

var card = standup.Card{ID: "c1", Repo: "acme/ivy", Change: "#7", As: "draft", For: "author:ivy", Seat: "ivy-author-1", Due: "2h", Brief: "Answer the open review threads at the head and report the SHA."}

func TestAgendaIsStableAcrossRuns(t *testing.T) {
	r := newRig(t)
	first := agendaID(t, r.must(0, "agenda"))
	second := agendaID(t, r.must(0, "agenda"))
	if first == second {
		t.Fatalf("ids must advance: %s twice", first)
	}
	a1, err := standup.LoadAgenda(filepath.Join(r.standup, "agenda", first+".json"))
	if err != nil {
		t.Fatal(err)
	}
	a2, err := standup.LoadAgenda(filepath.Join(r.standup, "agenda", second+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if a1.Digest != a2.Digest {
		t.Fatalf("same world, different digests:\n%v\n%v", a1.Projection, a2.Projection)
	}
	text := r.must(0, "agenda")
	for _, want := range []string{"## fleet mail (1)", "which unit?", "## org status (1)", "steward:ivy parent=lead:acme card=/cards/steward-ivy.md", "## gh pr list acme/ivy (1)", "#7 feat/seven"} {
		if !strings.Contains(text, want) {
			t.Errorf("agenda text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "parent=- card=-") {
		t.Error("placeholder org rows must be dropped")
	}
}

func TestConfirmIsCodeNotModel(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	if !strings.Contains(r.must(0, "show", path), "confirm: not yet") {
		t.Fatal("fresh record must read back as unconfirmed")
	}
	r.must(1, "apply", path)
	if got := r.callLog(); got != "" {
		t.Fatalf("unconfirmed apply wrote: %s", got)
	}
	r.must(1, "confirm", path, "--phrase", "sounds good")
	if r.load(path).Confirm != nil {
		t.Fatal("a near miss must not confirm")
	}
	r.must(0, "confirm", path, "--phrase", "  Ship It ")
	c := r.load(path).Confirm
	if c == nil || c.By != "human:acme" || c.Surface != "text" || c.PlanDigest == "" {
		t.Fatalf("confirm = %+v", c)
	}
	r.must(1, "confirm", path, "--phrase", "ship it")

	// An edit after the readback is a different plan: apply sends it back.
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "edited after it was confirmed") || r.callLog() != "" {
		t.Fatalf("edited-after-confirm: %s", out)
	}
	// Re-pointing a confirmed record at a fresh agenda is an edit like any other.
	r.edit(path, func(rec *standup.Record) { rec.Cards, rec.Confirm = nil, nil })
	r.must(0, "confirm", path, "--phrase", "ship it")
	fresh := agendaID(t, r.must(0, "agenda"))
	r.edit(path, func(rec *standup.Record) { rec.Agenda = fresh })
	if out := r.must(1, "apply", path); !strings.Contains(out, "edited after it was confirmed") {
		t.Fatalf("re-pointed agenda: %s", out)
	}
	// A confirm typed in by hand with the wrong phrase is not a confirm.
	r.edit(path, func(rec *standup.Record) {
		rec.Confirm = &standup.Confirm{By: "model", Phrase: "sounds good", At: "now", PlanDigest: rec.PlanDigest()}
	})
	if out := r.must(1, "apply", path); !strings.Contains(out, "not the configured one") || r.callLog() != "" {
		t.Fatalf("forged confirm: %s", out)
	}
}

// planAndApply runs one full standup with the standard card, a role with
// instructions, a decision and a deferral, confirmed and applied. It returns the
// record path and the agenda id.
func planAndApply(t *testing.T, r *rig) (string, string) {
	t.Helper()
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) {
		rec.Roles = []standup.Role{{Role: "author:ivy", Kind: "author", Checkout: r.seatDir, Instructions: "no force pushes", Terms: map[string]any{"scope": []string{"github:acme/ivy"}}}}
		rec.Cards = []standup.Card{card}
		rec.Decisions = []standup.Decision{{Kind: "rule", Subject: "ivy", Text: "two fix-rounds then the judge"}}
		rec.Deferred = []standup.Deferred{{Subject: "billing", Why: "product direction"}}
	})
	r.must(0, "confirm", path, "--phrase", "ship it")
	plan := r.must(0, "apply", path, "--dry-run")
	if r.callLog() != "" {
		t.Fatal("dry run wrote")
	}
	for _, want := range []string{"plan        card c1 dispatch", "plan        card c1 send", "plan        decision 1"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan lacks %q:\n%s", want, plan)
		}
	}
	r.must(0, "apply", path)
	return path, id
}

func TestApplyCompilesCards(t *testing.T) {
	r := newRig(t)
	path, id := planAndApply(t, r)
	calls := strings.Split(strings.TrimSpace(r.callLog()), "\n")
	if len(calls) != 3 {
		t.Fatalf("want 3 calls, got %d:\n%s", len(calls), r.callLog())
	}
	if dir, args, _ := strings.Cut(calls[0], "|"); dir != r.seatDir || !strings.HasPrefix(args, "dispatch #7 --as draft --for author:ivy --due 2h --brief ") || !strings.HasSuffix(args, "--slot ivy-author-1") {
		t.Errorf("dispatch ran as %q in %q", args, dir)
	}
	if dir, args, _ := strings.Cut(calls[1], "|"); dir != r.leadDir || !strings.HasPrefix(args, "send ivy-author-1 --id c1 --kind order --subject acme/ivy #7 --body ") || !strings.Contains(args, "role instructions: no force pushes") || !strings.Contains(args, `role terms: {"scope":["github:acme/ivy"]}`) {
		t.Errorf("send ran as %q in %q", args, dir)
	}
	if _, args, _ := strings.Cut(calls[2], "|"); args != "decide rule ivy two fix-rounds then the judge" {
		t.Errorf("decide ran as %q", args)
	}
	rec := r.load(path)
	if len(rec.Applied) != 3 || rec.Applied[0].Code != 0 || rec.Applied[1].Step != "card c1 send" {
		t.Fatalf("applied = %+v", rec.Applied)
	}
	// The deferral rides into the next agenda.
	next := r.must(0, "agenda")
	if !strings.Contains(next, "deferred from "+id) || !strings.Contains(next, "billing: product direction") {
		t.Errorf("next agenda lacks the deferral:\n%s", next)
	}
}

func TestApplyLedgerIsIdempotentAndGuarded(t *testing.T) {
	r := newRig(t)
	path, _ := planAndApply(t, r)

	// The world now carries the row; a second apply repeats nothing.
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"draft","for":"author:ivy","state":"dispatched","slot":"ivy-author-1","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	r.setFile("decisions.txt", "d1 rule ivy: two fix-rounds then the judge\n")
	r.must(1, "apply", path) // the world moved (a new row): the agenda is stale
	r.must(0, "apply", path, "--force-stale")
	if n := len(strings.Split(strings.TrimSpace(r.callLog()), "\n")); n != 3 {
		t.Fatalf("second apply repeated verbs: %d calls", n)
	}
	forced := r.load(path).Applied
	if len(forced) != 4 || forced[3].Step != "apply --force-stale" || !strings.Contains(forced[3].Output, "+ row repo:ivy-5ab57ce6:feat/seven draft open for=author:ivy") {
		t.Fatalf("a forced apply must be on the ledger: %+v", forced)
	}

	// The seat moved in roles.map: the ledger's dispatch ran elsewhere, so the plan
	// no longer matches what was applied. Refused, not silently skipped.
	moved := r.leadDir + "-moved"
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	rolesMap := filepath.Join(filepath.Dir(r.standup), "org", "roles.map")
	if err := os.WriteFile(rolesMap, []byte(r.leadDir+" acme lead:acme\n"+moved+" acme author:ivy ivy-author-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := r.must(1, "apply", path, "--force-stale"); !strings.Contains(out, "already ran with different arguments") {
		t.Fatalf("moved seat: %s", out)
	}
}

func TestApplyRefusesBeforeWriting(t *testing.T) {
	r := newRig(t)
	fresh := func(edit func(*standup.Record)) string {
		t.Helper()
		id := agendaID(t, r.must(0, "agenda"))
		path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
		r.edit(path, edit)
		r.must(0, "confirm", path, "--phrase", "ship it")
		return path
	}

	// A kind with no manifest.
	path := fresh(func(rec *standup.Record) {
		rec.Roles = []standup.Role{{Role: "wizard:ivy", Kind: "wizard", Checkout: r.seatDir, Seats: 1}}
		rec.Cards = []standup.Card{card}
	})
	out := r.must(1, "apply", path)
	if !strings.Contains(out, `kind "wizard" has no manifest`) || r.callLog() != "" {
		t.Fatalf("unknown kind: %s (calls %q)", out, r.callLog())
	}

	// A seat nobody pooled.
	c := card
	c.Seat = "ivy-author-9"
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{c} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "ivy-author-9") || r.callLog() != "" {
		t.Fatalf("unknown seat: %s", out)
	}

	// The seat is a clone of another repository.
	r.setFile("origin.txt", "https://github.com/acme/other.git\n")
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "is a clone of acme/other, not acme/ivy") || r.callLog() != "" {
		t.Fatalf("wrong origin: %s", out)
	}
	r.setFile("origin.txt", "git@github.com:acme/ivy.git\n")

	// A same-named row with no identity on record for the seat: unproven, refused.
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"draft","for":"author:other","state":"working","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "cannot be proven to be acme/ivy's row") || r.callLog() != "" {
		t.Fatalf("unproven row: %s", out)
	}

	// The same row, once the seat's identity is on record: a changed payload.
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"draft","for":"author:other","state":"working","slot":"ivy-author-1","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "already names author:other accountable") || r.callLog() != "" {
		t.Fatalf("changed payload: %s", out)
	}

	// Two cards for one row.
	r.setFile("work.json", "[]")
	twin := card
	twin.ID = "c2"
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card, twin} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "cards c1 and c2 both name acme/ivy feat/seven/draft") || r.callLog() != "" {
		t.Fatalf("duplicate cards: %s", out)
	}

	// Two repositories named ivy both carry the row: ambiguous, so refused.
	r.setFile("work.json", `[{"repo":"ivy-11111111","change":"feat/seven","relationship":"draft","for":"author:ivy","state":"working","key":"repo:ivy-11111111:feat/seven"},{"repo":"ivy-22222222","change":"feat/seven","relationship":"draft","for":"author:ivy","state":"working","key":"repo:ivy-22222222:feat/seven"}]`)
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	if out := r.must(1, "apply", path); !strings.Contains(out, "ivy-11111111 (accountable author:ivy) and ivy-22222222 (accountable author:ivy)") || r.callLog() != "" {
		t.Fatalf("ambiguous rows: %s", out)
	}
	r.setFile("work.json", "[]")

	// The world moved between agenda and apply: refused with the diff.
	r.setFile("work.json", "[]")
	path = fresh(func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.setFile("prs.json", `[]`)
	out = r.must(1, "apply", path)
	if !strings.Contains(out, "the world moved") || !strings.Contains(out, "- pr acme/ivy#7") || r.callLog() != "" {
		t.Fatalf("stale: %s", out)
	}
}

func TestRecordValidationSaysEverythingAtOnce(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) {
		rec.Cards = []standup.Card{{ID: "bad id", Repo: "ivy", Change: "", As: "Draft", For: "nobody", Due: "soon", Brief: ""}}
		rec.Decisions = []standup.Decision{{Kind: "maybe", Subject: "", Text: ""}}
	})
	out := r.must(4, "show", path)
	for _, want := range []string{"id \"bad id\"", "repo \"ivy\"", "change is required", "as \"Draft\"", "for \"nobody\"", "needs a seat", "due \"soon\"", "brief: one line", "kind \"maybe\""} {
		if !strings.Contains(out, want) {
			t.Errorf("validation lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageAndUnknownVerb(t *testing.T) {
	r := newRig(t)
	if code, _, _ := r.run(); code != 2 {
		t.Fatalf("no verb: exit %d", code)
	}
	if code, _, _ := r.run("frobnicate"); code != 2 {
		t.Fatalf("unknown verb: exit %d", code)
	}
	if code, out, _ := r.run("help"); code != 0 || !strings.Contains(out, "apply <id|path>") {
		t.Fatalf("help: exit %d, %s", code, out)
	}
}

func TestApplyCreatesTheBranchForNewWork(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	c := card
	c.Change = "task/new-work"
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{c} })
	r.must(0, "confirm", path, "--phrase", "ship it")
	r.must(0, "apply", path)
	calls := strings.Split(strings.TrimSpace(r.callLog()), "\n")
	want := []string{
		r.seatDir + "|git fetch origin --quiet",
		r.seatDir + "|git push origin refs/remotes/origin/trunk:refs/heads/task/new-work",
	}
	if len(calls) != 4 || calls[0] != want[0] || calls[1] != want[1] || !strings.HasPrefix(calls[2], r.seatDir+"|dispatch task/new-work ") {
		t.Fatalf("calls:\n%s", r.callLog())
	}

	// Existing branch: fetch still runs, create is skipped, nothing else changes.
	r.setFile("branches.txt", "task/new-work\n")
	r.setFile("calls.log", "")
	id = agendaID(t, r.must(0, "agenda"))
	path = strings.TrimSpace(r.must(0, "new", "--agenda", id))
	c.ID = "c2"
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{c} })
	r.must(0, "confirm", path, "--phrase", "ship it")
	out := r.must(0, "apply", path)
	if !strings.Contains(out, "skip        card c2 branch") || strings.Contains(r.callLog(), "git push") {
		t.Fatalf("existing branch must skip create:\n%s\n%s", out, r.callLog())
	}
}

func TestNewFromCarriesAStaleDraftOver(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) {
		rec.Cards = []standup.Card{card}
		rec.Decisions = []standup.Decision{{Kind: "park", Subject: "rung", Text: "after ivy"}}
		rec.Next = "tomorrow 09:00"
	})
	r.must(0, "confirm", path, "--phrase", "ship it")
	r.setFile("prs.json", "[]") // the world moves
	if out := r.must(1, "apply", path); !strings.Contains(out, "the world moved") {
		t.Fatalf("expected a stale refusal: %s", out)
	}
	id2 := agendaID(t, r.must(0, "agenda"))
	path2 := strings.TrimSpace(r.must(0, "new", "--agenda", id2, "--from", path))
	rec := r.load(path2)
	if rec.Confirm != nil || len(rec.Applied) != 0 {
		t.Fatal("a carried draft must start unconfirmed with nothing applied")
	}
	if len(rec.Cards) != 1 || rec.Cards[0].ID != id2+"-c1" || rec.Cards[0].Brief != card.Brief || len(rec.Decisions) != 1 || rec.Next != "tomorrow 09:00" {
		t.Fatalf("carried record = %+v", rec)
	}
	r.must(0, "confirm", path2, "--phrase", "ship it")
	r.must(0, "apply", path2)
	if !strings.Contains(r.callLog(), "dispatch #7") {
		t.Fatalf("carried card did not apply:\n%s", r.callLog())
	}
}

func TestIdsSortChronologicallyAndRowsCarryTheirRelationship(t *testing.T) {
	r := newRig(t)
	first := agendaID(t, r.must(0, "agenda"))
	if !strings.HasSuffix(first, "-001") {
		t.Fatalf("ids are zero-padded: %s", first)
	}
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"draft","for":"author:ivy","state":"working","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	asDraft := agendaID(t, r.must(0, "agenda"))
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"reviews","for":"author:ivy","state":"working","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	asReviews := agendaID(t, r.must(0, "agenda"))
	a, err := standup.LoadAgenda(filepath.Join(r.standup, "agenda", asDraft+".json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := standup.LoadAgenda(filepath.Join(r.standup, "agenda", asReviews+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == b.Digest {
		t.Fatalf("a row replaced by another relationship must project differently:\n%v", a.Projection)
	}
}

func TestUnreadableFleetIsAnErrorNotAnEmptyWorld(t *testing.T) {
	r := newRig(t)
	r.setFile("work-fail", "")
	id := agendaID(t, r.must(0, "agenda")) // the agenda records the source as unavailable
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.must(0, "confirm", path, "--phrase", "ship it")
	out := r.must(4, "apply", path)
	if !strings.Contains(out, "fleet work --json exited 1: fleet: state unavailable") || r.callLog() != "" {
		t.Fatalf("unreadable fleet: %s (calls %q)", out, r.callLog())
	}
}

func TestReadoutSaysWhatHappenedToEachStep(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) {
		rec.Cards = []standup.Card{card}
		rec.Decisions = []standup.Decision{{Kind: "rule", Subject: "ivy", Text: "one"}}
	})
	r.must(0, "confirm", path, "--phrase", "ship it")
	r.setFile("dispatch-fail", "")
	out := r.must(4, "apply", path)
	for _, want := range []string{"failed      card c1 dispatch", "not reached card c1 send", "not reached decision 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("readout lacks %q:\n%s", want, out)
		}
	}
	if applied := r.load(path).Applied; len(applied) != 1 || applied[0].Code != 1 {
		t.Fatalf("ledger after a failed dispatch = %+v", applied)
	}
	if err := os.Remove(filepath.Join(r.fake, "dispatch-fail")); err != nil {
		t.Fatal(err)
	}
	out = r.must(0, "apply", path)
	for _, want := range []string{"ran         card c1 dispatch", "ran         card c1 send", "ran         decision 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("retry readout lacks %q:\n%s", want, out)
		}
	}
	if out = r.must(0, "apply", path); !strings.Contains(out, "done        card c1 send") {
		t.Errorf("third apply must read the ledger:\n%s", out)
	}
}

func TestLatestRecordOrdersNumerically(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	a, err := standup.LoadAgenda(filepath.Join(r.standup, "agenda", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range []string{"9", "10"} {
		rec := &standup.Record{Schema: standup.SchemaRecord, ID: "standup-2026-09-10-" + seq, Tenant: "acme", Lead: "lead:acme", At: "x",
			Agenda: a.ID, AgendaDigest: a.Digest, Deferred: []standup.Deferred{{Subject: "seq " + seq, Why: "w"}}}
		if err := rec.Save(filepath.Join(r.standup, "records", rec.ID+".json")); err != nil {
			t.Fatal(err)
		}
	}
	next := r.must(0, "agenda")
	if !strings.Contains(next, "deferred from standup-2026-09-10-10") || strings.Contains(next, "seq 9:") {
		t.Fatalf("latest record must be sequence 10:\n%s", next)
	}
}

func TestPlannedPoolSeatsAreDispatchable(t *testing.T) {
	r := newRig(t)
	checkout := filepath.Join(filepath.Dir(r.seatDir), "ivy2")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	c := card
	c.Seat = "ivy2-author-1"
	r.edit(path, func(rec *standup.Record) {
		rec.Roles = []standup.Role{{Role: "author:ivy2", Kind: "author", Checkout: checkout, Seats: 1}}
		rec.Cards = []standup.Card{c}
	})
	r.must(0, "confirm", path, "--phrase", "ship it")
	r.must(0, "apply", path)
	calls := strings.Split(strings.TrimSpace(r.callLog()), "\n")
	if len(calls) != 3 || !strings.HasSuffix(calls[0], "|pool "+checkout+" author 1 --tenant acme") || !strings.HasPrefix(calls[1], checkout+"-author-1|dispatch #7 ") || !strings.Contains(calls[2], "|send ivy2-author-1 ") {
		t.Fatalf("calls:\n%s", r.callLog())
	}
}

func TestFinalRoundRefusals(t *testing.T) {
	r := newRig(t)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))

	// A card naming both a seat and a checkout is not a valid record.
	both := card
	both.Checkout = r.seatDir
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{both} })
	if out := r.must(4, "show", path); !strings.Contains(out, "names both a seat and a checkout") {
		t.Fatalf("both fields: %s", out)
	}

	// Terms are read back, not only bound.
	r.edit(path, func(rec *standup.Record) {
		rec.Cards = []standup.Card{card}
		rec.Roles = []standup.Role{{Role: "author:ivy", Kind: "author", Checkout: r.seatDir, Terms: map[string]any{"scope": []string{"github:acme/ivy"}}},
			{Role: "author:ivy", Kind: "author", Checkout: r.seatDir, Seats: 1}}
	})
	if out := r.must(0, "show", path); !strings.Contains(out, `terms: {"scope":["github:acme/ivy"]}`) {
		t.Fatalf("readback lacks terms:\n%s", out)
	}
	// The same role twice refuses before any write.
	r.must(0, "confirm", path, "--phrase", "ship it")
	if out := r.must(1, "apply", path); !strings.Contains(out, "author:ivy appears twice") || r.callLog() != "" {
		t.Fatalf("duplicate roles: %s", out)
	}
}

func TestSeatRepoIsLearnedFromItsRows(t *testing.T) {
	r := newRig(t)
	// The seat already carries a row, so its Fleet id is known; a same-named
	// repository's row for this branch is not ours and must not be matched.
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"other","relationship":"draft","for":"author:ivy","state":"working","slot":"ivy-author-1","key":"k1"},{"repo":"ivy-22222222","change":"feat/seven","relationship":"draft","for":"author:other","state":"working","key":"k2"}]`)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.must(0, "confirm", path, "--phrase", "ship it")
	out := r.must(0, "apply", path)
	if !strings.Contains(out, "ran         card c1 dispatch") || !strings.Contains(r.callLog(), "dispatch #7") {
		t.Fatalf("the other repository's row must not block this seat:\n%s\n%s", out, r.callLog())
	}
}

func TestSeatRepoIsLearnedFromReceipts(t *testing.T) {
	r := newRig(t)
	// The seat's only trace is a receipt; the row for this branch carries no slot.
	r.setFile("receipts.json", `[{"head":"abc","kind":"draft","verdict":"pass","slot":"ivy-author-1","repo":"ivy-5ab57ce6","role":"author:ivy","observable":"x","at":1}]`)
	r.setFile("work.json", `[{"repo":"ivy-5ab57ce6","change":"feat/seven","relationship":"draft","for":"author:ivy","state":"working","key":"repo:ivy-5ab57ce6:feat/seven"}]`)
	id := agendaID(t, r.must(0, "agenda"))
	path := strings.TrimSpace(r.must(0, "new", "--agenda", id))
	r.edit(path, func(rec *standup.Record) { rec.Cards = []standup.Card{card} })
	r.must(0, "confirm", path, "--phrase", "ship it")
	out := r.must(0, "apply", path)
	if !strings.Contains(out, "skip        card c1 dispatch") || strings.Contains(r.callLog(), "dispatch") {
		t.Fatalf("a proven own row must skip dispatch:\n%s\n%s", out, r.callLog())
	}
}
