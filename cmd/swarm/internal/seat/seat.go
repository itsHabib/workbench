// Package seat is one agent session as a process: the seam between a
// runner that decides who works on what and a substrate that decides where
// a process runs. A runner starts a seat with `swarm seat run` and reads one
// JSON line back; a substrate (a shell, a worktree, a Rooms clone) only has
// to run that command and pass the environment through.
package seat

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Result is the one line a seat prints last on stdout.
type Result struct {
	Seat      string  `json:"seat"`
	SessionID string  `json:"session_id"`
	Turns     int     `json:"num_turns"`
	CostUSD   float64 `json:"total_cost_usd"`
	WallS     float64 `json:"wall_s"`
	Exit      int     `json:"exit"`
	Error     string  `json:"error,omitempty"`
}

const usage = `swarm seat run: one agent session in one checkout, as a process

  swarm seat run --seat NAME --prompt FILE [--dir DIR] [--remote URL] [--resume SESSION] [--branch B]
                 [--model M] [--turns N] [--tools LIST] [--secrets FILE] [--skip-permissions]

  --dir      the checkout to work in (default: the current directory)
  --remote   clone this into --dir first when --dir has no .git
  --resume   continue this session id with the prompt as its next message
  --secrets  a KEY=VALUE file to load into the session's environment before it starts
             (default /run/rooms/secrets.env when it exists)

Reads SWARM_SEAT, SWARM_STORE and SWARM_INCARNATION from the environment and passes
them on, setting SWARM_SEAT to --seat. Prints one JSON line last on stdout:
{"seat","session_id","num_turns","total_cost_usd","wall_s","exit"}.
Exit 0 when the session ran to the end of its turn, 3 otherwise.`

// Main is the seat subcommand.
func Main(args []string) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, usage)
		return 3
	}
	fs := flag.NewFlagSet("seat run", flag.ContinueOnError)
	seat := fs.String("seat", os.Getenv("SWARM_SEAT"), "seat name")
	prompt := fs.String("prompt", "", "file holding the prompt")
	dir := fs.String("dir", ".", "checkout")
	remote := fs.String("remote", "", "clone this when dir is not a checkout")
	resume := fs.String("resume", "", "session id to continue")
	branch := fs.String("branch", "", "check this branch out after cloning (default: the remote's default branch)")
	model := fs.String("model", "", "model")
	turns := fs.Int("turns", 150, "turn cap")
	tools := fs.String("tools", defaultTools(), "allowed tools (default: $TOOLS when set, else the Go set)")
	secrets := fs.String("secrets", "", "KEY=VALUE file to load")
	skip := fs.Bool("skip-permissions", false, "run claude with --dangerously-skip-permissions (needs a non-root user)")
	if fs.Parse(args[1:]) != nil || *seat == "" || *prompt == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 3
	}
	res := Result{Seat: *seat}
	start := time.Now()
	err := run(&res, options{branch: *branch, dir: *dir, remote: *remote, prompt: *prompt, resume: *resume, model: *model, turns: *turns, tools: *tools, secrets: *secrets, skip: *skip})
	res.WallS = time.Since(start).Seconds()
	if err != nil {
		res.Error, res.Exit = err.Error(), 3
	}
	line, _ := json.Marshal(res)
	fmt.Println(string(line))
	return res.Exit
}

type options struct {
	dir, remote, prompt, resume, model, tools, secrets, branch string
	turns                                                      int
	skip                                                       bool
}

func run(res *Result, o options) error {
	if err := ensureCheckout(o.dir, o.remote, o.branch, res.Seat); err != nil {
		return err
	}
	msg, err := os.ReadFile(o.prompt)
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	env, err := environment(res.Seat, o.secrets)
	if err != nil {
		return err
	}
	args := []string{"-p", string(msg), "--output-format", "json", "--max-turns", fmt.Sprint(o.turns), "--allowedTools", o.tools}
	if o.skip {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args, "--permission-mode", "acceptEdits")
	}
	if o.model != "" {
		args = append(args, "--model", o.model)
	}
	if o.resume != "" {
		args = append(args, "--resume", o.resume)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir, cmd.Env, cmd.Stderr = o.dir, env, os.Stderr
	out, runErr := cmd.Output()
	var got struct {
		NumTurns  int     `json:"num_turns"`
		Cost      float64 `json:"total_cost_usd"`
		SessionID string  `json:"session_id"`
		IsError   bool    `json:"is_error"`
		Result    string  `json:"result"`
	}
	if json.Unmarshal(out, &got) != nil {
		if runErr != nil {
			return fmt.Errorf("claude: %w", runErr)
		}
		return errors.New("claude printed no result")
	}
	res.Turns, res.CostUSD, res.SessionID = got.NumTurns, got.Cost, got.SessionID
	if got.IsError {
		// claude -p exits 0 with is_error set, so a bad token or a refused
		// model would otherwise look like a finished turn.
		return fmt.Errorf("session error: %s", firstLine(got.Result))
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// ensureCheckout clones remote into dir when dir is not already a checkout,
// and names the seat as the committer so the board can tell seats apart.
func ensureCheckout(dir, remote, branch, seat string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	}
	if remote == "" {
		return fmt.Errorf("%s is not a checkout and no --remote was given", dir)
	}
	if out, err := exec.Command("git", "clone", "-q", remote, dir).CombinedOutput(); err != nil {
		return fmt.Errorf("clone: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for _, kv := range [][]string{{"user.name", seat}, {"user.email", seat + "@seat.invalid"}, {"commit.gpgsign", "false"}} {
		_ = exec.Command("git", "-C", dir, "config", kv[0], kv[1]).Run()
	}
	if branch != "" && branch != "main" {
		if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "-b", branch, "origin/"+branch).CombinedOutput(); err != nil {
			return fmt.Errorf("checkout %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// environment is the parent's, minus the harness's own session marker, plus
// the secrets file and the seat's name. Secrets never go into the process
// environment by the substrate; the seat loads them itself, on purpose.
func environment(seat, secrets string) ([]string, error) {
	env := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if key == "CLAUDECODE" || key == "SWARM_SEAT" {
			continue
		}
		env = append(env, kv)
	}
	if secrets == "" {
		if _, err := os.Stat("/run/rooms/secrets.env"); err == nil {
			secrets = "/run/rooms/secrets.env"
		}
	}
	if secrets != "" {
		loaded, err := loadSecrets(secrets)
		if err != nil {
			return nil, err
		}
		env = append(env, loaded...)
	}
	return append(env, "SWARM_SEAT="+seat), nil
}

func loadSecrets(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	defer f.Close()
	return parseSecrets(f), nil
}

// parseSecrets reads KEY=VALUE lines; blank lines and # comments are skipped.
func parseSecrets(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func defaultTools() string {
	if v := os.Getenv("TOOLS"); v != "" {
		return v
	}
	return "Read,Edit,Write,MultiEdit,Glob,Grep,Bash(git:*),Bash(go:*),Bash(swarm:*),Bash(cat:*),Bash(ls:*),Bash(gofmt:*),Bash(mkdir:*)"
}
