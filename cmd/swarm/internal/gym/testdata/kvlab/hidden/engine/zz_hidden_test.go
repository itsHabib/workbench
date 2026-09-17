package engine

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"kvlab/wal"
)

func exec(t *testing.T, e *Engine, line, want string) {
	t.Helper()
	got, err := e.Exec(line)
	if err != nil || got != want {
		t.Fatalf("%q -> %q (%v), want %q", line, got, err, want)
	}
}

func TestHiddenReplies(t *testing.T) {
	e := New(time.Now, nil)
	exec(t, e, "KEYS", "")
	exec(t, e, "COUNT", "0")
	exec(t, e, "GET a", "(nil)")
	exec(t, e, "SET b 2", "OK")
	exec(t, e, `set a "one two"`, "OK")
	exec(t, e, "GET a", "one two")
	exec(t, e, "KEYS", "a\nb")
	exec(t, e, "COUNT", "2")
	exec(t, e, "DEL a", "1")
	exec(t, e, "DEL a", "0")
	if _, err := e.Exec("BOGUS"); err == nil {
		t.Fatal("bogus accepted")
	}
}

func TestHiddenExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	e := New(func() time.Time { return now }, nil)
	exec(t, e, "SET a 1 EX 10", "OK")
	now = now.Add(9 * time.Second)
	exec(t, e, "GET a", "1")
	now = now.Add(time.Second)
	exec(t, e, "GET a", "(nil)")
	exec(t, e, "DEL a", "0")
}

func TestHiddenLogShape(t *testing.T) {
	var log bytes.Buffer
	e := New(time.Now, &log)
	exec(t, e, "SET a 1 EX 3", "OK")
	exec(t, e, "GET a", "1")
	exec(t, e, "DEL nope", "0")
	_, _ = e.Exec("SET broken")
	exec(t, e, "DEL a", "1")
	lines := strings.Split(strings.TrimRight(log.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines: %q", len(lines), log.String())
	}
	first, err := wal.Decode(lines[0])
	if err != nil || first != (wal.Entry{Op: "set", Key: "a", Val: "1", TTLms: 3000}) {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := wal.Decode(lines[1])
	if err != nil || second.Op != "del" || second.Key != "a" {
		t.Fatalf("second %+v %v", second, err)
	}
}

func TestHiddenRestoreRoundTrip(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	var log bytes.Buffer
	a := New(clock, &log)
	exec(t, a, `SET greeting "hello, \"world\""`, "OK")
	exec(t, a, "SET tmp x EX 5", "OK")
	exec(t, a, "SET gone 1", "OK")
	exec(t, a, "DEL gone", "1")
	var log2 bytes.Buffer
	b := New(clock, &log2)
	if err := b.Restore(bytes.NewReader(log.Bytes())); err != nil {
		t.Fatal(err)
	}
	if log2.Len() != 0 {
		t.Fatal("restore wrote to the log")
	}
	exec(t, b, "GET greeting", `hello, "world"`)
	exec(t, b, "KEYS", "greeting\ntmp")
	now = now.Add(5 * time.Second)
	exec(t, b, "COUNT", "1")
	if err := b.Restore(strings.NewReader("junk\n")); err == nil {
		t.Fatal("junk restored")
	}
}

func TestHiddenValueSizes(t *testing.T) {
	e := New(time.Now, nil)
	if got := e.ValueSizes(); got.N != 0 {
		t.Fatalf("%+v", got)
	}
	exec(t, e, "SET a 1", "OK")
	exec(t, e, "SET b 333", "OK")
	exec(t, e, `SET c "55555"`, "OK")
	got := e.ValueSizes()
	if got.N != 3 || got.Min != 1 || got.Max != 5 || got.Mean != 3 || got.P50 != 3 || got.P95 != 5 {
		t.Fatalf("%+v", got)
	}
}
