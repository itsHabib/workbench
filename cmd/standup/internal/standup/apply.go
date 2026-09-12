package standup

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Refusal is apply saying no with the reason. It is exit 1 at the CLI: the record
// is not ready, and the message says exactly what would make it ready.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

func refuse(f string, a ...any) error { return &Refusal{Reason: fmt.Sprintf(f, a...)} }

// IsRefusal reports whether err is apply declining rather than failing.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}

// ConfirmRecord sets confirm when phrase matches the configured one. The comparison
// is code: trimmed, case-folded, exact. Anything else leaves confirm nil and is a
// refusal, so a model relaying "sounds good" cannot commit the operator.
func ConfirmRecord(e Env, cfg Config, r *Record, phrase, by, surface string) error {
	if err := CheckIdentity(cfg, r); err != nil {
		return err
	}
	if r.Confirm != nil {
		return refuse("record %s is already confirmed by %s at %s", r.ID, r.Confirm.By, r.Confirm.At)
	}
	if !strings.EqualFold(strings.TrimSpace(phrase), strings.TrimSpace(cfg.Phrase)) {
		return refuse("phrase did not match; confirm stays null")
	}
	if by == "" {
		by = "human:" + cfg.Tenant
	}
	if surface == "" {
		surface = "text"
	}
	r.Confirm = &Confirm{By: by, Phrase: cfg.Phrase, At: e.Now().UTC().Format("2006-01-02T15:04:05Z"), Surface: surface, PlanDigest: r.PlanDigest()}
	return nil
}

// checkConfirm is apply's first gate: a confirm exists, carries the configured
// phrase, and binds the plan as it stands now. A record edited after the readback
// is a different plan and goes back to the operator.
func checkConfirm(cfg Config, r *Record) error {
	switch {
	case r.Confirm == nil:
		return refuse("record %s is not confirmed; nothing is written until the phrase sets confirm", r.ID)
	case r.Confirm.Phrase != cfg.Phrase:
		return refuse("record %s carries a confirm for a phrase that is not the configured one; confirm it again", r.ID)
	case r.Confirm.PlanDigest != r.PlanDigest():
		return refuse("record %s was edited after it was confirmed (plan %s, confirmed %s); read it back and confirm again", r.ID, short(r.PlanDigest()), short(r.Confirm.PlanDigest))
	}
	return nil
}

// Step is one verb apply will run, in order. Status is what happened to it:
// plan (dry run), done (on the ledger from an earlier apply), skip (the world
// already carries its effect), ran, failed, or not reached.
type Step struct {
	Name   string   `json:"name"`           // stable step id, e.g. card standup-…-c1 dispatch
	Dir    string   `json:"dir"`            // where the verb runs
	Bin    string   `json:"bin"`            // fleet
	Args   []string `json:"args"`           // the verb and its flags
	Skip   string   `json:"skip,omitempty"` // set when the world already carries this step's effect
	Status string   `json:"status"`
}

