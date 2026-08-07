package gatecli

import (
	"context"
	"errors"
	"reflect"
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
	want := []string{"next", "-state", "/s/state", "-json", "-live"}
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
	want := []string{"next", "-json", "-live"}
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
	// A broken chain exits non-zero with a tamper marker on stdout — the
	// stable log_integrity_failed code in current gate's terminal JSON, or the
	// TAMPERED line older binaries printed. Both map to a finding, never to an
	// operational error.
	for name, out := range map[string]string{
		"terminal-json": `{"error": "log_integrity_failed: body hash mismatch (at evd_x)", "retry_helps": false}` + "\n",
		"legacy-marker": "TAMPERED: rewrite (at esc_f6789012)\n",
	} {
		c := New("gate", "", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(out), errors.New("exit status 4")
		})
		st, err := c.Audit(context.Background())
		if err != nil {
			t.Fatalf("%s: a tamper finding must not surface as an error: %v", name, err)
		}
		if st.OK || st.Reason == "" {
			t.Fatalf("%s: tampered audit = %+v", name, st)
		}
	}
}

func TestAuditOperationalTokenIsNotTamper(t *testing.T) {
	// The code is parsed from the terminal JSON's error field, never searched
	// for in the raw text: an I/O failure on a path that happens to contain
	// the token must propagate as an error, not raise the tamper banner.
	for name, out := range map[string]string{
		"token-in-path": `{"error": "state: anchor dir: mkdir /tmp/log_integrity_failed: not a directory", "retry_helps": false}` + "\n",
		"token-in-text": "gate: could not stat /tmp/log_integrity_failed\n",
	} {
		c := New("gate", "", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(out), errors.New("exit status 4")
		})
		if _, err := c.Audit(context.Background()); err == nil {
			t.Fatalf("%s: an operational failure was mapped to a tamper finding", name)
		}
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
