package tower

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeRunner struct {
	stdout []byte
	err    error
	args   []string
}

func (f *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	f.args = append([]string{executable}, args...)
	return f.stdout, f.err
}

func TestCollectNormalizesOpaqueWorktree(t *testing.T) {
	f := &fakeRunner{stdout: []byte(`[{"worktree":{"repo":"repo","branch":"main","path":"C:\\outside\\repo","head":"abc","dirty":true,"ahead":2,"behind":1,"last_commit":"2026-07-13T11:45:00Z"},"extra":true}]`)}
	a := New("tower.exe")
	a.runner = f
	a.now = func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) }

	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Worktrees) != 1 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Worktrees[0].Path != `C:\outside\repo` {
		t.Fatalf("path was not retained opaquely: %q", got.Worktrees[0].Path)
	}
	wantArgs := []string{"tower.exe", "ls", "--json", "--no-reconcile"}
	if !reflect.DeepEqual(f.args, wantArgs) {
		t.Fatalf("argv = %#v, want %#v", f.args, wantArgs)
	}
}

func TestCollectRejectsDuplicateIdentity(t *testing.T) {
	f := &fakeRunner{stdout: []byte(`[{"worktree":{"repo":"repo","branch":"main"}},{"worktree":{"repo":"repo","branch":"main"}}]`)}
	a := New("tower")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "duplicate_identity" || len(got.Worktrees) != 0 {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestCollectMissingExecutableIsNormalUnavailable(t *testing.T) {
	a := New("tower")
	a.runner = &fakeRunner{err: &exec.Error{Name: "tower", Err: errors.New("missing")}}
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceUnavailable || got.Receipt.ErrorCode != "executable_not_found" {
		t.Fatalf("unexpected receipt: %#v", got.Receipt)
	}
}

func TestCollectAbsoluteMissingExecutableIsNormalUnavailable(t *testing.T) {
	a := New(`C:\tools\tower.exe`)
	a.runner = &fakeRunner{err: &os.PathError{Op: "fork/exec", Path: a.executable, Err: fs.ErrNotExist}}
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "executable_not_found" {
		t.Fatalf("unexpected receipt: %#v", got.Receipt)
	}
}

func TestCollectUnconfiguredDoesNotRun(t *testing.T) {
	f := &fakeRunner{}
	a := New("")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "not_configured" || len(f.args) != 0 {
		t.Fatalf("unexpected result: %#v args=%#v", got, f.args)
	}
}

func TestCollectMalformedAndAmbiguousOutput(t *testing.T) {
	for name, stdout := range map[string][]byte{
		"malformed": []byte(`{"bad":`),
		"ambiguous": []byte(`[{"worktree":{"repo":"repo"}}]`),
	} {
		t.Run(name, func(t *testing.T) {
			a := New("tower")
			a.runner = &fakeRunner{stdout: stdout}
			got := a.Collect(context.Background())
			if got.Receipt.State != model.SourceUnavailable || got.Receipt.ErrorCode == "" {
				t.Fatalf("unexpected result: %#v", got)
			}
		})
	}
}

func TestCollectTimeout(t *testing.T) {
	a := New("tower")
	a.timeout = time.Millisecond
	a.runner = runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	got := a.Collect(context.Background())
	if got.Receipt.ErrorCode != "timeout" {
		t.Fatalf("unexpected receipt: %#v", got.Receipt)
	}
}

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return f(ctx, executable, args...)
}
