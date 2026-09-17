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

func run(verb string, args []string, cwd string) error {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	as := fs.String("as", "", "seat acting (default FLAT_SEAT or current branch)")
	jsonOut := fs.Bool("json", false, "JSON output")
	base := fs.String("base", "main", "base branch")
	scope := fs.String("scope", "", "comma-separated paths or resource:NAME")
	question := fs.String("question", "", "the question")
	options := fs.String("options", "", "pipe-separated options")
	needs := fs.String("needs", "peer", "tier needed: peer, lead, operator")
	ruling := fs.String("ruling", "", "ruling text")
	evidence := fs.String("evidence", "", "what the ruling rests on")
	epoch := fs.Int("epoch", 0, "claim epoch (0 = claim now)")
	supersedes := fs.String("supersedes", "", "decision id this one replaces")
	to := fs.String("to", "", "tier to escalate to")
	why := fs.String("why", "", "reason")
	ttl := fs.Duration("ttl", 0, "lease length")
	timeout := fs.Duration("timeout", 8*time.Minute, "wait timeout")
	md := fs.Bool("md", false, "markdown")
	phone := fs.Bool("phone", false, "phone-sized")
	fetch := fs.Bool("fetch", false, "git fetch first")
	idle := fs.Duration("idle", 20*time.Minute, "silent after")
	all := fs.Bool("all", false, "include ruled requests")
	keep := fs.Bool("keep", false, "do not consume inbox")
	seats := fs.Int("seats", 0, "max working branches")
	diskMin := fs.String("disk-min", "", "free bytes floor, e.g. 10G")
	resource := fs.String("resource", "", "resource the new builder needs")
	wait := fs.Bool("wait", false, "block until admitted")
	interval := fs.Duration("interval", 30*time.Second, "watch interval")
	once := fs.Bool("once", false, "one pass")
	unclaimed := fs.Duration("unclaimed", 10*time.Minute, "alert after")
	threshold := fs.Duration("threshold", 20*time.Minute, "unclaimed kill line")
	operator := fs.String("operator", "", "operator name(s), comma-separated")
	lead := fs.String("lead", "", "lead name(s)")
	verifier := fs.String("verifier", "", "verifier name(s)")
	dir := fs.String("dir", cwd, "repository directory")
	cmd := fs.String("cmd", "", "flat binary path for the hook")
	wake := fs.Bool("wake", false, "resume seats that have notes and are between turns")
	wakeModel := fs.String("wake-model", "", "model for wakes")
	wakeTools := fs.String("wake-tools", "", "allowed tools for wakes")
	wakeMax := fs.Int("wake-max", 2, "concurrent wakes")

	// Positional args may precede flags: `flat rule ID --ruling ...`.
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos = append(pos, fs.Args()...)
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}

	s, err := flat.Open(cwd)
	if err != nil {
		return err
	}
	seat := *as
	if seat == "" {
		seat = flat.SeatOf(cwd)
	}
	os.Setenv("FLAT_SEAT", seat)
	emit := func(v any) error {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	record := func(err error) error {
		var ref *flat.Refusal
		if errors.As(err, &ref) {
			s.RecordRefusal(seat, verb, ref.Code)
		}
		return err
	}
	split := func(v, sep string) []string {
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

	switch verb {
	case "init":
		return s.Init(flat.Tiers{Operator: split(*operator, ","), Lead: split(*lead, ","), Verifier: split(*verifier, ",")})

	case "board":
		b, err := s.Board(flat.BoardOptions{Base: *base, Fetch: *fetch, Idle: *idle})
		if err != nil {
			return err
		}
		switch {
		case *md:
			fmt.Print(b.Markdown())
		case *phone:
			fmt.Print(b.Phone())
		case *jsonOut:
			return emit(b)
		default:
			fmt.Print(b.JSONL())
		}
		return nil

	case "verify":
		b, err := s.Board(flat.BoardOptions{Base: *base, Fetch: *fetch, Idle: *idle})
		if err != nil {
			return err
		}
		for _, r := range b.Rows {
			if r.Branch == arg(0) {
				if *jsonOut {
					return emit(r)
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
					return record(&flat.Refusal{Code: r.State, Msg: "not landed"})
				}
				return nil
			}
		}
		return fmt.Errorf("no branch %q", arg(0))

	case "check":
		p := arg(0)
		if p == "" {
			return errors.New("check needs a path")
		}
		b, err := s.Board(flat.BoardOptions{Base: *base, Fetch: *fetch, Idle: *idle})
		if err != nil {
			return err
		}
		var others []string
		for _, r := range b.Rows {
			if r.Branch == seat {
				continue
			}
			for _, f := range r.Files {
				if f == p || strings.HasPrefix(f, strings.TrimSuffix(p, "/")+"/") {
					others = append(others, r.Branch+" ("+r.State+")")
					break
				}
			}
		}
		ds, err := s.Lookup([]string{p})
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
			return record(&flat.Refusal{Code: "contended_unruled", Msg: p})
		}
		return nil

	case "ask":
		r, err := s.Ask(seat, split(*scope, ","), *question, split(*options, "|"), *needs)
		if err != nil {
			return record(err)
		}
		if *jsonOut {
			return emit(r)
		}
		fmt.Printf("asked %s (needs %s). wait with: flat wait %s\n", r.ID, r.Needs, r.ID)
		return nil

	case "requests":
		rs, err := s.Requests(!*all)
		if err != nil {
			return err
		}
		if *jsonOut {
			return emit(rs)
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

	case "claim":
		r, err := s.ClaimRequest(arg(0), seat, *ttl)
		if err != nil {
			return record(err)
		}
		fmt.Printf("claimed %s epoch %d until %s. rule with: flat rule %s --epoch %d --ruling '...' --evidence '...'\n", r.ID, r.Claim.Epoch, r.Claim.Until.Format(time.RFC3339), r.ID, r.Claim.Epoch)
		return nil

	case "rule":
		d, err := s.Rule(arg(0), seat, *epoch, *ruling, *evidence, *supersedes)
		if err != nil {
			return record(err)
		}
		if *jsonOut {
			return emit(d)
		}
		fmt.Printf("ruled %s -> %s by %s (%s)\n", arg(0), d.ID, d.By, d.Tier)
		return nil

	case "decide":
		d, err := s.Decide(seat, split(*scope, ","), *ruling, *evidence, *supersedes)
		if err != nil {
			return record(err)
		}
		fmt.Printf("decided %s by %s (%s) on %s\n", d.ID, d.By, d.Tier, strings.Join(d.Scope, ","))
		return nil

	case "escalate":
		r, err := s.Escalate(arg(0), seat, *to, *why)
		if err != nil {
			return record(err)
		}
		fmt.Printf("escalated %s to %s\n", r.ID, r.Needs)
		return nil

	case "wait":
		r, d, err := s.Wait(arg(0), *timeout, 0)
		if err != nil {
			return record(err)
		}
		if *jsonOut {
			return emit(map[string]any{"request": r, "decision": d})
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

	case "decisions":
		var ds []flat.Decision
		if *scope != "" {
			ds, err = s.Lookup(split(*scope, ","))
		} else {
			ds, err = s.Effective()
		}
		if err != nil {
			return err
		}
		if *jsonOut {
			return emit(ds)
		}
		if len(ds) == 0 {
			fmt.Println("no rulings")
		}
		for _, d := range ds {
			fmt.Printf("%s %s %s (%s) on %s: %s\n", d.ID, d.At.Format("15:04"), d.By, d.Tier, strings.Join(d.Scope, ","), d.Ruling)
		}
		return nil

	case "who":
		if arg(0) == "" {
			return errors.New("who needs a scope")
		}
		ws := s.Affinity(split(arg(0), ","), "")
		if *jsonOut {
			return emit(ws)
		}
		if len(ws) == 0 {
			fmt.Println("nobody has context on that scope yet")
		}
		for _, w := range ws {
			fmt.Printf("%s: %s\n", w.Seat, strings.Join(w.Why, "; "))
		}
		return nil

	case "take":
		r, err := s.Take(arg(0), seat, *ttl)
		if err != nil {
			return record(err)
		}
		fmt.Printf("holding %s (epoch %d) until %s\n", r.Name, r.Epoch, r.Until.Format(time.RFC3339))
		return nil

	case "drop":
		if err := s.Drop(arg(0), seat); err != nil {
			return record(err)
		}
		fmt.Printf("dropped %s\n", arg(0))
		return nil

	case "admit":
		var floor uint64
		if *diskMin != "" {
			if floor, err = flat.ParseBytes(*diskMin); err != nil {
				return err
			}
		}
		for {
			a, err := s.Admit(flat.AdmitOptions{Seats: *seats, DiskMin: floor, Base: *base, Resource: *resource})
			if err != nil {
				return err
			}
			if a.Admitted || !*wait {
				if *jsonOut {
					_ = emit(a)
				} else if a.Admitted {
					fmt.Printf("admitted (%d working)\n", a.Active)
				} else {
					fmt.Printf("refused: %s\n", strings.Join(a.Reasons, "; "))
				}
				if !a.Admitted {
					return record(&flat.Refusal{Code: "not_admitted", Msg: strings.Join(a.Reasons, "; ")})
				}
				return nil
			}
			time.Sleep(5 * time.Second)
		}

	case "watch":
		var floor uint64
		if *diskMin != "" {
			if floor, err = flat.ParseBytes(*diskMin); err != nil {
				return err
			}
		}
		return s.Watch(flat.WatchOptions{Interval: *interval, Base: *base, Fetch: *fetch, Idle: *idle, UnclaimedAfter: *unclaimed, DiskMin: floor, Once: *once, Out: os.Stdout,
			Wake: *wake, WakeOpts: flat.WakeOptions{Model: *wakeModel, Tools: *wakeTools, Max: *wakeMax}})

	case "digest":
		d, err := s.ReadDigest()
		if err != nil {
			b, alerts, err := s.WatchOnce(flat.WatchOptions{Base: *base, Idle: *idle, UnclaimedAfter: *unclaimed})
			if err != nil {
				return err
			}
			d = s.Digest(b, alerts)
		}
		fmt.Print(d)
		return nil

	case "inbox":
		notes, err := s.Inbox(seat, !*keep)
		if err != nil {
			return err
		}
		if len(notes) == 0 {
			fmt.Printf("inbox %s: empty\n", seat)
		}
		for _, n := range notes {
			fmt.Printf("[%s %s] %s\n", n.At.Format("15:04"), n.Kind, n.Text)
		}
		return nil

	case "nudge":
		_, err := s.Nudge(arg(0), "manual", strings.Join(pos[1:], " "))
		return err

	case "install-hook":
		return installHook(*dir, *cmd)

	case "stats":
		st, err := s.Stats(*threshold, *base)
		if err != nil {
			return err
		}
		return emit(st)
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown verb %q", verb)
}

func ageOf(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// installHook merges the flat hook into <dir>/.claude/settings.json.
func installHook(dir, cmd string) error {
	if cmd == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd = filepath.ToSlash(exe)
	}
	path := filepath.Join(dir, ".claude", "settings.json")
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
		present := false
		for _, e := range existing {
			if strings.Contains(fmt.Sprint(e), "flat hook") || strings.Contains(fmt.Sprint(e), "flat.exe hook") {
				present = true
			}
		}
		if present {
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
