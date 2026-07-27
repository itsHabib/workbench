// Command stubgate is a recording test double for the gate binary, used only by
// the escalate serve e2e (cmd/escalate/e2e). It is NOT gate: it implements just
// the two verbs `escalate serve` shells — `next -json` (the grant lookup) and
// `resolve` (the effect) — with deterministic, hand-written output matching
// gate's JSON+exit-code contract, and it RECORDS every resolve invocation so the
// e2e can assert exactly what serve drove gate with.
//
// It exists so the e2e can exercise the real `escalate serve` binary — its
// signature gate, the Slack-callback→decision map, the who-from-verified-identity
// path, the grant-from-parked-escalation lookup, and replay safety — end to end
// over real HTTP, without a live gate (which needs signing keys and an offline PR
// to seed a park). Loopback stands in for the ngrok tunnel; this stub stands in
// for the real gate, whose end-to-end thread Phase 1's manual EVIDENCE already
// captured against the real binary. The stub carries NO decision logic of gate's
// own — it is a sink that echoes a fixed outcome and journals the call.
//
// State lives in the -state dir serve forwards to both verbs (mirroring how real
// gate uses its state dir): seed.json (the seeded park the harness writes),
// resolved.txt (ids already resolved — so a replayed lookup finds nothing), and
// invocations.jsonl (the resolve journal the harness reads back).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail("stubgate: want a subcommand (next|resolve)")
	}
	state := flagValue(os.Args[2:], "-state")
	if state == "" {
		fail("stubgate: -state is required")
	}
	switch os.Args[1] {
	case "next":
		os.Exit(next(state))
	case "resolve":
		os.Exit(resolve(state, os.Args[2:]))
	}
	fail("stubgate: unknown subcommand %q", os.Args[1])
}

// seed is the park the harness seeds before starting serve: the escalation id
// and the grant its run parked under, plus the PR the outcome names.
type seed struct {
	Escalation string `json:"escalation"`
	Grant      string `json:"grant"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	Head       string `json:"head"`
}

// next emits gate's `next -json` inbox feed: the seeded park unless it has
// already been resolved, in which case the inbox is empty — exactly the
// projection that makes serve's grant lookup the first line of the replay
// defense (a resolved park leaves the inbox, so the second tap finds no grant).
func next(state string) int {
	s := readSeed(state)
	feed := map[string]any{"parked": []any{}}
	if !resolvedContains(state, s.Escalation) {
		feed["parked"] = []any{map[string]any{"escalation": s.Escalation, "grant": s.Grant}}
	}
	out, err := json.Marshal(feed)
	if err != nil {
		fail("stubgate: marshal feed: %v", err)
	}
	fmt.Println(string(out))
	return 0
}

// resolve journals the decision serve drove and echoes gate's outcome JSON: a
// pass exits 0 (would_merge), a block exits 1 (would_block) — both landed
// decisions in gate's 0..3 space, which serve relays as HTTP 200. It records the
// id as resolved so a subsequent next() shows the park gone. It carries none of
// gate's real ladder logic; it is the sink the e2e asserts against.
func resolve(state string, args []string) int {
	s := readSeed(state)
	inv := invocation{
		Escalation: flagValue(args, "-escalation"),
		Grant:      flagValue(args, "-grant"),
		Decision:   flagValue(args, "-decision"),
		Why:        flagValue(args, "-why"),
		Who:        flagValue(args, "-who"),
	}
	journal(state, inv)
	markResolved(state, inv.Escalation)
	outcome, code := "would_merge", 0
	if inv.Decision == "block" {
		outcome, code = "would_block", 1
	}
	body := map[string]any{
		"run":      "run_stub",
		"pr":       fmt.Sprintf("%s#%d", s.Repo, s.Number),
		"decision": inv.Decision,
		"tier":     "T0",
		"outcome":  outcome,
		"why":      "judgment: " + inv.Why,
		"who":      inv.Who,
		"head_sha": s.Head,
	}
	out, err := json.Marshal(body)
	if err != nil {
		fail("stubgate: marshal outcome: %v", err)
	}
	fmt.Println(string(out))
	return code
}

// invocation is one recorded resolve call — the fields the e2e asserts serve
// derived correctly (the verified who, the mapped verdict, the joined grant).
type invocation struct {
	Escalation string `json:"escalation"`
	Grant      string `json:"grant"`
	Decision   string `json:"decision"`
	Why        string `json:"why"`
	Who        string `json:"who"`
}

func readSeed(state string) seed {
	raw, err := os.ReadFile(filepath.Join(state, "seed.json"))
	if err != nil {
		fail("stubgate: read seed: %v", err)
	}
	var s seed
	if err := json.Unmarshal(raw, &s); err != nil {
		fail("stubgate: decode seed: %v", err)
	}
	return s
}

func journal(state string, inv invocation) {
	line, err := json.Marshal(inv)
	if err != nil {
		fail("stubgate: marshal invocation: %v", err)
	}
	appendLine(filepath.Join(state, "invocations.jsonl"), string(line))
}

func markResolved(state, escID string) {
	appendLine(filepath.Join(state, "resolved.txt"), escID)
}

func resolvedContains(state, escID string) bool {
	raw, err := os.ReadFile(filepath.Join(state, "resolved.txt"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == escID {
			return true
		}
	}
	return false
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fail("stubgate: open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		fail("stubgate: write %s: %v", path, err)
	}
}

// flagValue scans a "-name value" pair out of args (separate tokens, the form
// serve emits). It is a deliberate tiny parser, not flag.FlagSet, so the stub
// stays a single dependency-free file.
func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(4)
}
