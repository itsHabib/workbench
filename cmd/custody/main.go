// Command custody is the operator's credential broker. This binary is the CLI
// front door; today it wires the `grant` verb (mint a scoped, expiring,
// HMAC-signed action grant). The `keys` and `serve` verbs are owned by other
// streams and slot into the same registry — a pending verb prints a clear
// "not yet implemented" rather than a bare usage error.
//
// State and the mint key live OUTSIDE this repo and in SEPARATE trust domains:
// the state dir holds manifests/grants/logs; the mint key lives in its own dir
// so anything that can read state cannot thereby forge broader grants. See the
// custody design doc §5.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
)

// command is one CLI verb. The registry below is the single extension point:
// adding `keys` or `serve` is registering a command, not editing a switch.
type command struct {
	name    string
	summary string
	run     func(args []string) error
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if isHelp(os.Args[1]) {
		usage()
		return
	}
	cmd, ok := lookup(os.Args[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "custody: unknown verb %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err := cmd.run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "custody:", err)
		os.Exit(1)
	}
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// commands is the verb registry. Adding a verb is registering a command, not
// editing a switch.
func commands() []command {
	return []command{
		{name: "grant", summary: "mint a scoped, expiring action grant", run: cmdGrant},
		{name: "keys", summary: "manage vendor secrets (keys set)", run: cmdKeys},
		{name: "serve", summary: "run the localhost credential proxy", run: cmdServe},
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: custody <verb> [flags]")
	fmt.Fprintln(os.Stderr, "\nverbs:")
	for _, c := range commands() {
		fmt.Fprintf(os.Stderr, "  %-7s %s\n", c.name, c.summary)
	}
}

func cmdGrant(args []string) error {
	fs := flag.NewFlagSet("grant", flag.ContinueOnError)
	stateDir := fs.String("state", envOr("CUSTODY_STATE", defaultStateDir()), "state directory (grants/logs/manifest) [env CUSTODY_STATE]")
	keyDir := fs.String("mint-key-dir", envOr("CUSTODY_KEY_DIR", defaultKeyDir()), "mint-key dir; a separate trust domain, must be outside -state [env CUSTODY_KEY_DIR]")
	key := fs.String("key", "", "vendor key name the grant is scoped to")
	actions := fs.String("actions", "", "comma-separated action names (e.g. read,comment)")
	ttl := fs.Duration("ttl", 0, "grant lifetime (e.g. 8h)")
	mintedBy := fs.String("minted-by", "operator", "free-form, UNAUTHENTICATED label of who minted this")
	initKey := fs.Bool("init", false, "create a fresh mint key in -mint-key-dir if none exists yet (first-run opt-in)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	acts, err := parseActions(*actions)
	if err != nil {
		return err
	}
	store, err := grant.NewStore(*stateDir, *keyDir)
	if err != nil {
		return err
	}
	if err := store.RequireMintKey(*initKey); err != nil {
		return err
	}
	_, tok, err := store.Mint(*key, acts, *ttl, *mintedBy, time.Now)
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}

// parseActions splits -actions on commas, trims each, and drops empties so
// "read, comment," yields ["read","comment"]. An empty result is left for Mint
// to reject with its own message.
func parseActions(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if a := strings.TrimSpace(part); a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// envOr returns env var key's value, or fallback when unset/empty — the same
// ambient-default shape gate uses for GATE_STATE/GATE_KEY. An explicit flag
// still wins; the flag default is only consulted when the flag is not passed.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultStateDir is %USERPROFILE%\.custody (spec §5). Falls back to a
// .custody directory under the working directory when no home is resolvable.
func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".custody"
	}
	return filepath.Join(home, ".custody")
}

// defaultKeyDir is %USERPROFILE%\.custody-key (spec §5) — deliberately a
// SIBLING of the state dir, never nested under it, so the mint key is its own
// trust domain. NewStore refuses any key dir that violates that.
func defaultKeyDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".custody-key"
	}
	return filepath.Join(home, ".custody-key")
}
