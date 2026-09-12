package fleet

import (
	"runtime"
	"testing"
)

// A quoted Windows path keeps its separators on every platform: inside double quotes
// the shell escapes only $ ` " \ and newline, and the guard must read the operand the
// way the shell will (#320).
func TestQuotedBackslashesSurviveTheLexer(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`cd "C:\Users\seat"`, `C:\Users\seat`},
		{`cd "a\"b"`, `a"b`},
		{`cd "a\$b"`, `a$b`},
		{`cd "a\\b"`, `a\b`},
		{`cd "a\nb"`, `a\nb`},
	}
	for _, c := range cases {
		got := shellWords(c.cmd)
		if len(got) != 2 || got[1] != c.want {
			t.Fatalf("%s lexed to %q, wanted [cd %s]", c.cmd, got, c.want)
		}
	}
}

// Unquoted, the POSIX shell drops the backslash; on Windows the guard keeps it as a
// separator, because the drive-relative residue matched nothing bound and the guard
// waved the move through.
func TestUnquotedBackslashByPlatform(t *testing.T) {
	if got := shellWords(`git checkout a\b`); len(got) != 3 || got[2] != `ab` {
		t.Fatalf("the other guards keep POSIX lexing, got %q", got)
	}
	got := cdWords(`cd C:\Users\seat`)
	want := `C:Usersseat`
	if runtime.GOOS == "windows" {
		want = `C:\Users\seat`
	}
	if len(got) != 2 || got[1] != want {
		t.Fatalf("lexed to %q, wanted [cd %s] on %s", got, want, runtime.GOOS)
	}
}

// A drive-relative operand is unresolved, and unresolved is refused, never joined to
// the cwd into a path nothing is bound to.
func TestDriveRelativeCdIsUnresolved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-relative paths exist only on Windows")
	}
	for _, cmd := range []string{`cd C:Usersseat && ls`, `cd C: && ls`} {
		targets, unresolved := CdChain(cmd, `C:\tmp\seat`)
		if !unresolved || len(targets) != 0 {
			t.Fatalf("%q must be unresolved with no target, got %v", cmd, targets)
		}
	}
	targets, unresolved := CdChain(`cd C:\other\seat && ls`, `C:\tmp\seat`)
	if unresolved || len(targets) != 1 || targets[0] != `C:\other\seat` {
		t.Fatalf("an unquoted absolute Windows path must resolve as written, got %v (unresolved=%v)", targets, unresolved)
	}
}

func TestDriveRelativeDetection(t *testing.T) {
	if runtime.GOOS != "windows" {
		if driveRelative(`C:seat`) || driveRelative(`/tmp/seat`) {
			t.Fatal("there are no volumes here")
		}
		return
	}
	for p, want := range map[string]bool{`C:seat`: true, `C:`: true, `C:\seat`: false, `\\host\share\x`: false, `seat`: false, `\seat`: false} {
		if driveRelative(p) != want {
			t.Fatalf("driveRelative(%q) = %v, wanted %v", p, !want, want)
		}
	}
}
