package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driftFixture binds three directories: two seats of one kind and an unbound tree
// beside them. It returns the parent, so a test can name any path under it.
func driftFixture(t *testing.T) (parent, one, two, loose string) {
	t.Helper()
	oldState, oldOrg := State, OrgState
	State, OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	parent = t.TempDir()
	one = filepath.Join(parent, "bench-hand-1")
	two = filepath.Join(parent, "bench-hand-2")
	loose = filepath.Join(parent, "scratch")
	for _, d := range []string{one, two, loose, filepath.Join(one, "src")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	text := one + " mh hand:bench bench-hand-1\n" + two + " mh hand:bench bench-hand-2\n"
	if err := os.WriteFile(RolesMap(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return parent, one, two, loose
}

func TestCdIntoAnotherBoundDirectoryIsRefused(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	reason := cdDestinations("Bash", "cd "+two+" && git status", one)
	if reason == "" {
		t.Fatal("a session in one seat may not cd into another")
	}
	for _, want := range []string{"bench-hand-2", "hand:bench", "git -C " + two, "(cd " + two + " &&"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("refusal does not carry %q:\n%s", want, reason)
		}
	}
}

func TestCdRefusalNamesTheRoleWhenThereIsNoSeatName(t *testing.T) {
	_, one, _, _ := driftFixture(t)
	lead := filepath.Join(filepath.Dir(one), "lead")
	if err := os.MkdirAll(lead, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(RolesMap(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(lead + " mh hub:bench\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	reason := cdDestinations("Bash", "cd "+lead, one)
	if !strings.Contains(reason, "hub:bench") {
		t.Fatalf("refusal does not name the role:\n%s", reason)
	}
}

func TestAnUnroledSessionIsRefusedToo(t *testing.T) {
	_, one, _, loose := driftFixture(t)
	reason := cdDestinations("Bash", "cd "+one+" && ls", loose)
	if reason == "" || !strings.Contains(reason, "holds no bound directory of its own") {
		t.Fatalf("an unroled session must be refused a bound directory:\n%s", reason)
	}
}

func TestMovementInsideTheSessionsOwnTreeIsAllowed(t *testing.T) {
	_, one, _, loose := driftFixture(t)
	cases := []struct {
		name, cmd, cwd string
	}{
		{"deeper into its own seat", "cd " + filepath.Join(one, "src") + " && go test ./...", one},
		{"a relative hop inside its own seat", "cd src && ls", one},
		{"back up to itself", "cd " + one, filepath.Join(one, "src")},
		{"an unbound directory", "cd " + loose + " && ls", one},
	}
	for _, c := range cases {
		if reason := cdDestinations("Bash", c.cmd, c.cwd); reason != "" {
			t.Fatalf("%s must be allowed: %s", c.name, reason)
		}
	}
}

func TestNamingAnotherSeatWithoutMovingIsAllowed(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	cases := []struct{ name, cmd string }{
		{"an absolute path as an operand", "ls " + two + "/src"},
		{"git -C", "git -C " + two + " status"},
		{"a subshell that returns here", "(cd " + two + " && git status)"},
		{"a command substitution", "head=$(cd " + two + " && git rev-parse HEAD)"},
		{"the path inside a quoted string", "echo \"cd " + two + "\""},
		{"a longer word that merely starts with cd", "cdk deploy " + two},
	}
	for _, c := range cases {
		if reason := cdDestinations("Bash", c.cmd, one); reason != "" {
			t.Fatalf("%s must be allowed: %s", c.name, reason)
		}
	}
}

func TestTheGuardOnlyLooksAtBashAndOnlyAtCd(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	if reason := cdDestinations("Read", "cd "+two, one); reason != "" {
		t.Fatalf("only Bash moves a session: %s", reason)
	}
	if reason := cdDestinations("Bash", "", one); reason != "" {
		t.Fatalf("an empty command moves nothing: %s", reason)
	}
	if reason := cdDestinations("Bash", "pushd "+two, one); reason == "" {
		t.Fatal("pushd moves the session exactly as cd does")
	}
}

func TestCdTargetsResolution(t *testing.T) {
	// Absolute paths are the platform's own: a bare /abs is a relative path on Windows.
	here := t.TempDir()
	there := filepath.Join(here, "there")
	cases := []struct {
		cmd, cwd, want string
	}{
		{"cd " + there, here, there},
		{"cd sub && ls", here, filepath.Join(here, "sub")},
		{"cd '" + there + "'", here, there},
		{"ls && cd " + there, here, there},
		{"cd -", here, ""},
		{"cd", here, ""},
		{"cd ../sibling", filepath.Join(here, "deep"), filepath.Join(here, "sibling")},
	}
	for _, c := range cases {
		got := CdTargets(c.cmd, c.cwd)
		if c.want == "" && len(got) > 0 {
			t.Fatalf("%q resolved to %v, wanted nothing", c.cmd, got)
		}
		if c.want != "" && (len(got) != 1 || got[0] != c.want) {
			t.Fatalf("%q resolved to %v, wanted [%s]", c.cmd, got, c.want)
		}
	}
}

// Each top-level cd is resolved against where the preceding one left the shell, not
// against the event's cwd: `cd /tmp && cd seat2` ends in the other seat.
func TestChainedCdIsResolvedAgainstThePrecedingHop(t *testing.T) {
	parent, one, two, loose := driftFixture(t)
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cd " + parent + " && cd " + filepath.Base(two), []string{parent, two}},
		{"cd " + loose + " && cd ../" + filepath.Base(two) + " && git commit", []string{loose, two}},
		{"cd src && cd ..", []string{filepath.Join(one, "src"), one}},
	}
	for _, c := range cases {
		got := CdTargets(c.cmd, one)
		if len(got) != len(c.want) {
			t.Fatalf("%q resolved to %v, wanted %v", c.cmd, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q resolved to %v, wanted %v", c.cmd, got, c.want)
			}
		}
	}
	// And the guard sees the seat the chain actually ends in.
	if reason := cdDestinations("Bash", "cd "+parent+" && cd "+filepath.Base(two)+" && git commit -m x", one); reason == "" {
		t.Fatal("a chained cd into another seat must be refused")
	}
	// A chain that stays inside this session's own tree is still allowed.
	if reason := cdDestinations("Bash", "cd src && cd .. && go test ./...", one); reason != "" {
		t.Fatalf("a chain that ends in its own seat must be allowed: %s", reason)
	}
}

// A hop whose destination is not in the command makes every later relative hop
// unreadable. The guard refuses rather than measuring it from the wrong base.
func TestARelativeHopFromAnUnresolvableBaseIsRefused(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	if _, unresolved := CdChain("cd - && cd "+filepath.Base(two), one); !unresolved {
		t.Fatal("a relative hop after `cd -` cannot be resolved")
	}
	reason := cdDestinations("Bash", "cd - && cd "+filepath.Base(two)+" && git commit -m x", one)
	if reason == "" || !strings.Contains(reason, "cannot resolve") {
		t.Fatalf("an unresolvable chain must be refused:\n%s", reason)
	}
	// `cd -` on its own names no destination and is left alone, as it always was.
	if reason := cdDestinations("Bash", "cd - && ls", one); reason != "" {
		t.Fatalf("a lone `cd -` moves nowhere this guard can name: %s", reason)
	}
	// An absolute hop after it is still readable: the base does not matter.
	if _, unresolved := CdChain("cd - && cd "+two, one); unresolved {
		t.Fatal("an absolute destination needs no base")
	}
}

func TestBoundDirIsLongestPrefix(t *testing.T) {
	parent, one, _, loose := driftFixture(t)
	row, ok := BoundDir(filepath.Join(one, "src", "deep"))
	if !ok || row.Slot != "bench-hand-1" {
		t.Fatalf("a path under a seat belongs to it: %v %v", row, ok)
	}
	if _, ok := BoundDir(loose); ok {
		t.Fatal("an unbound directory has no row")
	}
	if _, ok := BoundDir(parent); ok {
		t.Fatal("the parent of two seats is not bound by them")
	}
}

// The order of the pre-write refusals is load-bearing, not cosmetic. `cd /other-seat &&
// git commit` targets the other seat, so a lease check ahead of the directory guard
// TAKES that seat's branch lease and only then refuses the call — and the refusal
// releases nothing, so this session is left holding a branch it was never allowed to
// touch, locking out the seat's real occupant.
func TestTheDirectoryGuardRunsBeforeTheLeaseIsTaken(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	cmd := "cd " + two + " && git commit -m x"
	target := BashTarget(cmd, one)
	branch := "feature/theirs"
	key := Scope(target, branch)
	_, v := preWriteVerdicts(Event{}, Rec{}, "sess-drifter", "Bash", cmd, target, branch, key, one, true)
	if v == nil || v.Code != 2 {
		t.Fatalf("the move into another seat must be refused: %v", v)
	}
	if !strings.Contains(v.Err, "bench-hand-2") {
		t.Fatalf("refusal is not the directory guard's:\n%s", v.Err)
	}
	if lease := Lease(key); lease != nil {
		t.Fatalf("a lease was taken on the other seat's branch before the guard refused: %v", lease)
	}
}
