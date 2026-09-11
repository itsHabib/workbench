// Package standup is the record and the compiler behind the standup: a conversation
// with the lead lane that ends in one JSON record, which then compiles field by field
// into Fleet verbs that already exist. The package owns three things and no more:
//
//   - the agenda, derived from records (fleet work, receipts, mail, org status, open
//     pull requests, the previous record's deferrals) and pinned by a digest;
//   - the record (standup.v1): roles, cards, decisions, deferrals, confirm, applied;
//   - apply, which refuses an unconfirmed or stale record and otherwise runs the
//     verbs in order, writing each receipt into applied[] as it goes.
//
// Every verb it runs is another tool's CLI read as an artifact (its JSON output and
// exit code). Nothing here imports a decision from fleet, org or gh; the binaries are
// named by the environment so this tool learns no path and no harness flag.
package standup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config is $STANDUP_DIR/config.json: who the lead is, which repositories the agenda
// watches, and the phrase that sets confirm. It is written once by the operator.
type Config struct {
	Lead   string   `json:"lead"`
	Tenant string   `json:"tenant"`
	Phrase string   `json:"phrase"`
	Repos  []string `json:"repos"`
}

// Env is everything the verbs need from the outside: where records live, which
// binaries to run, where lanes and the roles map are, and a clock. Tests build one
// by hand; the binary builds it from the process environment.
type Env struct {
	Dir      string // $STANDUP_DIR, default $FLEET_STATE/standup
	Fleet    string // $FLEET_BIN, default "fleet"
	Org      string // $ORG_BIN, default "org"
	GH       string // $GH_BIN, default "gh"
	LeadDir  string // where fleet mail and send run: the lead's own directory
	Lanes    string // $FLEET_STATE/lanes: one manifest.json per kind
	RolesMap string // $ORG_STATE/roles.map: path → tenant role [seat]
	Run      Runner
	Now      func() time.Time
}

// Result is what a tool said: its streams and exit code, or the failure to run it.
type Result struct {
	Stdout string
	Stderr string
	Code   int
	Err    error
}

// Runner runs one tool in one directory. The exec form is the only one outside tests.
type Runner interface {
	Run(dir, name string, args ...string) Result
}

// ExecRunner runs the real binaries with a bound on how long any one may take.
type ExecRunner struct{ Timeout time.Duration }

// Run executes name with args in dir and captures both streams. A missing binary or
// a timeout is an Err; a non-zero exit is a Code with Err nil, because a refusal is
// the tool doing its job and the caller decides what it means.
func (r ExecRunner) Run(dir, name string, args ...string) Result {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
	default:
		res.Err = err
	}
	if ctx.Err() != nil {
		res.Err = fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctx.Err())
	}
	return res
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// EnvFromOS reads the process environment. cwd is the lead's directory: fleet mail
// and send resolve the caller from the directory they run in, so the standup runs
// where the lead session runs.
func EnvFromOS() (Env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Env{}, err
	}
	fleetState := expand(envOr("FLEET_STATE", "~/.fleet"))
	orgState := expand(envOr("ORG_STATE", "~/dev/org/state"))
	return Env{
		Dir:      expand(envOr("STANDUP_DIR", filepath.Join(fleetState, "standup"))),
		Fleet:    envOr("FLEET_BIN", "fleet"),
		Org:      envOr("ORG_BIN", "org"),
		GH:       envOr("GH_BIN", "gh"),
		LeadDir:  cwd,
		Lanes:    filepath.Join(fleetState, "lanes"),
		RolesMap: filepath.Join(orgState, "roles.map"),
		Run:      ExecRunner{},
		Now:      time.Now,
	}, nil
}

// LoadConfig reads config.json and says exactly what to write when it is missing.
func (e Env) LoadConfig() (Config, error) {
	p := filepath.Join(e.Dir, "config.json")
	b, err := os.ReadFile(p)
	if err != nil {
		example, _ := json.MarshalIndent(Config{Lead: "lead:mh", Tenant: "mh", Phrase: "ship it", Repos: []string{"owner/repo"}}, "", "  ")
		return Config{}, fmt.Errorf("no config at %s; write one like:\n%s", p, example)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", p, err)
	}
	switch {
	case c.Lead == "":
		return Config{}, fmt.Errorf("%s: lead is required (the address fleet mail is read for)", p)
	case c.Tenant == "":
		return Config{}, fmt.Errorf("%s: tenant is required (seats are looked up in roles.map by tenant)", p)
	case strings.TrimSpace(c.Phrase) == "":
		return Config{}, fmt.Errorf("%s: phrase is required (the words that set confirm)", p)
	}
	return c, nil
}

// Seats reads roles.map and returns seat name → directory for the lead's tenant.
// A line is `<path> <tenant> <role> [seat]`; only lines with a seat are seats.
func (e Env) Seats(tenant string) (map[string]string, error) {
	b, err := os.ReadFile(e.RolesMap)
	if err != nil {
		return nil, err
	}
	seats := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 4 && f[1] == tenant {
			seats[f[3]] = f[0]
		}
	}
	return seats, nil
}

// KindExists reports whether the lanes directory carries a manifest for kind.
func (e Env) KindExists(kind string) bool {
	_, err := os.Stat(filepath.Join(e.Lanes, kind, "manifest.json"))
	return err == nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