// Plan turns the record into steps against the live world: pools for roles, then
// per card a dispatch and a send, then decisions. It refuses before any write when
// a kind has no manifest, a seat is not in roles.map, or a card's row already exists
// under a different accountable role — a changed payload under the same identity.
func Plan(e Env, cfg Config, r *Record, world *World) ([]Step, error) {
	var steps []Step
	roles := map[string]Role{}
	for _, ro := range r.Roles {
		if !e.KindExists(ro.Kind) {
			return nil, refuse("roles: kind %q has no manifest under %s; write %s/manifest.json and card.md first", ro.Kind, e.Lanes, ro.Kind)
		}
		if _, dup := roles[ro.Role]; dup {
			return nil, refuse("roles: %s appears twice; one entry per role", ro.Role)
		}
		roles[ro.Role] = ro
		if ro.Seats == 0 {
			continue
		}
		world.planSeats(ro)
		steps = append(steps, Step{Name: "role " + ro.Role + " pool", Dir: ro.Checkout, Bin: e.Fleet,
			Args: []string{"pool", ro.Checkout, ro.Kind, fmt.Sprint(ro.Seats), "--tenant", cfg.Tenant}})
	}
	claimed := map[string]string{}
	for _, c := range r.Cards {
		dir, err := cardDir(e, c, world)
		if err != nil {
			return nil, err
		}
		steps = append(steps, branchSteps(e, c, dir, world)...)
		branch, err := world.BranchOf(e, c)
		if err != nil {
			return nil, err
		}
		key := repoName(c.Repo) + " " + branch + " " + c.As
		if other, dup := claimed[key]; dup {
			return nil, refuse("cards %s and %s both name %s %s/%s; one row, one card", other, c.ID, c.Repo, branch, c.As)
		}
		claimed[key] = c.ID
		dispatch := Step{Name: "card " + c.ID + " dispatch", Dir: dir, Bin: e.Fleet,
			Args: []string{"dispatch", c.Change, "--as", c.As, "--for", c.For, "--due", c.Due, "--brief", c.Brief}}
		if c.Seat != "" {
			dispatch.Args = append(dispatch.Args, "--slot", c.Seat)
		}
		row, err := world.rowFor(c, key)
		if err != nil {
			return nil, err
		}
		if row != nil {
			if row.For != c.For {
				return nil, refuse("card %s: a row for %s %s/%s already names %s accountable, not %s; reassign it or change the card", c.ID, c.Repo, branch, c.As, row.For, c.For)
			}
			dispatch.Skip = "row exists for " + row.For
		}
		steps = append(steps, dispatch)
		if c.Seat != "" {
			body := fmt.Sprintf("%s\n\nrepo %s · change %s · done when a passing %s receipt exists at the head · due %s · card %s of %s", c.Brief, c.Repo, c.Change, c.As, c.Due, c.ID, r.ID)
			body += roleTerms(roles[c.For])
			steps = append(steps, Step{Name: "card " + c.ID + " send", Dir: e.LeadDir, Bin: e.Fleet,
				Args: []string{"send", c.Seat, "--id", c.ID, "--kind", "order", "--subject", c.Repo + " " + c.Change, "--body", body}})
		}
	}
	for i, d := range r.Decisions {
		step := Step{Name: fmt.Sprintf("decision %d", i+1), Dir: e.LeadDir, Bin: e.Fleet, Args: []string{"decide", d.Kind, d.Subject, d.Text}}
		if world.Decided[d.Kind+" "+d.Subject+": "+d.Text] {
			step.Skip = "already decided"
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// cardDir resolves where a card's verbs run and proves the checkout is a clone of
// the card's repository. Fleet and git take the repository from the directory, so
// a seat copied from another card would push and dispatch in the wrong place while
// the record claimed this one.
func cardDir(e Env, c Card, world *World) (string, error) {
	if c.Seat != "" && c.Checkout != "" {
		return "", refuse("card %s: names both seat %q and checkout %q; Fleet places the row in the seat, so name one", c.ID, c.Seat, c.Checkout)
	}
	dir, probe := c.Checkout, c.Checkout
	if dir == "" {
		p, ok := world.Seats[c.Seat]
		if !ok {
			return "", refuse("card %s: seat %q is not in %s and no roles[] entry pools it; pool it first or name a checkout", c.ID, c.Seat, e.RolesMap)
		}
		dir, probe = p, p
		// A seat a planned pool will create does not exist yet; its origin is
		// the checkout it is pooled beside.
		if from, planned := world.Planned[c.Seat]; planned {
			probe = from
		}
	}
	origin := world.OriginRepo(e, probe)
	if origin == "" {
		return "", refuse("card %s: cannot read the origin of %s; is it a clone with a remote?", c.ID, probe)
	}
	if !strings.EqualFold(origin, c.Repo) {
		return "", refuse("card %s: %s is a clone of %s, not %s; name the right seat or fix the card", c.ID, probe, origin, c.Repo)
	}
	return dir, nil
}

// roleTerms renders a role's instructions and terms for the mail body, so the
// worker receives them until Fleet's per-role definition store has a reader.
func roleTerms(ro Role) string {
	var b strings.Builder
	if ro.Instructions != "" {
		b.WriteString("\n\nrole instructions: " + ro.Instructions)
	}
	if len(ro.Terms) > 0 {
		t, _ := json.Marshal(ro.Terms)
		b.WriteString("\nrole terms: " + string(t))
	}
	return b.String()
}

// branchSteps makes a card's change exist before dispatch. A pull request number
// already has a branch; a branch name may be new work, and Fleet refuses to declare
// a row for a branch it cannot see. So: fetch, then create the branch on origin from
// the repository's default branch when it is not there. The seat's checkout is never
// switched; the worker checks the branch out itself when its order arrives.
func branchSteps(e Env, c Card, dir string, world *World) []Step {
	if strings.HasPrefix(c.Change, "#") {
		return nil
	}
	fetch := Step{Name: "card " + c.ID + " fetch", Dir: dir, Bin: "git", Args: []string{"fetch", "origin", "--quiet"}}
	create := Step{Name: "card " + c.ID + " branch", Dir: dir, Bin: "git",
		Args: []string{"push", "origin", "refs/remotes/origin/" + world.DefaultBranch(e, dir) + ":refs/heads/" + c.Change}}
	if world.BranchOnOrigin(e, dir, c.Change) {
		create.Skip = "branch exists on origin"
	}
	return []Step{fetch, create}
}

// World is the part of live state apply plans against.
type World struct {
	Rows     map[string][]Row  // "repo-name branch relationship" → rows (one per Fleet repo id)
	Seats    map[string]string // seat → directory, from roles.map
	SeatRepo map[string]string // seat → Fleet repo id, learned from rows placed in it
	Planned  map[string]string // seat → the checkout a planned pool creates it beside
	Decided  map[string]bool
}

// Row is the declared part of a dispatch row, with Fleet's own repository id.
type Row struct {
	Repo string
	For  string
}

// planSeats adds the seats a pool step will create: <basename>-<kind>-<i>, beside
// the checkout. Fleet names them deterministically, so a card may name one before
// the pool has run.
func (w *World) planSeats(ro Role) {
	base := filepath.Base(ro.Checkout)
	for i := 1; i <= ro.Seats; i++ {
		seat := fmt.Sprintf("%s-%s-%d", base, ro.Kind, i)
		if _, exists := w.Seats[seat]; exists {
			continue
		}
		w.Seats[seat] = filepath.Join(filepath.Dir(ro.Checkout), seat)
		w.Planned[seat] = ro.Checkout
	}
}

// BranchOf is the branch a card's change names: itself, or for #<n> the pull
// request's head branch, which is how Fleet keys the row it will write.
func (w *World) BranchOf(e Env, c Card) (string, error) {
	n, ok := strings.CutPrefix(c.Change, "#")
	if !ok {
		return c.Change, nil
	}
	// Asked from the lead's directory: -R names the repository, and a planned
	// seat may not exist yet.
	res := e.Run.Run(e.LeadDir, e.GH, "pr", "view", n, "-R", c.Repo, "--json", "headRefName", "-q", ".headRefName")
	branch := strings.TrimSpace(res.Stdout)
	if res.Err != nil || res.Code != 0 || branch == "" {
		return "", refuse("card %s: cannot resolve %s in %s to its branch (%s); Fleet keys rows by branch, so the ownership check needs it", c.ID, c.Change, c.Repo, strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return branch, nil
}

// rowFor is the existing row a card would collide with, or nil. Fleet's repository
// id is a basename plus a hash of the checkout's git directory, which this tool
// does not recompute. A seat's id is learned from any row or receipt Fleet has
// already recorded in that seat, and then the match is exact. With no id on
// record and no same-named row, there is nothing to collide with; with no id and
// a same-named row, the row cannot be proven to be this repository's, and an
// unproven match is refused rather than guessed. The verb that would close this
// is a Fleet read of a path's id (FOLLOWUPS.md).
func (w *World) rowFor(c Card, key string) (*Row, error) {
	rows := w.Rows[key]
	if len(rows) == 0 {
		return nil, nil
	}
	id, known := w.SeatRepo[c.Seat]
	if !known || c.Seat == "" {
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.Repo + " (accountable " + r.For + ")"
		}
		return nil, refuse("card %s: a row for %s/%s exists in %s, and %s has no Fleet repository identity on record yet, so it cannot be proven to be %s's row. Name a seat Fleet has already placed work in, or reassign or retire that row first", c.ID, strings.SplitN(key, " ", 2)[1], c.As, strings.Join(ids, " and "), seatOrCheckout(c), c.Repo)
	}
	for i := range rows {
		if rows[i].Repo == id {
			return &rows[i], nil
		}
	}
	return nil, nil
}

func seatOrCheckout(c Card) string {
	if c.Seat != "" {
		return "seat " + c.Seat
	}
	return "checkout " + c.Checkout
}

// BranchOnOrigin asks the remote, from the seat's checkout, whether the branch exists.
// A remote that cannot be reached reads as absent, and the create step then says why.
func (w *World) BranchOnOrigin(e Env, dir, branch string) bool {
	res := e.Run.Run(dir, "git", "ls-remote", "--heads", "origin", branch)
	return res.Err == nil && res.Code == 0 && strings.Contains(res.Stdout, "refs/heads/"+branch)
}

// DefaultBranch is what origin's HEAD points at, or main when it does not say.
func (w *World) DefaultBranch(e Env, dir string) string {
	res := e.Run.Run(dir, "git", "ls-remote", "--symref", "origin", "HEAD")
	for _, line := range strings.Split(res.Stdout, "\n") {
		rest, ok := strings.CutPrefix(line, "ref: refs/heads/")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			return f[0]
		}
	}
	return "main"
}

// OriginRepo is the owner/name of a checkout's origin, from its URL in either the
// https or the ssh form, or empty when there is no readable origin.
func (w *World) OriginRepo(e Env, dir string) string {
	res := e.Run.Run(dir, "git", "remote", "get-url", "origin")
	if res.Err != nil || res.Code != 0 {
		return ""
	}
	u := strings.TrimSuffix(strings.TrimSpace(res.Stdout), ".git")
	u = strings.ReplaceAll(u, ":", "/")
	parts := strings.Split(strings.Trim(u, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// ReadWorld gathers rows, seats and decisions. Rows are keyed the way the record
// names them; fleet's repo id carries a hash suffix, so the match is on basename.
func ReadWorld(e Env, cfg Config) (*World, error) {
	w := &World{Rows: map[string][]Row{}, SeatRepo: map[string]string{}, Planned: map[string]string{}, Decided: map[string]bool{}}
	seats, err := e.Seats(cfg.Tenant)
	if err != nil {
		return nil, fmt.Errorf("roles.map: %w", err)
	}
	w.Seats = seats
	res := e.Run.Run(e.LeadDir, e.Fleet, "work", "--json")
	if res.Err != nil {
		return nil, res.Err
	}
	if res.Code != 0 {
		// An unreadable ownership view is not an empty fleet: planning against it
		// would let a dispatch replace a row nobody could see.
		return nil, fmt.Errorf("fleet work --json exited %d: %s", res.Code, strings.TrimSpace(res.Stderr+res.Stdout))
	}
	rows, err := parseRows(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("fleet work --json: %w", err)
	}
	for _, r := range rows {
		key := repoBase(str(r, "repo")) + " " + str(r, "change") + " " + str(r, "relationship")
		w.Rows[key] = append(w.Rows[key], Row{Repo: str(r, "repo"), For: str(r, "for")})
		w.learnSeat(r)
	}
	// Receipts carry the seat and repository id of the session that wrote them:
	// a second source of a seat's identity, for seats whose rows are done and gone.
	res = e.Run.Run(e.LeadDir, e.Fleet, "receipts", "--json")
	if res.Err == nil && res.Code == 0 {
		receipts, err := parseRows(res.Stdout)
		if err != nil {
			return nil, fmt.Errorf("fleet receipts --json: %w", err)
		}
		for _, r := range receipts {
			w.learnSeat(r)
		}
	}
	decided, err := e.decisions()
	if err != nil {
		return nil, err
	}
	w.Decided = decided
	return w, nil
}

// decisions is the ledger of decisions in force, keyed the way Plan names a
// decision step. It reads `fleet decisions --json`: the human table pads its
// columns, so a line never equals "kind subject: text" and the already-decided
// skip could not fire, and a carried-over decision was appended a second time.
func (e Env) decisions() (map[string]bool, error) {
	res := e.Run.Run(e.LeadDir, e.Fleet, "decisions", "--json")
	if res.Err != nil {
		return nil, fmt.Errorf("fleet decisions --json: %w", res.Err)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("fleet decisions --json: exit %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &rows); err != nil {
		return nil, fmt.Errorf("fleet decisions --json: %w (is fleet from the same checkout?)", err)
	}
	out := map[string]bool{}
	for _, d := range rows {
		out[str(d, "kind")+" "+str(d, "subject")+": "+str(d, "text")] = true
	}
	return out, nil
}

// learnSeat records a seat's Fleet repository id from a row or receipt placed in it.
func (w *World) learnSeat(r map[string]any) {
	if slot, repo := str(r, "slot"), str(r, "repo"); slot != "" && repo != "" {
		w.SeatRepo[slot] = repo
	}
}

// repoName is the name half of owner/name: what a Fleet row carries as its repo.
func repoName(repo string) string {
	if i := strings.IndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// repoBase strips fleet's hash suffix: "ivy-5ab57ce6" → "ivy". A card's repo is
// owner/name; rows match on name because that is all the local record carries.
func repoBase(repo string) string {
	if i := strings.LastIndexByte(repo, '-'); i > 0 && len(repo)-i-1 == 8 {
		return repo[:i]
	}
	return repo
}

// Apply refuses an unconfirmed or stale record, plans, then runs each step not
// already in applied[], saving the record after every one so a crash leaves a
// truthful ledger. A step that exits non-zero stops the run; the next apply resumes
// after the last step that succeeded.
func Apply(e Env, cfg Config, r *Record, path string, dryRun, forceStale bool) ([]Step, error) {
	if err := checkConfirm(cfg, r); err != nil {
		return nil, err
	}
	steps, done, moved, err := prepareSteps(e, cfg, r, forceStale)
	if err != nil {
		return nil, err
	}
	markStatus(steps, done, dryRun)
	if dryRun {
		return steps, nil
	}
	if moved != "" {
		if err := recordForced(e, r, path, r.Agenda, moved); err != nil {
			return steps, err
		}
	}
	for i := range steps {
		s := &steps[i]
		if done[s.Name] {
			continue
		}
		rec := Applied{Step: s.Name, Verb: s.Bin, Dir: s.Dir, Args: s.Args, At: e.Now().UTC().Format("2006-01-02T15:04:05Z"), Skip: s.Skip}
		if s.Skip == "" {
			res := e.Run.Run(s.Dir, s.Bin, s.Args...)
			rec.Code = res.Code
			rec.Output = strings.TrimSpace(res.Stdout + res.Stderr)
			if res.Err != nil {
				rec.Code = 4
				rec.Output = res.Err.Error()
			}
			s.Status = "ran"
		}
		r.Applied = append(r.Applied, rec)
		if err := r.Save(path); err != nil {
			return steps, err
		}
		if rec.Code != 0 {
			s.Status = "failed"
			return steps, fmt.Errorf("%s exited %d: %s\nfix the cause and re-run apply; steps before it are recorded and will not repeat", s.Name, rec.Code, rec.Output)
		}
	}
	return steps, nil
}

// markStatus is each step's status before anything runs: plan on a dry run, done
// when the ledger carries it, skip when the world does, else not reached.
func markStatus(steps []Step, done map[string]bool, dryRun bool) {
	for i := range steps {
		switch {
		case dryRun:
			steps[i].Status = "plan"
		case done[steps[i].Name]:
			steps[i].Status = "done"
		case steps[i].Skip != "":
			steps[i].Status = "skip"
		default:
			steps[i].Status = "not reached"
		}
	}
}

// recordForced puts a forced apply on the record before anything runs, so a
// ledger read later says the world had moved and the operator went ahead anyway.
func recordForced(e Env, r *Record, path, agendaID, moved string) error {
	r.Applied = append(r.Applied, Applied{Step: "apply --force-stale", Verb: "standup", Args: []string{"apply", "--force-stale"},
		Output: moved, At: e.Now().UTC().Format("2006-01-02T15:04:05Z"), Skip: "forced past a world that moved since agenda " + agendaID})
	return r.Save(path)
}

// alreadyDone is the set of planned steps the ledger already carries with exit 0.
// A ledger entry whose directory or arguments differ from the plan is a refusal:
// the record was edited after that step ran, and repeating or skipping it would
// both leave the ledger claiming something that did not happen.
func alreadyDone(r *Record, steps []Step) (map[string]bool, error) {
	ledger := map[string]Applied{}
	for _, a := range r.Applied {
		if a.Code == 0 {
			ledger[a.Step] = a
		}
	}
	done := map[string]bool{}
	for _, s := range steps {
		a, ok := ledger[s.Name]
		if !ok {
			continue
		}
		if a.Dir != s.Dir || !slices.Equal(a.Args, s.Args) {
			return nil, refuse("step %q already ran with different arguments; the record or roles.map changed after apply. Start a fresh record (standup new --from) instead of editing this one", s.Name)
		}
		done[s.Name] = true
	}
	return done, nil
}

func diffLines(before, after []string) string {
	was := map[string]bool{}
	for _, l := range before {
		was[l] = true
	}
	is := map[string]bool{}
	for _, l := range after {
		is[l] = true
	}
	var b strings.Builder
	for _, l := range before {
		if !is[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range after {
		if !was[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// prepareSteps is the shared read-only planning path. Apply adds confirmation and
// effects; Prepare never manufactures a confirmation to reach this path.
func prepareSteps(e Env, cfg Config, r *Record, forceStale bool) ([]Step, map[string]bool, string, error) {
	if err := r.Validate(); err != nil {
		return nil, nil, "", err
	}
	if err := CheckIdentity(cfg, r); err != nil {
		return nil, nil, "", err
	}
	agenda, err := LoadAgenda(e.AgendaPath(r.Agenda))
	if err != nil {
		return nil, nil, "", fmt.Errorf("agenda %s: %w", r.Agenda, err)
	}
	if agenda.Digest != r.AgendaDigest {
		return nil, nil, "", refuse("record %s was made against agenda digest %s but %s carries %s", r.ID, short(r.AgendaDigest), r.Agenda, short(agenda.Digest))
	}
	live, err := e.Build(cfg, agenda.ID)
	if err != nil {
		return nil, nil, "", err
	}
	moved := ""
	if live.Digest != agenda.Digest {
		moved = diffLines(agenda.Projection, live.Projection)
	}
	if moved != "" && !forceStale {
		return nil, nil, "", refuse("the world moved since agenda %s:\n%s\nre-run standup agenda and re-plan, or apply --force-stale to proceed anyway", agenda.ID, moved)
	}
	world, err := ReadWorld(e, cfg)
	if err != nil {
		return nil, nil, "", err
	}
	steps, err := Plan(e, cfg, r, world)
	if err != nil {
		return nil, nil, "", err
	}
	done, err := alreadyDone(r, steps)
	if err != nil {
		return nil, nil, "", err
	}
	return steps, done, moved, nil
}

// CheckIdentity confines record mutations to the configured tenant and lead.
func CheckIdentity(cfg Config, r *Record) error {
	if r.Tenant != cfg.Tenant || r.Lead != cfg.Lead {
		return refuse("record identity differs from configured tenant/lead")
	}
	return nil
}
