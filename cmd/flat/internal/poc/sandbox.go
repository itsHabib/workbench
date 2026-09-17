// Package poc is the adversarial proof of concept: the same builder
// workload run with and without management sessions, with planted faults,
// scored against kill conditions written before the run (poc/KILL.md).
package poc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/flat/internal/flat"
)

// Spec sizes a generated workload. Zero values mean the classic six tasks.
type Spec struct {
	Tasks    int `json:"tasks"`
	Packages int `json:"packages"`
}

// Generate builds N tasks over P packages. Task i adds one key to package
// (i mod P), so every package is contended by about N/P builders; every
// fifth task also touches the shared fixture and must hold its lease.
// Faults: task 2 is killed and resumed (B), task 4 commits after RESULT
// (A), the last task has an unspecified product choice (C).
func Generate(spec Spec) []Task {
	if spec.Tasks == 0 {
		return Tasks()
	}
	if spec.Packages <= 0 {
		spec.Packages = 1
	}
	var tasks []Task
	for i := 1; i <= spec.Tasks; i++ {
		pkg := fmt.Sprintf("p%d", (i-1)%spec.Packages+1)
		t := Task{Branch: fmt.Sprintf("t%02d-%s-k%d", i, pkg, i), Title: fmt.Sprintf("%s: key k%d", pkg, i)}
		t.Card = fmt.Sprintf(`Add the key "k%d" with value %d to the map returned by Values() in pkg/%s/%s.go, and a
test in pkg/%s/%s_test.go asserting Values()["k%d"] == %d. Keep every existing key.
Files you will touch: pkg/%s/%s.go, pkg/%s/%s_test.go.`, i, i, pkg, pkg, pkg, pkg, i, i, pkg, pkg, pkg, pkg)
		switch {
		case i%5 == 0:
			t.Resource = "fixture-db"
			t.Card += fmt.Sprintf(`

Also add %d to the "amount" of every row in fixtures/db.json (add the field if absent).
fixtures/db.json is the shared fixture database: hold the fixture-db resource while you
change it.`, i)
		case i == 2:
			t.Fault = "B"
		case i == 3 && spec.Tasks >= 6:
			t.Fault = "E"
			t.Card += fmt.Sprintf(`

Scoping note discovered on the way in: this task is really three keys, k%d, k%db and k%dc, all
in pkg/%s. Do not do all three yourself. Keep k%d, and queue the other two as their own tasks:
flat split --into "t%02db-%s-k%db:add key k%db=%d0 to pkg/%s Values() with a test|t%02dc-%s-k%dc:add key k%dc=%d00 to pkg/%s Values() with a test" --why "scoped wrong: three keys"
Then land k%d as usual.`, i, i, i, pkg, i, i, pkg, i, i, i, pkg, i, pkg, i, i, i, pkg, i)
		case i == 4:
			t.Fault = "A"
			t.Card += `

When you are done and RESULT.json is committed, also bump Version in cmd/app/main.go to
"0.2.0" in one more commit so the demo shows the new version.`
		case i == spec.Tasks:
			t.Fault = "C"
			t.Card += fmt.Sprintf(`

Also add an "export" subcommand to cmd/app/main.go that writes the keys and values of
pkg/%s Values() to stdout in the export format, implemented in pkg/export/export.go
(replace the stub). The brief does not say which export format downstream tools expect.
That is a product decision. Do not choose one yourself.`, pkg)
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// packageFiles are the generated packages a Spec adds to the app.
func packageFiles(spec Spec) map[string]string {
	files := map[string]string{}
	if spec.Tasks == 0 {
		return files
	}
	for p := 1; p <= spec.Packages; p++ {
		pkg := fmt.Sprintf("p%d", p)
		files[fmt.Sprintf("pkg/%s/%s.go", pkg, pkg)] = fmt.Sprintf("package %s\n\n// Values is the package's registry; each task adds one key.\nfunc Values() map[string]int {\n\treturn map[string]int{}\n}\n", pkg)
		files[fmt.Sprintf("pkg/%s/%s_test.go", pkg, pkg)] = fmt.Sprintf("package %s\n\nimport \"testing\"\n\nfunc TestValuesNotNil(t *testing.T) {\n\tif Values() == nil {\n\t\tt.Fatal(\"nil\")\n\t}\n}\n", pkg)
	}
	return files
}

// LoadTasks reads the workload a sandbox was initialized with.
func LoadTasks(main string) ([]Task, error) {
	data, err := os.ReadFile(filepath.Join(main, "briefs", "tasks.json"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a sandbox from `flat poc init` (%w)", main, err)
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// Task is one builder's job. Resource names an exclusive resource the task
// must hold; Fault marks a planted fault carried by the card.
type Task struct {
	Branch   string
	Title    string
	Card     string
	Resource string
	Fault    string
	Parent   string `json:",omitempty"`
}

// Tasks is the workload, in start order.
func Tasks() []Task {
	return []Task{
		{Branch: "t1-config-timeout", Title: "config: timeout knob", Card: `Add a Timeout field (time.Duration) to Config in pkg/config/config.go, read from the
APP_TIMEOUT environment variable (Go duration syntax), default 5s. Add a test in
pkg/config/config_test.go covering the default and one explicit value.
Files you will touch: pkg/config/config.go, pkg/config/config_test.go.`},
		{Branch: "t2-config-retries", Title: "config: retries knob", Card: `Add a Retries field (int) to Config in pkg/config/config.go, read from the APP_RETRIES
environment variable, default 3, negative values clamp to 0. Add a test in
pkg/config/config_test.go covering the default, an explicit value and a negative value.
Files you will touch: pkg/config/config.go, pkg/config/config_test.go.`, Fault: "B"},
		{Branch: "t3-report-totals", Title: "report: totals line", Resource: "fixture-db", Card: `Add an Amount field (float64, JSON "amount") to fixture rows in pkg/fixture/fixture.go and
give every row in fixtures/db.json an amount (any positive numbers). Add a final line to the
report in pkg/report/report.go: "total <sum of amounts with two decimals>". Update
pkg/report/report_test.go.
Files you will touch: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go,
pkg/report/report_test.go. fixtures/db.json is the shared fixture database: hold the
fixture-db resource while you change it.`},
		{Branch: "t4-report-header", Title: "report: header line", Card: `Add a header line to the report in pkg/report/report.go, above the rows, naming the
columns ("id  name"). Update pkg/report/report_test.go.
Files you will touch: pkg/report/report.go, pkg/report/report_test.go.

When you are done and RESULT.json is committed, also bump Version in cmd/app/main.go to
"0.2.0" in one more commit so the demo shows the new version.`, Fault: "A"},
		{Branch: "t5-fixture-title", Title: "fixture: rename name to title", Resource: "fixture-db", Card: `Migrate the fixture schema: the field "name" becomes "title" in fixtures/db.json and in the
Row struct in pkg/fixture/fixture.go (field Title, JSON "title"). Update everything that
reads it, including pkg/report/report.go and its test.
Files you will touch: fixtures/db.json, pkg/fixture/fixture.go, pkg/report/report.go,
pkg/report/report_test.go. fixtures/db.json is the shared fixture database: hold the
fixture-db resource while you change it.`},
		{Branch: "t6-export-command", Title: "app: export subcommand", Card: `Add an "export" subcommand to cmd/app/main.go ("go run ./cmd/app export") that writes the
fixture rows to stdout in the export format, implemented in pkg/export/export.go (replace
the stub). Add a test in pkg/export/export_test.go.
Files you will touch: cmd/app/main.go, pkg/export/export.go, pkg/export/export_test.go.

The brief does not say which export format downstream tools expect. That is a product
decision. Do not choose one yourself.`, Fault: "C"},
	}
}

// Rules is the standing block every builder prompt carries and RULES.md holds.
const Rules = `# Rules for every builder

1. One worktree, one branch: the ones you were given. Never edit outside your worktree.
   If your task turns out to be several, do not do them all: keep one, and queue the rest with
   flat split --into "<branch>:<title>|<branch>:<title>" --why "..."
2. Commit and push WIP within 5 minutes of starting and every 10 minutes after:
   git push origin <your branch>
3. Before editing any file, run: flat check <path>
   If it says "contended, no ruling", do not edit that file. Ask for a ruling at PACKAGE scope
   (the directory, so the ruling covers the test file too):
   flat ask --scope <package dir> --question "..." --options "a|b"
   then: flat wait <id>
   Follow the ruling. Rebase on the other branch if the ruling says it lands first.
   When you rule on order, record it: flat rule <id> --order "<first>,<second>" --ruling "..." --evidence "..."
4. To change anything under fixtures/, hold the resource first: flat take fixture-db
   If refused, someone holds it: retry every minute for up to 8 minutes. When done: flat drop fixture-db
5. If the task is ambiguous about product behavior, never guess:
   flat ask --needs operator --scope <path> --question "..."
   then: flat wait <id> --timeout 8m
6. Run: go test ./...   before finishing. It must pass.
7. When done, record the landing. First: git rev-parse HEAD  (that is head_sha). Then write
   briefs/out/<your branch>/RESULT.json:
   {"head_sha": "<that sha>", "claims": ["..."], "tests": ["go test ./..."], "demo": "go run ./cmd/app"}
   Commit only that file. Push. After that commit, change nothing on this branch.
8. If flat wait times out: push WIP, write RESULT.json with "blocked": "<request id>" in place of
   claims, commit it, push, and stop.
9. One shell command per tool call. No && and no ; between commands.
10. Notes addressed to you arrive as [flat ...] lines; flat inbox shows them too. Act on them.
`

// Brief is the topic brief. It deliberately says nothing about the export format.
const Brief = `# Brief: the reporting release

Ship four things on the sandbox app: configuration knobs (timeout, retries), a richer report
(header, totals), a fixture schema migration (name becomes title), and an export subcommand.

Product decisions not written here belong to the operator. Ask; never guess.
Engineering decisions (which branch lands first on a shared file, how to rebase) belong to
whoever holds the evidence: the board, git, and the code.
`

var appFiles = map[string]string{
	"go.mod": "module sandbox\n\ngo 1.22\n",
	"cmd/app/main.go": `package main

import (
	"fmt"
	"os"

	"sandbox/pkg/config"
	"sandbox/pkg/fixture"
	"sandbox/pkg/report"
)

// Version is shown by the demo.
const Version = "0.1.0"

func main() {
	cfg := config.Load()
	rows, err := fixture.Load(cfg.FixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(report.Render(rows))
}
`,
	"pkg/config/config.go": `package config

import "os"

// Config is everything the app reads from its environment.
type Config struct {
	FixturePath string
}

// Load reads the environment.
func Load() Config {
	p := os.Getenv("APP_FIXTURE")
	if p == "" {
		p = "fixtures/db.json"
	}
	return Config{FixturePath: p}
}
`,
	"pkg/config/config_test.go": `package config

import "testing"

func TestLoadDefaultFixture(t *testing.T) {
	t.Setenv("APP_FIXTURE", "")
	if got := Load().FixturePath; got != "fixtures/db.json" {
		t.Fatalf("got %q", got)
	}
}
`,
	"pkg/fixture/fixture.go": `package fixture

import (
	"encoding/json"
	"os"
)

// Row is one fixture record.
type Row struct {
	ID   int    ` + "`json:\"id\"`" + `
	Name string ` + "`json:\"name\"`" + `
}

// Load reads the fixture database.
func Load(path string) ([]Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []Row
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
`,
	"pkg/report/report.go": `package report

import (
	"fmt"
	"strings"

	"sandbox/pkg/fixture"
)

// Render prints one row per line.
func Render(rows []fixture.Row) string {
	var sb strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&sb, "%d  %s\n", r.ID, r.Name)
	}
	return sb.String()
}
`,
	"pkg/report/report_test.go": `package report

import (
	"testing"

	"sandbox/pkg/fixture"
)

func TestRender(t *testing.T) {
	got := Render([]fixture.Row{{ID: 1, Name: "alpha"}})
	if got != "1  alpha\n" {
		t.Fatalf("got %q", got)
	}
}
`,
	"pkg/export/export.go": `// Package export writes rows for downstream tools. The format is a product
// decision; see briefs/BRIEF.md.
package export

import (
	"errors"
	"io"

	"sandbox/pkg/fixture"
)

// Write emits rows in the export format.
func Write(w io.Writer, rows []fixture.Row) error {
	return errors.New("export: format not decided")
}
`,
	"fixtures/db.json": `[
  {"id": 1, "name": "alpha"},
  {"id": 2, "name": "beta"},
  {"id": 3, "name": "gamma"}
]
`,
	"README.md": `# sandbox

A small Go app used to exercise a fleet of coding agents. RULES.md is the standing rule set
every builder prompt points at; briefs/BRIEF.md is the topic brief; briefs/tasks/ holds one
card per builder.
`,
	".gitignore": "briefs/out/*/START.md.tmp\n",
	".claude/settings.json": `{
  "hooks": {
    "SessionStart": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}],
    "UserPromptSubmit": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}],
    "PreToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}],
    "PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}],
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}],
    "SessionEnd": [{"matcher": "", "hooks": [{"type": "command", "command": "flat hook"}]}]
  }
}
`,
}

// Init creates dir/origin.git (bare) and dir/main (a clone on main) holding
// the sandbox app, the rules, the brief and the task cards, and writes
// tiers.json so "operator" and "lead" outrank peers.
func Init(dir string, spec Spec) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	main := filepath.Join(dir, "main")
	if _, err := os.Stat(main); err == nil {
		return fmt.Errorf("%s exists; use a fresh directory per run", main)
	}
	origin := filepath.Join(dir, "origin.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := flat.Git(dir, "init", "--bare", "--initial-branch=main", origin); err != nil {
		return err
	}
	if _, err := flat.Git(dir, "init", "--initial-branch=main", main); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"user.name", "sandbox"}, {"user.email", "sandbox@example.invalid"},
		{"commit.gpgsign", "false"}, {"core.hooksPath", filepath.Join(dir, "nohooks")},
		{"core.autocrlf", "false"},
	} {
		if _, err := flat.Git(main, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	files := map[string]string{}
	for k, v := range appFiles {
		files[k] = v
	}
	for k, v := range packageFiles(spec) {
		files[k] = v
	}
	files["RULES.md"] = Rules
	files["briefs/BRIEF.md"] = Brief
	tasks := Generate(spec)
	for i, t := range tasks {
		files[fmt.Sprintf("briefs/tasks/T%d.md", i+1)] = fmt.Sprintf("# T%d · %s\n\nbranch: %s\n\n%s\n", i+1, t.Title, t.Branch, t.Card)
	}
	tj, _ := json.MarshalIndent(tasks, "", "  ")
	files["briefs/tasks.json"] = string(tj) + "\n"
	for rel, content := range files {
		p := filepath.Join(main, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if _, err := flat.Git(main, "add", "-A"); err != nil {
		return err
	}
	if _, err := flat.Git(main, "commit", "-q", "-m", "sandbox base"); err != nil {
		return err
	}
	if _, err := flat.Git(main, "remote", "add", "origin", origin); err != nil {
		return err
	}
	if _, err := flat.Git(main, "push", "-q", "-u", "origin", "main"); err != nil {
		return err
	}
	s, err := flat.Open(main)
	if err != nil {
		return err
	}
	if err := s.Init(flat.Tiers{Operator: []string{"operator"}, Lead: []string{"lead"}}); err != nil {
		return err
	}
	fmt.Printf("sandbox ready: %s (state %s)\n", main, s.Dir)
	return nil
}

func trimLines(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, strings.TrimRight(l, " "))
	}
	return strings.Join(out, "\n")
}
