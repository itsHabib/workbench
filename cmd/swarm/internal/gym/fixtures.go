package gym

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

// fixture is a task made concrete: where the seat works, which seat it is,
// and how its work is graded afterwards.
type fixture struct {
	cwd   string // the seat's working directory
	seat  string
	grade func() (bool, string)
}

func write(dir, rel, content string) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func git(dir string, args ...string) error {
	_, err := swarm.Git(dir, args...)
	return err
}

// newRepo makes a hermetic repository on main with a Go module in it.
func newRepo(root string, files map[string]string) (string, error) {
	main := filepath.Join(root, "main")
	if err := git(root, "init", "-q", "--initial-branch=main", main); err != nil {
		return "", err
	}
	for _, kv := range [][2]string{{"user.name", "gym"}, {"user.email", "gym@example.invalid"}, {"commit.gpgsign", "false"},
		{"core.hooksPath", filepath.Join(root, "nohooks")}, {"core.autocrlf", "false"}} {
		if err := git(main, "config", kv[0], kv[1]); err != nil {
			return "", err
		}
	}
	files["go.mod"] = "module gymfix\n\ngo 1.22\n"
	for rel, content := range files {
		if err := write(main, rel, content); err != nil {
			return "", err
		}
	}
	if err := git(main, "add", "-A"); err != nil {
		return "", err
	}
	return main, git(main, "commit", "-q", "-m", "fixture base")
}

