// Command swarm is the substrate for a fleet with no management sessions.
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

	"github.com/itsHabib/workbench/cmd/swarm/internal/gym"
	"github.com/itsHabib/workbench/cmd/swarm/internal/poc"
	"github.com/itsHabib/workbench/cmd/swarm/internal/seat"
	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

const usage = `swarm: a fleet substrate with no leads

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
            order                    landing order of landed branches from the ledger (the theme map)
            load [--window 1h]       queue wait per address: the number behind "too many messages"
            split --into "b1:title|b2:title" --why W    this task is really several; queue the rest
            intend PATH...           declare what you are about to change, so it contends from now
            consolidate [--verify CMD]   merge landed branches into this one in ledger order; list conflicts
resource    take NAME [--ttl 30m] · drop NAME
admit       admit [--seats N] [--disk-min 10G] [--resource NAME] [--wait] [--json]
watch       watch [--interval 30s] [--once] [--fetch] [--idle 20m] [--unclaimed 10m] [--disk-min 10G]
                  [--wake] [--wake-model M] [--wake-tools LIST] [--wake-max 2] [--verify "go test ./..."]
            digest
inbox       inbox [--keep] · nudge SEAT TEXT · hook · install-hook [--dir REPO]
stats       stats [--threshold 20m] [--json]
gym         gym list · gym run --models a,b --out DIR · gym table --out DIR     which model for which seat
plane       plane fault --store file|resp · plane rooms ...                      the store and its fault harness
poc         poc init DIR · poc run --dir DIR --mode flat|tree [...] · poc stats --run DIR

--as SEAT applies to every verb; default SWARM_SEAT, else the checked-out branch.
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
	if verb == "plane" {
		os.Exit(planeMain(args))
	}
	if verb == "seat" {
		os.Exit(seat.Main(args))
	}
	if verb == "gym" {
		os.Exit(gym.Main(args))
	}
	if verb == "hook" {
		_ = swarm.Hook(os.Stdin, os.Stdout)
		return
	}
	cwd, _ := os.Getwd()
	err := run(verb, args, cwd)
	var ref *swarm.Refusal
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
	s    *swarm.State
	seat string

	title, files, result, head, verdict, tail string

	as, scope, question, options, needs, ruling, evidence, supersedes, to, why        string
	operator, lead, verifier, diskMin, resource, dir, cmd, wakeModel, wakeTools, base string
	orderFlag, verifyCmd, into, forBranch                                             string
	jsonOut, md, phone, fetch, all, keep, wait, once, wake                            bool
	epoch, seats, wakeMax                                                             int
	ttl, timeout, idle, interval, unclaimed, threshold, window                        time.Duration
}

func parse(verb string, args []string) (*cli, error) {
	c := &cli{verb: verb}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.StringVar(&c.as, "as", "", "seat acting (default SWARM_SEAT or current branch)")
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
	fs.StringVar(&c.cmd, "cmd", "", "swarm binary path for the hook; receipt: the verification command recorded")
	fs.BoolVar(&c.wake, "wake", false, "resume seats that have notes and are between turns")
	fs.StringVar(&c.wakeModel, "wake-model", "", "model for wakes")
	fs.StringVar(&c.wakeTools, "wake-tools", "", "allowed tools for wakes")
	fs.IntVar(&c.wakeMax, "wake-max", 2, "concurrent wakes")
	fs.StringVar(&c.orderFlag, "order", "", "branches in landing order, comma-separated, when ruling on order")
	fs.StringVar(&c.verifyCmd, "verify", "", "watch: command to run at each landed head, e.g. 'go test ./...'")
	fs.DurationVar(&c.window, "window", time.Hour, "load window")
	fs.StringVar(&c.title, "title", "", "work title")
	fs.StringVar(&c.files, "files", "", "comma-separated paths the work touches")
	fs.StringVar(&c.verdict, "verdict", "", "pass or fail")
	fs.StringVar(&c.tail, "tail", "", "last lines of the verifier output")
	fs.StringVar(&c.head, "head", "", "commit that carries the finished unit")
	fs.StringVar(&c.result, "result", "", "what was done")
	fs.StringVar(&c.into, "into", "", "split children as branch:title|branch:title")
	fs.StringVar(&c.forBranch, "for", "", "admit: the branch this seat is for; reserves the seat until it is on the board")

	// Positional args may precede flags: `swarm rule ID --ruling ...`.
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
	"order":        (*cli).order,
	"load":         (*cli).load,
	"split":        (*cli).split,
	"intend":       (*cli).intend,
	"consolidate":  (*cli).consolidate,
	"who":          (*cli).who,
	"take":         (*cli).take,
	"work":         (*cli).work,
	"receipt":      (*cli).receipt,
	"receipts":     (*cli).receipts,
	"review":       (*cli).review,
	"reviews":      (*cli).reviews,
	"idle":         (*cli).idleWait,
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
	if c.s, err = swarm.Open(cwd); err != nil {
		return err
	}
	c.seat = c.as
	if c.seat == "" {
		c.seat = swarm.SeatOf(cwd)
	}
	os.Setenv("SWARM_SEAT", c.seat)
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
	var ref *swarm.Refusal
	if errors.As(err, &ref) {
		c.s.RecordRefusal(c.seat, c.verb, ref.Code)
	}
	return err
}

func (c *cli) diskFloor() (uint64, error) {
	if c.diskMin == "" {
		return 0, nil
	}
	return swarm.ParseBytes(c.diskMin)
}

func (c *cli) boardOpts() swarm.BoardOptions {
	return swarm.BoardOptions{Base: c.base, Fetch: c.fetch, Idle: c.idle}
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
	return c.s.Init(swarm.Tiers{Operator: split(c.operator, ","), Lead: split(c.lead, ","), Verifier: split(c.verifier, ",")})
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
			return c.record(&swarm.Refusal{Code: r.State, Msg: "not landed"})
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
	// Contention is judged at package scope: a sibling in the same directory
	// counts, because a ruling on one file rarely leaves its test alone.
	// Coverage is judged at file scope: the ruling must reach this path.
	pkg := packageOf(p)
	others := b.Touching(pkg, c.seat)
	ds, err := c.s.Lookup([]string{p})
	if err != nil {
		return err
	}
	switch {
	case len(others) == 0:
		fmt.Printf("clear: no other branch has changed anything under %s\n", pkg)
	case len(ds) > 0:
		d := ds[len(ds)-1]
		fmt.Printf("contended but ruled: %s is also changed by %s. ruling %s by %s (%s): %s\n", pkg, strings.Join(others, ", "), d.ID, d.By, d.Tier, d.Ruling)
	default:
		fmt.Printf("contended, no ruling: %s is also changed by %s. do not edit %s until ruled. ask at package scope: swarm ask --scope %s --question 'who lands first on %s?' --options '%s|me' then swarm wait ID; rule with --order to record it\n", pkg, strings.Join(others, ", "), p, pkg, pkg, strings.Split(others[0], " ")[0])
		return c.record(&swarm.Refusal{Code: "contended_unruled", Msg: p})
	}
	return nil
}

// packageOf is the directory a path lives in, or the path itself for a
// directory or a top-level file.
func packageOf(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return p
}

func (c *cli) load() error {
	ls, err := c.s.Load(c.window)
	if err != nil {
		return err
	}
	if c.jsonOut {
		return c.emit(ls)
	}
	fmt.Println(swarm.LoadText(ls))
	return nil
}

func (c *cli) intend() error {
	paths, err := c.s.Intend(c.dir, c.seat, c.pos)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("intent recorded for %s: %s\n", c.seat, strings.Join(paths, ", "))
	return nil
}

func (c *cli) consolidate() error {
	res, err := c.s.Consolidate(c.dir, c.verifyCmd, c.boardOpts())
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(res)
	}
	if res.HeadRed != "" {
		fmt.Printf("HEAD %s FAILED VERIFY before any merge: an earlier merge was never verified and is red. fix or undo it, then run consolidate again.\n", res.HeadRed[:8])
		return c.record(&swarm.Refusal{Code: "head_red", Msg: res.HeadRed})
	}
	fmt.Printf("merged %d: %s\n", len(res.Merged), strings.Join(res.Merged, ", "))
	if len(res.Conflicted) > 0 {
		fmt.Printf("CONFLICTED %d (merge these by hand, in this order): %s\n", len(res.Conflicted), strings.Join(res.Conflicted, ", "))
	}
	if len(res.Failed) > 0 {
		fmt.Printf("FAILED VERIFY %d (merged cleanly, tests red, undone): %s\n", len(res.Failed), strings.Join(res.Failed, ", "))
	}
	return nil
}

func (c *cli) split() error {
	top, err := swarm.Git(c.dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	// tasks.json lives in the shared checkout: the main worktree of the repo.
	common, err := swarm.Git(c.dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(c.dir, common)
	}
	repo := filepath.Dir(filepath.Clean(common))
	if _, err := os.Stat(filepath.Join(repo, "briefs")); err != nil {
		repo = top
	}
	d, err := c.s.Split(c.seat, repo, swarm.ParseChildren(c.into), c.why)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("split recorded as %s; children queued in %s\n", d.ID, filepath.Join(repo, "briefs", "tasks.json"))
	return nil
}

func (c *cli) order() error {
	seq, err := c.s.Order(c.boardOpts())
	if err != nil {
		return err
	}
	if c.jsonOut {
		return c.emit(seq)
	}
	fmt.Println(seq.Text())
	return nil
}

func (c *cli) ask() error {
	r, err := c.s.Ask(c.seat, split(c.scope, ","), c.question, split(c.options, "|"), c.needs)
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(r)
	}
	fmt.Printf("asked %s (needs %s). wait with: swarm wait %s\n", r.ID, r.Needs, r.ID)
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
	fmt.Printf("claimed %s epoch %d until %s. rule with: swarm rule %s --epoch %d --ruling '...' --evidence '...'\n", r.ID, r.Claim.Epoch, r.Claim.Until.Format(time.RFC3339), r.ID, r.Claim.Epoch)
	return nil
}

func (c *cli) rule() error {
	d, err := c.s.Rule(c.arg(0), c.seat, c.epoch, c.ruling, c.evidence, c.supersedes, split(c.orderFlag, ","))
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
	d, err := c.s.Decide(c.seat, split(c.scope, ","), c.ruling, c.evidence, c.supersedes, split(c.orderFlag, ","))
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
	var ds []swarm.Decision
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

func (c *cli) work() error {
	var w *swarm.Work
	var err error
	switch c.arg(0) {
	case "add":
		w, err = c.s.WorkAdd(c.arg(1), c.title, split(c.files, ","), c.seat, c.supersedes)
	case "claim":
		w, err = c.s.WorkClaim(c.arg(1), c.seat, c.ttl)
	case "drop":
		w, err = c.s.WorkDrop(c.arg(1), c.seat)
	case "done":
		w, err = c.s.WorkDone(c.arg(1), c.seat, c.result, c.head)
	case "list", "":
		return c.workList()
	default:
		return fmt.Errorf("work: add|list|claim|done|drop")
	}
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("%s %s: %s\n", w.State, w.ID, w.Title)
	return nil
}

func (c *cli) workList() error {
	ws, err := c.s.WorkList()
	if err != nil {
		return err
	}
	if c.jsonOut {
		return c.emit(ws)
	}
	for _, w := range ws {
		who := w.Holder
		if w.State == "done" {
			who = w.DoneBy
		}
		fmt.Printf("%-8s %-24s %-12s %s  [%s]\n", w.State, w.ID, who, w.Title, strings.Join(w.Files, ","))
	}
	if len(ws) == 0 {
		fmt.Println("no work yet")
	}
	return nil
}

func (c *cli) idleWait() error {
	line := c.s.Idle(c.seat, c.timeout)
	if line == "" {
		return &swarm.Refusal{Code: "timeout", Msg: "nothing to do yet"}
	}
	fmt.Println(line)
	return nil
}

func (c *cli) receipt() error {
	tip := c.arg(0)
	if c.verdict == "" {
		rec := c.s.Receipt(tip)
		if rec == nil {
			return c.record(&swarm.Refusal{Code: "no_receipt", Msg: "no receipt for " + tip})
		}
		return c.emit(rec)
	}
	if c.verdict != "pass" && c.verdict != "fail" {
		return fmt.Errorf("--verdict pass|fail")
	}
	rec := &swarm.Receipt{Tip: tip, Branch: c.seat, Cmd: c.cmd, Pass: c.verdict == "pass", Tail: c.tail}
	if err := c.s.PutReceipt(rec); err != nil {
		return c.record(err)
	}
	fmt.Printf("receipt %s: %s\n", tip, c.verdict)
	return nil
}

func (c *cli) receipts() error {
	rs, err := c.s.Receipts()
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(rs)
	}
	for _, r := range rs {
		fmt.Printf("%-5s %s %-12s %s\n", map[bool]string{true: "pass", false: "FAIL"}[r.Pass], shortTip(r.Tip), r.Branch, r.Cmd)
	}
	return nil
}

func (c *cli) review() error {
	r, err := c.s.Review(c.arg(0), c.seat, c.verdict, c.why)
	if err != nil {
		return c.record(err)
	}
	fmt.Printf("review %s by %s: %s\n", shortTip(r.Tip), r.By, r.Verdict)
	return nil
}

func (c *cli) reviews() error {
	rs, err := c.s.Reviews(c.arg(0))
	if err != nil {
		return c.record(err)
	}
	if c.jsonOut {
		return c.emit(rs)
	}
	for _, r := range rs {
		fmt.Printf("%-5s %s by %-10s %s\n", r.Verdict, shortTip(r.Tip), r.By, r.Why)
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
	if err := c.s.Drop(c.arg(0), c.seat, c.epoch); err != nil {
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
	opts := swarm.AdmitOptions{Seats: c.seats, DiskMin: floor, Base: c.base, Resource: c.resource, For: c.forBranch}
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

func (c *cli) reportAdmission(a *swarm.Admission) error {
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
		return c.record(&swarm.Refusal{Code: "not_admitted", Msg: reasons})
	}
	return nil
}

func (c *cli) watch() error {
	floor, err := c.diskFloor()
	if err != nil {
		return err
	}
	return c.s.Watch(swarm.WatchOptions{Interval: c.interval, Base: c.base, Fetch: c.fetch, Idle: c.idle, UnclaimedAfter: c.unclaimed, DiskMin: floor, Once: c.once, Out: os.Stdout,
		Wake: c.wake, WakeOpts: swarm.WakeOptions{Model: c.wakeModel, Tools: c.wakeTools, Max: c.wakeMax}, Verify: c.verifyCmd})
}

func (c *cli) digest() error {
	d, err := c.s.ReadDigest()
	if err != nil {
		b, alerts, err := c.s.WatchOnce(swarm.WatchOptions{Base: c.base, Idle: c.idle, UnclaimedAfter: c.unclaimed})
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

// installHook merges the swarm hook into <dir>/.claude/settings.json.
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
	want := swarm.HookSettings(cmd)["hooks"].(map[string]any)
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
	if err := swarm.WriteFileAtomic(path, append(data, '\n')); err != nil {
		return err
	}
	fmt.Printf("hook installed in %s (%s hook)\n", path, cmd)
	return nil
}

func hasFlatHook(entries []any) bool {
	for _, e := range entries {
		s := fmt.Sprint(e)
		if strings.Contains(s, "swarm hook") || strings.Contains(s, "swarm.exe hook") {
			return true
		}
	}
	return false
}

func shortTip(tip string) string {
	if len(tip) > 12 {
		return tip[:12]
	}
	return tip
}
