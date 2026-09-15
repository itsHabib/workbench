package foundation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T, fault Fault) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := Open(dir, fault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws, dir
}

func TestPathsStayInsideTheWorkspace(t *testing.T) {
	ws, dir := open(t, Fault{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x", "./a", "", ".", ".wb/evidence.jsonl", ".wb", ".WB/evidence.jsonl", ".Wb"} {
		if err := ws.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) succeeded, want refusal", p)
		}
	}
	// A clean path that resolves through a symlink to outside is refused
	// by os.Root, and nothing lands outside.
	if err := ws.WriteFile("escape/x.txt", []byte("x")); err == nil {
		t.Error("write through an escaping symlink succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("files appeared outside the workspace: %v", entries)
	}
}

func TestWriteFileIsAtomicReplace(t *testing.T) {
	ws, dir := open(t, Fault{})
	if err := ws.WriteFile("a/b.txt", []byte("one")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(dir, "a/b.txt"))
	if err := ws.WriteFile("a/b.txt", []byte("two")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(dir, "a/b.txt"))
	if os.SameFile(before, after) {
		t.Error("rewrite reused the inode; want a renamed-in replacement")
	}
	st, err := ws.ReadFile("a/b.txt")
	if err != nil || string(st.Content) != "two" || st.Digest != Digest([]byte("two")) {
		t.Fatalf("ReadFile = %+v, %v", st, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "a"))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestExecCommitsOnlyOnSuccess(t *testing.T) {
	ws, _ := open(t, Fault{})
	ctx := context.Background()
	if err := ws.WriteFile("in.txt", []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	ok := ExecSpec{Label: "up", Argv: []string{"tr", "a-z", "A-Z"}, Stdin: "in.txt", Output: "out.txt"}
	st, err := ws.Exec(ctx, ok)
	if err != nil || string(st.Content) != "HELLO\n" {
		t.Fatalf("Exec = %+v, %v", st, err)
	}
	bad := ExecSpec{Label: "bad", Argv: []string{"sh", "-c", "echo partial; echo boom >&2; exit 3"}, Stdin: "in.txt", Output: "out.txt"}
	_, err = ws.Exec(ctx, bad)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failing Exec error = %v, want exit error with stderr", err)
	}
	st, _ = ws.ReadFile("out.txt")
	if string(st.Content) != "HELLO\n" {
		t.Errorf("failed run replaced the previous output with %q", st.Content)
	}
}

func TestFailFaultStopsBeforeTheChild(t *testing.T) {
	ws, _ := open(t, Fault{Fail: "wc"})
	if err := ws.WriteFile("in.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	_, err := ws.Exec(context.Background(), ExecSpec{Label: "wc", Argv: []string{"cat"}, Stdin: "in.txt", Output: "o.txt"})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("err = %v, want injected failure", err)
	}
	if st, _ := ws.ReadFile("o.txt"); st.Exists {
		t.Error("output written despite injected failure")
	}
}

func TestParseFault(t *testing.T) {
	cases := map[string]Fault{"": {}, "fail:a.b": {Fail: "a.b"}, "crash:a.b": {Crash: "a.b"}}
	for in, want := range cases {
		got, err := ParseFault(in)
		if err != nil || got != want {
			t.Errorf("ParseFault(%q) = %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"fail", "fail:", "explode:x"} {
		if _, err := ParseFault(in); err == nil {
			t.Errorf("ParseFault(%q) accepted", in)
		}
	}
}

func TestEvidenceAppendsInSequence(t *testing.T) {
	ws, _ := open(t, Fault{})
	for i := range 2 {
		r, err := ws.Record(Record{Event: EventStart, Subject: "t"})
		if err != nil || r.Seq != i+1 {
			t.Fatalf("Record #%d = %+v, %v", i+1, r, err)
		}
	}
}

func TestEvidenceSurvivesATornLine(t *testing.T) {
	ws, dir := open(t, Fault{})
	for range 2 {
		if _, err := ws.Record(Record{Event: EventStart, Subject: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a crash in the middle of an append.
	f, err := os.OpenFile(filepath.Join(dir, journalName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":3,"event":"fin`)
	_ = f.Close()

	ev, err := ws.Evidence()
	if err != nil || len(ev.Records) != 2 || ev.Unreadable != 1 {
		t.Fatalf("Evidence = %+v, %v; want 2 records and 1 unreadable", ev, err)
	}
	r, err := ws.Record(Record{Event: EventFinish, Subject: "t", Status: StatusOK})
	if err != nil || r.Seq != 3 {
		t.Fatalf("Record after torn line = %+v, %v", r, err)
	}
	ev, _ = ws.Evidence()
	if len(ev.Records) != 3 || ev.Head() != 3 || ev.Unreadable != 1 {
		t.Fatalf("after append: %+v; want the torn fragment isolated and the new record readable", ev)
	}
}
