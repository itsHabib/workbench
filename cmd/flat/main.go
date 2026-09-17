// Command flat is the substrate for a fleet with no management sessions.
//
// Exit codes: 0 ok, 1 refused (a typed reason on stdout), 2 wait timed out,
// 3 usage or error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flat/internal/flat"
	"github.com/itsHabib/workbench/cmd/flat/internal/poc"
)

const usage = `flat: a fleet substrate with no leads

state       init --operator NAME [--lead NAME] [--verifier NAME]
board       board [--md|--phone|--json] [--fetch] [--base main] [--idle 20m]
            verify BRANCH
            check PATH               is PATH contended by another branch, and is it ruled?
decide      ask --scope a,b --question Q [--options "x|y"] [--needs peer|lead|operator]
            requests [--all] [--json]
            claim ID [--ttl 15m]
            rule ID --ruling R [--evidence E] [--epoch N] [--supersedes DEC]
            decide --scope a,b --ruling R [--evidence E]     rule without a request
            escalate ID --to lead|operator --why W
            wait ID [--timeout 8m]
            decisions [--scope PATH] [--json]
            who SCOPE[,SCOPE]        which seats hold context on a scope (the routing index)
resource    take NAME [--ttl 30m] · drop NAME
admit       admit [--seats N] [--disk-min 10G] [--resource NAME] [--wait] [--json]
watch       watch [--interval 30s] [--once] [--fetch] [--idle 20m] [--unclaimed 10m] [--disk-min 10G]
                  [--wake] [--wake-model M] [--wake-tools LIST] [--wake-max 2]
            digest
inbox       inbox [--keep] · nudge SEAT TEXT · hook · install-hook [--dir REPO]
stats       stats [--threshold 20m] [--json]
poc         poc init DIR · poc run --dir DIR --mode flat|tree [...] · poc stats --run DIR

--as SEAT applies to every verb; default FLAT_SEAT, else the checked-out branch.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(3)
	}
	verb, args := os.Args[1], os.Args[2:]
	if verb == "poc" {
		os.Exit(poc.Main(args))
	}
	if verb == "hook" {
		_ = flat.Hook(os.Stdin, os.Stdout)
		return
	}
	cwd, _ := os.Getwd()
	err := run(verb, args, cwd)
	var ref *flat.Refusal
	switch {
	case err == nil:
	case errors.As(err, &ref):
		fmt.Printf("refused %s: %s\n", ref.Code, ref.Msg)
		if ref.Code == "timeout" {
			os.Exit(2)
		}
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(3)
	}
}

// cli is one parsed invocation. Every verb reads the same flag set, so the
// usage text above is the whole grammar.
type cli struct {
	verb string
	pos  []string
	s    *flat.State
	seat string

	as, scope, question, options, needs, ruling, evidence, supersedes, to, why        string
	operator, lead, verifier, diskMin, resource, dir, cmd, wakeModel, wakeTools, base string
	jsonOut, md, phone, fetch, all, keep, wait, once, wake                            bool
	epoch, seats, wakeMax                                                             int
	ttl, timeout, idle, interval, unclaimed, threshold                                time.Duration
}

func parse(verb string, args []string) (*cli, error) {
	c := &cli{verb: verb}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.StringVar(&c.as, "as", "", "seat acting (default FLAT_SEAT or current branch)")
	fs.BoolVar(&c.jsonOut, "json", false, "JSON output")
	fs.StringVar(&c.base, "base", "main", "base branch")
	fs.StringVar(&c.scope, "scope", "", "comma-separated paths or resource:NAME")
	fs.StringVar(&c.question, "question", "", "the question")
	fs.StringVar(&c.options, "options", "", "pipe-separated options")
	fs.StringVar(&c.needs, "needs", "peer", "tier needed: peer, lead, operator")
	fs.StringVar(&c.ruling, "ruling", "", "ruling text")
	fs.StringVar(&c.evidence, "evidence", "", "what the ruling rests on")
	fs.IntVar(&c.epoch, "epoch", 0, "claim epoch (0 = claim now)")
	fs.StringVar(&c.supersedes, "supersedes", "", "decision id this one replaces")
	fs.StringVar(&c.to, "to", "", "tier to escalate to")
	fs.StringVar(&c.why, "why", "", "reason")
	fs.DurationVar(&c.ttl, "ttl", 0, "lease length")
	fs.DurationVar(&c.timeout, "timeout", 8*time.Minute, "wait timeout")
	fs.BoolVar(&c.md, "md", false, "markdown")
	fs.BoolVar(&c.phone, "phone", false, "phone-sized")
	fs.BoolVar(&c.fetch, "fetch", false, "git fetch first")
	fs.DurationVar(&c.idle, "idle", 20*time.Minute, "silent after")
	fs.BoolVar(&c.all, "all", false, "include ruled requests")
	fs.BoolVar(&c.keep, "keep", false, "do not consume inbox")
	fs.IntVar(&c.seats, "seats", 0, "max working branches")
	fs.StringVar(&c.diskMin, "disk-min", "", "free bytes floor, e.g. 10G")
	fs.StringVar(&c.resource, "resource", "", "resource the new builder needs")
	fs.BoolVar(&c.wait, "wait", false, "block until admitted")
	fs.DurationVar(&c.interval, "interval", 30*time.Second, "watch interval")
	fs.BoolVar(&c.once, "once", false, "one pass")
	fs.DurationVar(&c.unclaimed, "unclaimed", 10*time.Minute, "alert after")
	fs.DurationVar(&c.threshold, "threshold", 20*time.Minute, "unclaimed kill line")
	fs.StringVar(&c.operator, "operator", "", "operator name(s), comma-separated")
	fs.StringVar(&c.lead, "lead", "", "lead name(s)")
	fs.StringVar(&c.verifier, "verifier", "", "verifier name(s)")
	fs.StringVar(&c.dir, "dir", "", "repository directory")
	fs.StringVar(&c.cmd, "cmd", "", "flat binary path for the hook")
	fs.BoolVar(&c.wake, "wake", false, "resume seats that have notes and are between turns")
	fs.StringVar(&c.wakeModel, "wake-model", "", "model for wakes")
	fs.StringVar(&c.wakeTools, "wake-tools", "", "allowed tools for wakes")
	fs.IntVar(&c.wakeMax, "wake-max", 2, "concurrent wakes")

	// Positional args may precede flags: `flat rule ID --ruling ...`.
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		c.pos, args = append(c.pos, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	c.pos = append(c.pos, fs.Args()...)
	return c, nil
}

var verbs = map[string]func(*cli) error{
	"init":         (*cli).initTiers,
	"board":        (*cli).board,
	"verify":       (*cli).verify,
	"check":        (*cli).check,
	"ask":          (*cli).ask,
	"requests":     (*cli).requests,
	"claim":        (*cli).claim,
	"rule":         (*cli).rule,
	"decide":       (*cli).decide,
	"escalate":     (*cli).escalate,
	"wait":         (*cli).waitFor,
	"decisions":    (*cli).decisions,
	"who":          (*cli).who,
	"take":         (*cli).take,
	"drop":         (*cli).drop,
	"admit":        (*cli).admit,
	"watch":        (*cli).watch,
	"digest":       (*cli).digest,
	"inbox":        (*cli).inbox,
	"nudge":        (*cli).nudge,
	"install-hook": (*cli).installHook,
	"stats":        (*cli).stats,
}

func run(verb string, args []string, cwd string) error {
	c, err := parse(verb, args)
	if err != nil {
		return err
	}
	if c.dir == "" {
		c.dir = cwd
	}
	fn, ok := verbs[verb]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown verb %q", verb)
	}
	if c.s, err = flat.Open(cwd); err != nil {
		return err
	}
	c.seat = c.as
	if c.seat == "" {
		c.seat = flat.SeatOf(cwd)
	}
	os.Setenv("FLAT_SEAT", c.seat)
	return fn(c)
}

func (c *cli) arg(i int) string {
	if i < len(c.pos) {
		return c.pos[i]
	}
	return ""
}

func (c *cli) emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// record turns a refusal into an event before returning it.
func (c *cli) record(err error) error {
	var ref *flat.Refusal
	if errors.As(err, &ref) {
		c.s.RecordRefusal(c.seat, c.verb, ref.Code)
	}
	return err
}

func (c *cli) diskFloor() (uint64, error) {
	if c.diskMin == "" {
		return 0, nil
	}
	return flat.ParseBytes(c.diskMin)
}

func (c *cli) boardOpts() flat.BoardOptions {
	return flat.BoardOptions{Base: c.base, Fetch: c.fetch, Idle: c.idle}
}

func split(v, sep string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *cli) initTiers() error {
	return c.s.Init(flat.Tiers{Operator: split(c.operator, ","), Lead: split(c.lead, ","), Verifier: split(c.verifier, ",")})
}

func (c *cli) board() error {
	b, err := c.s.Board(c.boardOpts())
	if err != nil {
		return err
	}
	switch {
	case c.md:
		fmt.Print(b.Markdown())
	case c.phone:
		fmt.Print(b.Phone())
	case c.jsonOut:
		return c.emit(b)
	default:
		fmt.Print(b.JSONL())
	}
	return nil
}

func (c *cli) verify() error {
	b, err := c.s.Board(c.boardOpts())
	if err != nil {
		return err
	}
	for _, r := range b.Rows {
		if r.Branch != c.arg(0) {
			continue
		}
		if c.jsonOut {
			return c.emit(r)
		}
		fmt.Printf("%s %s tip %s", r.Branch, r.State, r.Tip[:8])
		if r.HeadSHA != "" {
			fmt.Printf(" pinned %s", r.HeadSHA[:8])
		}
		if len(r.Extra) > 0 {
			fmt.Printf(" changed after RESULT: %s", strings.Join(r.Extra, ", "))
		}
		fmt.Println()
		if r.State != "landed" {
			return c.record(&flat.Refusal{Code: r.State, Msg: "not landed"})
		}
		return nil
	}
	return fmt.Errorf("no branch %q", c.arg(0))
}

func (c *cli) check() error {
	p := c.arg(0)
	if p == "" {
		return errors.New("check needs a path")
	}
	b, err := c.s.Board(c.boardOpts())
	if err != nil {
		return err
	}
	others := touching(b, p, c.seat)
	ds, err := c.s.Lookup([]string{p})
	if err != nil {
		return err
	}
	switch {
	case len(others) == 0:
		fmt.Printf("clear: no other branch has changed %s\n", p)
	case len(ds) > 0:
		d := ds[len(ds)-1]
		fmt.Printf("contended but ruled: %s also changed by %s. ruling %s by %s (%s): %s\n", p, strings.Join(others, ", "), d.ID, d.By, d.Tier, d.Ruling)
	default:
		fmt.Printf("contended, no ruling: %s is also changed by %s. do not edit it until ruled: flat ask --scope %s --question 'who lands first on %s?' --options '%s|me' then flat wait ID\n", p, strings.Join(others, ", "), p, p, strings.Split(others[0], " ")[0])
		return c.record(&flat.Refusal{Code: "contended_unruled", Msg: p})
	}
	return nil
}

// touching lists the other branches that changed p or something under it.
func touching(b *flat.Board, p, seat string) []string {
	var out []string
	dir := strings.TrimSuffix(p, "/") + "/"
	for _, r := range b.Rows {
		if r.Branch == seat {
			continue
		}
		for _, f := range r.Files {
			if f == p || strings.HasPrefix(f, dir) {
				out = append(out, r.Branch+" ("+r.State+")")
				break
			}
		}
	}
	return out
}

func (c *cli) ask() error {
	r, err := c.s.Ask(c.seat, split(c.scope, ","), c.question, split(c.options, "|"), c.needs)
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(r)
	}
	fmt.Printf("asked %s (needs %s). wait with: flat wait %s\n", r.ID, r.Needs, r.ID)
	return nil
}

func (c *cli) requests() error {
	rs, err := c.s.Requests(!c.all)
	if err != nil {
		return err
	}
	if c.jsonOut {
		return c.emit(rs)
	}
	if len(rs) == 0 {
		fmt.Println("no open requests")
	}
	for _, r := range rs {
		claim := ""
		if r.Claim != nil && r.Status == "claimed" {
			claim = fmt.Sprintf(" claimed by %s (epoch %d)", r.Claim.Holder, r.Claim.Epoch)
		}
		fmt.Printf("%s %s from %s needs %s age %s%s\n  scope %s\n  q: %s", r.ID, r.Status, r.From, r.Needs, ageOf(r.At), claim, strings.Join(r.Scope, ","), r.Question)
		if len(r.Options) > 0 {
			fmt.Printf("\n  options: %s", strings.Join(r.Options, " | "))
		}
		fmt.Println()
	}
	return nil
}

func (c *cli) claim() error {
	r, err := c.s.ClaimRequest(c.arg(0), c.seat, c.ttl)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("claimed %s epoch %d until %s. rule with: flat rule %s --epoch %d --ruling '...' --evidence '...'\n", r.ID, r.Claim.Epoch, r.Claim.Until.Format(time.RFC3339), r.ID, r.Claim.Epoch)
	return nil
}

func (c *cli) rule() error {
	d, err := c.s.Rule(c.arg(0), c.seat, c.epoch, c.ruling, c.evidence, c.supersedes)
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(d)
	}
	fmt.Printf("ruled %s -> %s by %s (%s)\n", c.arg(0), d.ID, d.By, d.Tier)
	return nil
}

func (c *cli) decide() error {
	d, err := c.s.Decide(c.seat, split(c.scope, ","), c.ruling, c.evidence, c.supersedes)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("decided %s by %s (%s) on %s\n", d.ID, d.By, d.Tier, strings.Join(d.Scope, ","))
	return nil
}

func (c *cli) escalate() error {
	r, err := c.s.Escalate(c.arg(0), c.seat, c.to, c.why)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("escalated %s to %s\n", r.ID, r.Needs)
	return nil
}

func (c *cli) waitFor() error {
	r, d, err := c.s.Wait(c.arg(0), c.timeout, 0)
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(map[string]any{"request": r, "decision": d})
	}
	if d == nil {
		fmt.Printf("ruled %s (decision %s not found in ledger)\n", r.ID, r.Decision)
		return nil
	}
	fmt.Printf("RULED %s by %s (%s)\n%s\n", r.ID, d.By, d.Tier, d.Ruling)
	if d.Evidence != "" {
		fmt.Printf("evidence: %s\n", d.Evidence)
	}
	return nil
}

func (c *cli) decisions() error {
	var ds []flat.Decision
	var err error
	if c.scope != "" {
		ds, err = c.s.Lookup(split(c.scope, ","))
	} else {
		ds, err = c.s.Effective()
	}
	if err != nil {
		return err
	}
	if c.jsonOut {
		return c.emit(ds)
	}
	if len(ds) == 0 {
		fmt.Println("no rulings")
	}
	for _, d := range ds {
		fmt.Printf("%s %s %s (%s) on %s: %s\n", d.ID, d.At.Format("15:04"), d.By, d.Tier, strings.Join(d.Scope, ","), d.Ruling)
	}
	return nil
}

func (c *cli) who() error {
	if c.arg(0) == "" {
		return errors.New("who needs a scope")
	}
	ws := c.s.Affinity(split(c.arg(0), ","), "")
	if c.jsonOut {
		return c.emit(ws)
	}
	if len(ws) == 0 {
		fmt.Println("nobody has context on that scope yet")
	}
	for _, w := range ws {
		fmt.Printf("%s: %s\n", w.Seat, strings.Join(w.Why, "; "))
	}
	return nil
}

func (c *cli) take() error {
	r, err := c.s.Take(c.arg(0), c.seat, c.ttl)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("holding %s (epoch %d) until %s\n", r.Name, r.Epoch, r.Until.Format(time.RFC3339))
	return nil
}

func (c *cli) drop() error {
	if err := c.s.Drop(c.arg(0), c.seat); err != nil {
		return c.record(err)
	}
	fmt.Printf("dropped %s\n", c.arg(0))
	return nil
}

func (c *cli) admit() error {
	floor, err := c.diskFloor()
	if err != nil {
		return err
	}
	opts := flat.AdmitOptions{Seats: c.seats, DiskMin: floor, Base: c.base, Resource: c.resource}
	for {
		a, err := c.s.Admit(opts)
		if err != nil {
			return err
		}
		if !a.Admitted && c.wait {
			time.Sleep(5 * time.Second)
			continue
		}
		return c.reportAdmission(a)
	}
}

func (c *cli) reportAdmission(a *flat.Admission) error {
	reasons := strings.Join(a.Reasons, "; ")
	switch {
	case c.jsonOut:
		_ = c.emit(a)
	case a.Admitted:
		fmt.Printf("admitted (%d working)\n", a.Active)
	default:
		fmt.Printf("refused: %s\n", reasons)
	}
	if !a.Admitted {
		return c.record(&flat.Refusal{Code: "not_admitted", Msg: reasons})
	}
	return nil
}

func (c *cli) watch() error {
	floor, err := c.diskFloor()
	if err != nil {
		return err
	}
	return c.s.Watch(flat.WatchOptions{Interval: c.interval, Base: c.base, Fetch: c.fetch, Idle: c.idle, UnclaimedAfter: c.unclaimed, DiskMin: floor, Once: c.once, Out: os.Stdout,
		Wake: c.wake, WakeOpts: flat.WakeOptions{Model: c.wakeModel, Tools: c.wakeTools, Max: c.wakeMax}})
}

func (c *cli) digest() error {
	d, err := c.s.ReadDigest()
	if err != nil {
		b, alerts, err := c.s.WatchOnce(flat.WatchOptions{Base: c.base, Idle: c.idle, UnclaimedAfter: c.unclaimed})
		if err != nil {
			return err
		}
		d = c.s.Digest(b, alerts)
	}
	fmt.Print(d)
	return nil
}

func (c *cli) inbox() error {
	notes, err := c.s.Inbox(c.seat, !c.keep)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Printf("inbox %s: empty\n", c.seat)
	}
	for _, n := range notes {
		fmt.Printf("[%s %s] %s\n", n.At.Format("15:04"), n.Kind, n.Text)
	}
	return nil
}

func (c *cli) nudge() error {
	_, err := c.s.Nudge(c.arg(0), "manual", strings.Join(c.pos[1:], " "))
	return err
}

func (c *cli) stats() error {
	st, err := c.s.Stats(c.threshold, c.base)
	if err != nil {
		return err
	}
	return c.emit(st)
}

func ageOf(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// installHook merges the flat hook into <dir>/.claude/settings.json.
func (c *cli) installHook() error {
	cmd := c.cmd
	if cmd == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd = filepath.ToSlash(exe)
	}
	path := filepath.Join(c.dir, ".claude", "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	want := flat.HookSettings(cmd)["hooks"].(map[string]any)
	for event, entries := range want {
		existing, _ := hooks[event].([]any)
		if hasFlatHook(existing) {
			continue
		}
		for _, e := range entries.([]map[string]any) {
			existing = append(existing, e)
		}
		hooks[event] = existing
	}
	settings["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("hook installed in %s (%s hook)\n", path, cmd)
	return nil
}

func hasFlatHook(entries []any) bool {
	for _, e := range entries {
		s := fmt.Sprint(e)
		if strings.Contains(s, "flat hook") || strings.Contains(s, "flat.exe hook") {
			return true
		}
	}
	return false
}
