package gatecli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// recorder is a Runner that captures the args it was called with and returns a
// canned result.
func recorder(out []byte, err error, got *[]string) Runner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		*got = args
		return out, err
	}
}

func TestStatePassedThroughWhenSet(t *testing.T) {
	var got []string
	c := New("gate", "/s/state", recorder([]byte("{}"), nil, &got))
	if _, err := c.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The verb leads; -state is a flag of the verb, never before it.
	want := []string{"next", "-state", "/s/state", "-json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestStateOmittedWhenEmpty(t *testing.T) {
	var got []string
	c := New("gate", "", recorder([]byte("{}"), nil, &got))
	if _, err := c.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"next", "-json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestExplainRejectsBadRunID(t *testing.T) {
	var got []string
	called := false
	c := New("gate", "", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		called = true
		got = args
		return nil, nil
	})
	for _, bad := range []string{"notarun", "run_", "run_XYZ", "run_abc; rm", "../etc"} {
		if _, err := c.Explain(context.Background(), bad); err == nil {
			t.Fatalf("Explain(%q) should reject", bad)
		}
	}
	if called {
		t.Fatalf("gate must not be spawned for a bad run id (last args %v)", got)
	}
}

func TestExplainValidRunID(t *testing.T) {
	var got []string
	c := New("gate", "", recorder([]byte("{}"), nil, &got))
	if _, err := c.Explain(context.Background(), "run_9f3a41c2"); err != nil {
		t.Fatal(err)
	}
	want := []string{"explain", "-run", "run_9f3a41c2", "-json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestAuditClean(t *testing.T) {
	c := New("gate", "", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("chain intact\n"), nil
	})
	st, err := c.Audit(context.Background())
	if err != nil || !st.OK || st.Reason != "chain intact" {
		t.Fatalf("clean audit = %+v err %v", st, err)
	}
}

func TestAuditTamperedMapsToFinding(t *testing.T) {
	// gate prints TAMPERED on stdout and exits non-zero; the client must map
	// that to a finding, not propagate the exit as an operational error.
	c := New("gate", "", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("TAMPERED: rewrite (at esc_f6789012)\n"), errors.New("exit status 4")
	})
	st, err := c.Audit(context.Background())
	if err != nil {
		t.Fatalf("a tamper finding must not surface as an error: %v", err)
	}
	if st.OK || !strings.Contains(st.Reason, "TAMPERED") {
		t.Fatalf("tampered audit = %+v", st)
	}
}

func TestAuditOtherErrorPropagates(t *testing.T) {
	// A non-zero exit that is NOT a tamper finding (e.g. state unreadable) is a
	// real error and must surface.
	c := New("gate", "", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(""), errors.New("state: open log: permission denied")
	})
	if _, err := c.Audit(context.Background()); err == nil {
		t.Fatal("a non-tamper audit failure must surface as an error")
	}
}