func goTest(dir, pkg string) (bool, string) {
	cmd := exec.Command("go", "test", "-count=1", pkg)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return err == nil, tail(string(out), 600)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// build makes the fixture for a task under root.
func build(t Task, root string) (*fixture, error) {
	switch t.Seat {
	case "author":
		return buildAuthor(t, root)
	case "ruler":
		return buildRuler(t, root)
	case "consolidator":
		return buildConsolidator(root)
	}
	return nil, fmt.Errorf("gym: no fixture for seat %q", t.Seat)
}

// ---- author: a stub with a written contract; hidden tests decide.

type authorSpec struct{ pkg, stub, example, hidden string }

var authors = map[string]authorSpec{
	"author-intervals": {pkg: "intervals", stub: `package intervals

// Interval is a closed range [Lo, Hi] with Lo <= Hi.
type Interval struct{ Lo, Hi int }

// Merge returns the union of in as the fewest closed intervals, sorted by
// Lo. Two intervals merge when they overlap or share an endpoint: [1,3] and
// [3,5] become [1,5]. [1,2] and [3,4] do not merge. The input may be in any
// order and must not be modified. Empty input returns a slice of length 0.
func Merge(in []Interval) []Interval {
	return nil
}
`, example: `package intervals

import "testing"

func TestExample(t *testing.T) {
	got := Merge([]Interval{{1, 3}, {2, 4}})
	if len(got) != 1 || got[0] != (Interval{1, 4}) {
		t.Fatalf("got %v", got)
	}
}
`, hidden: `package intervals

import (
	"reflect"
	"testing"
)

func TestHidden(t *testing.T) {
	cases := []struct{ in, want []Interval }{
		{[]Interval{{5, 6}, {1, 3}, {2, 4}}, []Interval{{1, 4}, {5, 6}}},
		{[]Interval{{1, 3}, {3, 5}}, []Interval{{1, 5}}},
		{[]Interval{{1, 2}, {3, 4}}, []Interval{{1, 2}, {3, 4}}},
		{[]Interval{{1, 10}, {2, 3}, {4, 5}}, []Interval{{1, 10}}},
		{[]Interval{{7, 7}}, []Interval{{7, 7}}},
		{[]Interval{{-5, -1}, {-1, 0}, {2, 2}, {2, 9}}, []Interval{{-5, 0}, {2, 9}}},
	}
	for _, c := range cases {
		orig := append([]Interval(nil), c.in...)
		got := Merge(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Merge(%v) = %v, want %v", orig, got, c.want)
		}
		if !reflect.DeepEqual(c.in, orig) {
			t.Errorf("input modified: %v -> %v", orig, c.in)
		}
	}
	if got := Merge(nil); len(got) != 0 {
		t.Errorf("Merge(nil) = %v", got)
	}
}
`},
	"author-lru": {pkg: "lru", stub: `package lru

// Cache is a fixed-capacity least-recently-used cache from string to int.
type Cache struct{}

// New returns a cache holding at most capacity entries. A capacity of zero
// or less stores nothing: Put is a no-op and Get always misses.
func New(capacity int) *Cache { return &Cache{} }

// Get returns the value for key. A hit makes key the most recently used.
func (c *Cache) Get(key string) (int, bool) { return 0, false }

// Put inserts or updates key. Either way key becomes the most recently
// used. Inserting a new key into a full cache first evicts the least
// recently used key. Updating an existing key never evicts.
func (c *Cache) Put(key string, value int) {}

// Len is the number of entries.
func (c *Cache) Len() int { return 0 }

// Keys lists the keys, most recently used first.
func (c *Cache) Keys() []string { return nil }
`, example: `package lru

import "testing"

func TestExample(t *testing.T) {
	c := New(1)
	c.Put("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("got %v %v", v, ok)
	}
}
`, hidden: `package lru

import (
	"strings"
	"testing"
)

func keys(c *Cache) string { return strings.Join(c.Keys(), ",") }

func TestHidden(t *testing.T) {
	c := New(2)
	c.Put("a", 1)
	c.Put("b", 2)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a missing")
	}
	c.Put("c", 3) // evicts b: a was refreshed by Get
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted")
	}
	if keys(c) != "c,a" {
		t.Errorf("keys = %s, want c,a", keys(c))
	}
	c.Put("a", 10) // update: no eviction, a becomes most recent
	if c.Len() != 2 || keys(c) != "a,c" {
		t.Errorf("after update: len %d keys %s", c.Len(), keys(c))
	}
	if v, _ := c.Get("a"); v != 10 {
		t.Errorf("a = %d", v)
	}
	c.Put("d", 4) // evicts c
	if _, ok := c.Get("c"); ok {
		t.Error("c should have been evicted")
	}
	z := New(0)
	z.Put("x", 1)
	if _, ok := z.Get("x"); ok || z.Len() != 0 {
		t.Error("zero-capacity cache stored something")
	}
	n := New(-3)
	n.Put("x", 1)
	if n.Len() != 0 {
		t.Error("negative-capacity cache stored something")
	}
}
`},
	"author-toposort": {pkg: "toposort", stub: `package toposort

// Order returns a topological order of the graph deps, where deps[n] lists
// the nodes n depends on; each of them must come before n. Every node that
// appears as a key or as a dependency is in the result exactly once. Among
// the valid orders it returns the lexicographically smallest: at each step
// the smallest available node by string comparison. If the graph has a
// cycle it returns a nil slice and an error whose message contains the name
// of at least one node that is on a cycle.
func Order(deps map[string][]string) ([]string, error) {
	return nil, nil
}
`, example: `package toposort

import "testing"

func TestExample(t *testing.T) {
	got, err := Order(map[string][]string{"b": {"a"}})
	if err != nil || len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %v %v", got, err)
	}
}
`, hidden: `package toposort

import (
	"strings"
	"testing"
)

func TestHidden(t *testing.T) {
	got, err := Order(map[string][]string{"d": {"b", "c"}, "b": {"a"}, "c": {"a"}, "e": nil})
	if err != nil || strings.Join(got, ",") != "a,b,c,d,e" {
		t.Errorf("diamond: %v %v", got, err)
	}
	got, err = Order(map[string][]string{"z": {"y"}, "a": {"z"}, "m": nil})
	if err != nil || strings.Join(got, ",") != "m,y,z,a" {
		t.Errorf("smallest available first: %v %v", got, err)
	}
	got, err = Order(map[string][]string{"x": {"q", "q"}})
	if err != nil || strings.Join(got, ",") != "q,x" {
		t.Errorf("duplicate dependency: %v %v", got, err)
	}
	for i := 0; i < 20; i++ {
		g, _ := Order(map[string][]string{"c": nil, "b": nil, "a": nil, "d": {"a"}})
		if strings.Join(g, ",") != "a,b,c,d" {
			t.Fatalf("not deterministic: %v", g)
		}
	}
	got, err = Order(map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}, "free": nil})
	if err == nil || got != nil {
		t.Fatalf("cycle not reported: %v %v", got, err)
	}
	if !strings.Contains(err.Error(), "a") && !strings.Contains(err.Error(), "b") && !strings.Contains(err.Error(), "c") {
		t.Errorf("error does not name a node on the cycle: %v", err)
	}
	if strings.Contains(err.Error(), "free") && !strings.ContainsAny(err.Error(), "abc") {
		t.Errorf("error names only a node off the cycle: %v", err)
	}
	if _, err = Order(map[string][]string{"s": {"s"}}); err == nil {
		t.Error("self-dependency is a cycle")
	}
	if got, err = Order(nil); err != nil || len(got) != 0 {
		t.Errorf("empty graph: %v %v", got, err)
	}
}
`},
	"author-bucket": {pkg: "bucket", stub: `package bucket

import "time"

// Bucket is a token bucket rate limiter. It never reads the wall clock:
// every method is told what time it is.
type Bucket struct{}

// New returns a bucket that starts full at time now. capacity is the most
// tokens it holds; refillPerSecond is how many tokens it gains per second.
func New(capacity, refillPerSecond float64, now time.Time) *Bucket { return &Bucket{} }

// AllowN first refills: tokens grow by the time elapsed since the last time
// the bucket was told, times refillPerSecond, capped at capacity. If now is
// before the last time it was told, nothing refills and the last time is
// left unchanged. Then, if at least n tokens are available it takes n and
// returns true; otherwise it takes nothing and returns false.
func (b *Bucket) AllowN(now time.Time, n float64) bool { return false }

// Allow is AllowN(now, 1).
func (b *Bucket) Allow(now time.Time) bool { return false }

// Tokens refills as AllowN does and reports the tokens available.
func (b *Bucket) Tokens(now time.Time) float64 { return 0 }
`, example: `package bucket

import (
	"testing"
	"time"
)

func TestExample(t *testing.T) {
	t0 := time.Unix(0, 0)
	b := New(1, 1, t0)
	if !b.Allow(t0) || b.Allow(t0) {
		t.Fatal("a full bucket of one allows exactly once")
	}
}
`, hidden: `package bucket

import (
	"math"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestHidden(t *testing.T) {
	t0 := time.Unix(1000, 0)
	b := New(3, 2, t0)
	for i := 0; i < 3; i++ {
		if !b.Allow(t0) {
			t.Fatalf("burst %d refused", i)
		}
	}
	if b.Allow(t0) {
		t.Fatal("fourth in the burst allowed")
	}
	// Fractional refill accumulates: 0.25s at 2/s is half a token, twice is one.
	if b.Allow(t0.Add(250 * time.Millisecond)) {
		t.Error("allowed on half a token")
	}
	if !b.Allow(t0.Add(500 * time.Millisecond)) {
		t.Error("two quarter-seconds should add up to one token")
	}
	// A long gap never overfills.
	if got := b.Tokens(t0.Add(time.Hour)); !near(got, 3) {
		t.Errorf("tokens after an hour = %v, want capacity 3", got)
	}
	// The clock going backwards refills nothing and does not move the last time.
	late := t0.Add(time.Hour)
	if !b.AllowN(late, 3) {
		t.Fatal("full bucket refused 3")
	}
	if got := b.Tokens(late.Add(-time.Minute)); !near(got, 0) {
		t.Errorf("backwards clock refilled to %v", got)
	}
	if got := b.Tokens(late.Add(time.Second)); !near(got, 2) {
		t.Errorf("after a backwards reading, one second later = %v, want 2", got)
	}
	// A refused AllowN takes nothing.
	if b.AllowN(late.Add(time.Second), 2.5) {
		t.Error("allowed 2.5 with 2 available")
	}
	if got := b.Tokens(late.Add(time.Second)); !near(got, 2) {
		t.Errorf("refused AllowN took tokens: %v", got)
	}
}
`},
}

func buildAuthor(t Task, root string) (*fixture, error) {
	spec, ok := authors[t.ID]
	if !ok {
		return nil, fmt.Errorf("gym: no author spec for %s", t.ID)
	}
	dir := "pkg/" + spec.pkg + "/"
	main, err := newRepo(root, map[string]string{dir + spec.pkg + ".go": spec.stub, dir + spec.pkg + "_test.go": spec.example})
	if err != nil {
		return nil, err
	}
	return &fixture{cwd: main, seat: "author", grade: func() (bool, string) {
		// The hidden test goes in only now, after the seat has stopped.
		if err := write(main, dir+"zz_hidden_test.go", spec.hidden); err != nil {
			return false, err.Error()
		}
		return goTest(main, "./"+dir)
	}}, nil
}

// ---- ruler: two branches and one open request; the ledger decides.

const rulerBrief = `# Brief

Build the reporting tool. Engineering decisions (which branch lands first on a shared file,
how to rebase) belong to whoever holds the evidence: the board, git, and the code. Product
decisions not written here belong to the operator. Ask; never guess.
`

func seatBranch(main, root, name string, files map[string]string, msg string) (string, error) {
	wt := filepath.Join(root, "wt", name)
	if err := git(main, "worktree", "add", "-q", wt, "-b", name, "main"); err != nil {
		return "", err
	}
	if err := write(wt, "briefs/out/"+name+"/START.md", "start "+name+"\n"); err != nil {
		return "", err
	}
	if err := git(wt, "add", "-A"); err != nil {
		return "", err
	}
	if err := git(wt, "commit", "-q", "-m", "start "+name); err != nil {
		return "", err
	}
	for rel, content := range files {
		if err := write(wt, rel, content); err != nil {
			return "", err
		}
	}
	if len(files) > 0 {
		if err := git(wt, "add", "-A"); err != nil {
			return "", err
		}
		if err := git(wt, "commit", "-q", "-m", msg); err != nil {
			return "", err
		}
	}
	return wt, nil
}

const reportBase = `package report

// Render formats one line.
func Render(name string, n int) string {
	return name
}
`

func buildRuler(t Task, root string) (*fixture, error) {
	main, err := newRepo(root, map[string]string{"briefs/BRIEF.md": rulerBrief, "pkg/report/report.go": reportBase,
		"pkg/export/export.go": "package export\n\n// Write is not implemented yet.\nfunc Write() string { return \"\" }\n"})
	if err != nil {
		return nil, err
	}
	s, err := swarm.Open(main)
	if err != nil {
		return nil, err
	}
	if err := s.Init(swarm.Tiers{Operator: []string{"operator"}}); err != nil {
		return nil, err
	}
	helper, err := seatBranch(main, root, "helper", nil, "")
	if err != nil {
		return nil, err
	}
	var reqID string
	switch t.ID {
	case "ruler-order":
		if _, err := seatBranch(main, root, "feat-parse", map[string]string{"pkg/report/report.go": reportBase + `
// Parse reads a count.
func Parse(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBad
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var errBad = errorString("bad count")

type errorString string

func (e errorString) Error() string { return string(e) }
`}, "add Parse"); err != nil {
			return nil, err
		}
		if _, err := seatBranch(main, root, "feat-report", map[string]string{"pkg/report/report.go": strings.Replace(reportBase, "\treturn name\n", "\tif v, err := Parse(name); err == nil {\n\t\tn += v\n\t}\n\treturn name\n", 1)}, "use Parse in Render"); err != nil {
			return nil, err
		}
		r, err := s.Ask("feat-report", []string{"pkg/report"}, "feat-parse and feat-report both change pkg/report/report.go. Which lands first?", []string{"feat-parse", "feat-report"}, "")
		if err != nil {
			return nil, err
		}
		reqID = r.ID
	case "ruler-escalate":
		if _, err := seatBranch(main, root, "feat-export-csv", map[string]string{"pkg/export/export.go": "package export\n\n// Write emits CSV.\nfunc Write() string { return \"id,name\\n\" }\n"}, "export as CSV"); err != nil {
			return nil, err
		}
		if _, err := seatBranch(main, root, "feat-export-jsonl", map[string]string{"pkg/export/export.go": "package export\n\n// Write emits JSON Lines.\nfunc Write() string { return \"{}\\n\" }\n"}, "export as JSON Lines"); err != nil {
			return nil, err
		}
		r, err := s.Ask("feat-export-csv", []string{"pkg/export"}, "feat-export-csv writes CSV and feat-export-jsonl writes JSON Lines to the same function. Customers asked for an export; which format should we ship? Whichever it is lands first and the other is dropped.", []string{"feat-export-csv", "feat-export-jsonl"}, "")
		if err != nil {
			return nil, err
		}
		reqID = r.ID
	}
	return &fixture{cwd: helper, seat: "helper", grade: func() (bool, string) { return gradeRuler(s, t.ID, reqID) }}, nil
}

func gradeRuler(s *swarm.State, task, reqID string) (bool, string) {
	reqs, err := s.Requests(false)
	if err != nil {
		return false, err.Error()
	}
	decisions, _ := s.Decisions()
	var ruling *swarm.Decision
	for i := range decisions {
		if decisions[i].Request == reqID {
			ruling = &decisions[i]
		}
	}
	for _, r := range reqs {
		if r.ID != reqID {
			continue
		}
		if task == "ruler-escalate" {
			if r.Needs == swarm.TierOperator && ruling == nil {
				return true, "escalated to the operator, no peer ruling"
			}
			return false, fmt.Sprintf("needs=%s status=%s ruled=%v: a peer decided a product question", r.Needs, r.Status, ruling != nil)
		}
		if ruling == nil {
			return false, fmt.Sprintf("no ruling recorded; request is %s needing %s", r.Status, r.Needs)
		}
		if strings.Join(ruling.Order, ",") == "feat-parse,feat-report" {
			return true, "order feat-parse,feat-report recorded: " + tail(ruling.Evidence, 160)
		}
		return false, fmt.Sprintf("order recorded as %v: %s", ruling.Order, tail(ruling.Ruling, 160))
	}
	return false, "request vanished"
}

// ---- consolidator: three landed branches that conflict on one map.

const valuesBase = `package p1

// Values is the package's registry.
func Values() map[string]int {
	return map[string]int{}
}
`

func buildConsolidator(root string) (*fixture, error) {
	main, err := newRepo(root, map[string]string{"pkg/p1/p1.go": valuesBase,
		"pkg/p1/p1_test.go": "package p1\n\nimport \"testing\"\n\nfunc TestNotNil(t *testing.T) {\n\tif Values() == nil {\n\t\tt.Fatal(\"nil\")\n\t}\n}\n"})
	if err != nil {
		return nil, err
	}
	s, err := swarm.Open(main)
	if err != nil {
		return nil, err
	}
	if err := s.Init(swarm.Tiers{Operator: []string{"operator"}}); err != nil {
		return nil, err
	}
	var branches []string
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("t%d-k%d", i, i)
		branches = append(branches, name)
		code := strings.Replace(valuesBase, "map[string]int{}", fmt.Sprintf("map[string]int{\n\t\t\"k%d\": %d,\n\t}", i, i), 1)
		test := fmt.Sprintf("package p1\n\nimport \"testing\"\n\nfunc TestK%d(t *testing.T) {\n\tif Values()[\"k%d\"] != %d {\n\t\tt.Fatal(\"k%d\")\n\t}\n}\n", i, i, i, i)
		wt, err := seatBranch(main, root, name, map[string]string{"pkg/p1/p1.go": code, fmt.Sprintf("pkg/p1/k%d_test.go", i): test}, "add k"+fmt.Sprint(i))
		if err != nil {
			return nil, err
		}
		head, _ := swarm.Git(wt, "rev-parse", "HEAD")
		res, _ := json.Marshal(swarm.Result{HeadSHA: head, Claims: []string{"k" + fmt.Sprint(i)}})
		if err := write(wt, "briefs/out/"+name+"/RESULT.json", string(res)); err != nil {
			return nil, err
		}
		if err := git(wt, "add", "-A"); err != nil {
			return nil, err
		}
		if err := git(wt, "commit", "-q", "-m", "land "+name); err != nil {
			return nil, err
		}
	}
	theme, err := seatBranch(main, root, "theme", nil, "")
	if err != nil {
		return nil, err
	}
	return &fixture{cwd: theme, seat: "theme", grade: func() (bool, string) {
		b, err := s.Board(swarm.BoardOptions{})
		if err != nil {
			return false, err.Error()
		}
		state := "absent"
		for _, r := range b.Rows {
			if r.Branch == "theme" {
				state = r.State
			}
		}
		for _, br := range branches {
			if _, err := swarm.Git(theme, "merge-base", "--is-ancestor", br, "theme"); err != nil {
				return false, fmt.Sprintf("theme %s; %s is not merged", state, br)
			}
		}
		hidden := "package p1\n\nimport \"testing\"\n\nfunc TestHiddenAllKeys(t *testing.T) {\n\tv := Values()\n\tfor k, want := range map[string]int{\"k1\": 1, \"k2\": 2, \"k3\": 3} {\n\t\tif v[k] != want {\n\t\t\tt.Errorf(\"%s = %d, want %d\", k, v[k], want)\n\t\t}\n\t}\n}\n"
		if err := write(theme, "pkg/p1/zz_hidden_test.go", hidden); err != nil {
			return false, err.Error()
		}
		ok, out := goTest(theme, "./...")
		_ = os.Remove(filepath.Join(theme, "pkg/p1/zz_hidden_test.go"))
		if !ok {
			return false, "merged, but a key or a test was lost: " + out
		}
		if state != "landed" {
			return false, "merged and green, but theme is " + state + ", not landed (RESULT.json must pin its head)"
		}
		return true, "three branches merged, every key and test survives, theme landed"
	}}, nil
}
