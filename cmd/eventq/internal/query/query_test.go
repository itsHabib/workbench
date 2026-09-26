package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t testing.TB, body string) *Table {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	table, err := Build([]string{p}, []Field{{Name: "n", Path: "nested.n", Type: "int"}, {Name: "kind", Path: "kind", Type: "string"}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestQuerySemantics(t *testing.T) {
	table := fixture(t, "{\"nested\":{\"n\":9007199254740993},\"kind\":\"a\"}\n{\"nested\":{\"n\":-2},\"kind\":\"b\"}\n{\"kind\":\"\"}\n{}\n{\"nested\":{\"n\":null},\"kind\":null}\n")
	cases := map[string]int64{
		"": 5, "n == 9007199254740993": 1, "n != 0": 2, "n < 0": 1, "n <= -2": 1,
		"n >= 0 AND kind == \"a\"": 1, "n > 0 && kind != \"b\"": 1,
		"n IS NULL": 3, "n is not null": 2, "kind != \"unknown\"": 3,
		"kind == \"unknown\"": 0, "kind == \"\"": 1,
	}
	for expression, want := range cases {
		t.Run(expression, func(t *testing.T) {
			p, err := Compile(table, expression)
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.Execute(Options{CountOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if r.Matches != want {
				t.Fatalf("got %d want %d", r.Matches, want)
			}
		})
	}
	for _, expression := range []string{"n > 0 AND ", "n > 0 &&\n", "n ==", "unknown == 1", "n == \"1\"", "kind == 2", "n == 9223372036854775808", "n > 0 OR n < 0", "n > 0 garbage", "kind == \"unterminated", "kind > \"a\"", "n == 1.5", "n = 2"} {
		if _, err := Compile(table, expression); err == nil {
			t.Errorf("accepted %q", expression)
		}
	}
}

func TestGroupingAndLimits(t *testing.T) {
	table := fixture(t, "{\"nested\":{\"n\":5},\"kind\":\"a\"}\n{\"nested\":{\"n\":7},\"kind\":\"a\"}\n{\"kind\":\"b\"}\n{}\n{\"kind\":\"null\"}\n")
	p, _ := Compile(table, "")
	r, err := p.Execute(Options{Group: "kind", Sum: "n", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if r.Matches != 5 || *r.Sum != 12 || r.SumPresent != 2 || r.TotalGroups != 4 || !r.Truncated || r.Groups[0].Count != 2 {
		t.Fatalf("bad result: %+v", r)
	}
	if *r.Groups[0].Sum != 12 || r.Groups[0].SumPresent != 2 {
		t.Fatal(r.Groups)
	}
	r, err = p.Execute(Options{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0]["line"] != int64(1) || !r.Truncated {
		t.Fatal(r)
	}
	if _, err = p.Execute(Options{Sum: "kind"}); err == nil {
		t.Fatal("summed strings")
	}
	if _, err = p.Execute(Options{Limit: -1}); err == nil {
		t.Fatal("accepted negative limit")
	}
}

func TestSumOverflow(t *testing.T) {
	for _, nums := range [][]int64{{math.MaxInt64, 1}, {math.MinInt64, -1}} {
		table := fixture(t, fmt.Sprintf("{\"nested\":{\"n\":%d}}\n{\"nested\":{\"n\":%d}}", nums[0], nums[1]))
		p, _ := Compile(table, "")
		if _, err := p.Execute(Options{Sum: "n"}); err == nil {
			t.Fatal("overflow accepted")
		}
	}
}

func TestStoreRoundtripAndCorruption(t *testing.T) {
	table := fixture(t, "{\"nested\":{\"n\":-9223372036854775808},\"kind\":\"a\"}\n{}")
	path := filepath.Join(t.TempDir(), "index.eq")
	if err := Save(path, table); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, table); err == nil {
		t.Fatal("overwrote existing index")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < table.Rows; i++ {
		if !reflect.DeepEqual(table.row(i), loaded.row(i)) {
			t.Fatal("roundtrip")
		}
	}
	data, _ := os.ReadFile(path)
	for _, n := range []int{0, 1, 8, 12, 43, len(data) - 1} {
		if _, err := unmarshal(data[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	data[len(data)/2] ^= 1
	if _, err := unmarshal(data); err == nil {
		t.Fatal("accepted corruption")
	}
	data, _ = marshal(table)
	headerLen := int(binary.LittleEndian.Uint32(data[8:12]))
	// Forge a valid checksum around an invalid presence mask; structural checks still reject it.
	binary.LittleEndian.PutUint64(data[12+headerLen+table.Rows*8:], 42)
	sum := sha256.Sum256(data[:len(data)-32])
	copy(data[len(data)-32:], sum[:])
	if _, err := unmarshal(data); err == nil {
		t.Fatal("accepted invalid mask")
	}
}

func TestKernelDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 42))
	for n := 0; n < 131; n++ {
		values, valid := make([]int64, n), make([]int64, n)
		for i := range values {
			values[i] = int64(rng.Uint64())
			if i%3 != 0 {
				valid[i] = -1
			}
		}
		for _, op := range []string{"==", "!=", ">", ">=", "<", "<=", "null", "present"} {
			for _, bound := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
				checkKernel(t, values, valid, op, bound)
			}
		}
	}
}

func checkKernel(t *testing.T, values, valid []int64, op string, bound int64) {
	t.Helper()
	a, b := make([]int64, len(values)), make([]int64, len(values))
	for i := range a {
		if i%5 != 0 {
			a[i] = -1
			b[i] = -1
		}
	}
	scalarFilter(a, values, valid, op, bound)
	filter(b, values, valid, op, bound)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("backend mismatch len=%d op=%s bound=%d", len(a), op, bound)
	}
}

func TestCodexProjectionAndTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	record := `{"type":"event_msg","timestamp":"2026-09-26T01:02:03Z","payload":{"type":"item_completed","thread_id":"session-1","item":{"type":"CommandExecution","id":"item-1","cwd":"file:///example","status":"Completed","exit_code":1,"duration":{"secs":30,"nanos":123456789},"command":"PRIVATE_SENTINEL","aggregated_output":"PRIVATE_OUTPUT"}}}`
	if err := os.WriteFile(path, []byte(record+"\n{\"type\":"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build([]string{path}, nil, true, false); err == nil {
		t.Fatal("accepted partial tail by default")
	}
	table, err := Build([]string{path}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if table.Rows != 1 || table.IgnoredTails != 1 {
		t.Fatal(table.Rows, table.IgnoredTails)
	}
	row := table.row(0)
	if row["duration_ms"] != int64(30123) || row["day"] != "2026-09-26" || row["exit_code"] != int64(1) {
		t.Fatal(row)
	}
	data, _ := marshal(table)
	if bytes.Contains(data, []byte("PRIVATE")) {
		t.Fatal("private payload leaked into index")
	}
	if err := os.WriteFile(path, []byte(record+"\n{\"type\":\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build([]string{path}, nil, true, true); err == nil {
		t.Fatal("ignored malformed completed line")
	}
}

func TestGenericRejectsWrongTypes(t *testing.T) {
	for _, body := range []string{`{"nested":{"n":1.5}}`, `{"nested":{"n":"1"}}`, `{"nested":{"n":9223372036854775808}}`, `{"kind":true}`, `[]`, `null`} {
		p := filepath.Join(t.TempDir(), "bad.jsonl")
		_ = os.WriteFile(p, []byte(body), 0600)
		_, err := Build([]string{p}, []Field{{Name: "n", Path: "nested.n", Type: "int"}, {Name: "kind", Path: "kind", Type: "string"}}, false, false)
		if err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func FuzzCompile(f *testing.F) {
	for _, s := range []string{"n > 3", "n IS NULL", "kind == \"a\"", "n > 1 AND ", "n == -9223372036854775808"} {
		f.Add(s)
	}
	table, _ := New([]Field{{Name: "n", Type: "int"}, {Name: "kind", Type: "string"}})
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		_, _ = Compile(table, s)
	})
}

func FuzzIndex(f *testing.F) {
	table, _ := New([]Field{{Name: "n", Type: "int"}})
	data, _ := marshal(table)
	f.Add(data)
	f.Add([]byte("EVENTQ01"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		_, _ = unmarshal(b)
	})
}

func BenchmarkQuery(b *testing.B) {
	for _, size := range []int{50000, 1000000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			table, _ := New([]Field{{Name: "duration_ms", Type: "int"}, {Name: "exit_code", Type: "int"}})
			for i := 0; i < size; i++ {
				_ = table.appendRow([]any{int64(i % 100000), int64(i % 7)}, "fixture", int64(i+1))
			}
			p, _ := Compile(table, "duration_ms >= 30000 AND exit_code != 0")
			for _, scalar := range []bool{true, false} {
				b.Run(fmt.Sprintf("scalar=%v", scalar), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						r, err := p.Execute(Options{CountOnly: true, Scalar: scalar})
						if err != nil || r.Matches == 0 {
							b.Fatal(r, err)
						}
					}
				})
			}
		})
	}
}

func TestFieldValidation(t *testing.T) {
	for _, s := range []string{"no-type", "n=x:float", "n=:int", "source=x:int", "2name=x:int"} {
		if _, err := ParseField(s); err == nil {
			t.Fatal(s)
		}
	}
	for _, name := range []string{"source", "line", "", "two names", "2n"} {
		if _, err := New([]Field{{Name: name, Type: "int"}}); err == nil {
			t.Fatal(name)
		}
	}
	table := fixture(t, "{}")
	if _, err := Compile(table, strings.Repeat("n == 0 AND ", 65)+"n == 0"); err == nil {
		t.Fatal("unbounded predicates")
	}
}

func TestTopKeepsLargestAndStableTies(t *testing.T) {
	table := fixture(t, "{\"nested\":{\"n\":-5}}\n{}\n{\"nested\":{\"n\":8}}\n{\"nested\":{\"n\":8}}\n{\"nested\":{\"n\":-1}}\n")
	p, _ := Compile(table, "")
	r, err := p.Execute(Options{Top: "n", Limit: 2})
	if err != nil || r.Matches != 5 || len(r.Rows) != 2 || !r.Truncated {
		t.Fatal(r, err)
	}
	if r.Rows[0]["line"] != int64(3) || r.Rows[1]["line"] != int64(4) {
		t.Fatal(r.Rows)
	}
	r, err = p.Execute(Options{Top: "n", Limit: 10})
	if err != nil || r.Rows[4]["n"] != nil || r.Rows[3]["n"] != int64(-5) {
		t.Fatal(r, err)
	}
	r, err = p.Execute(Options{Top: "n", Limit: 0})
	if err != nil || len(r.Rows) != 0 || !r.Truncated {
		t.Fatal(r, err)
	}
	for _, o := range []Options{{Top: "kind"}, {Top: "n", CountOnly: true}, {Top: "n", Group: "kind"}} {
		if _, err := p.Execute(o); err == nil {
			t.Fatal(o)
		}
	}
}

func TestIncompleteVersusMalformedTail(t *testing.T) {
	for _, tail := range []string{`{"x": nope}`, `{"x":1,}`, `{"x": truX}`, `{"x": "unterminated`, `{"x":`} {
		path := filepath.Join(t.TempDir(), "tail.jsonl")
		_ = os.WriteFile(path, []byte("{\"x\":1}\n"+tail), 0600)
		table, err := Build([]string{path}, []Field{{Name: "x", Path: "x", Type: "int"}}, false, true)
		incomplete := !strings.HasSuffix(tail, "}")
		if incomplete && (err != nil || table.Rows != 1 || table.IgnoredTails != 1) {
			t.Fatalf("%s: %v", tail, err)
		}
		if !incomplete && err == nil {
			t.Fatalf("ignored malformed record %s", tail)
		}
	}
	// An invalid final byte is not an incomplete record, even when a
	// json.SyntaxError.Offset equals len(data).
	path := filepath.Join(t.TempDir(), "bad-last-byte.jsonl")
	_ = os.WriteFile(path, []byte("{\"x\":1}\n{\"x\":truX"), 0600)
	if _, err := Build([]string{path}, []Field{{Name: "x", Path: "x", Type: "int"}}, false, true); err == nil {
		t.Fatal("ignored invalid last byte")
	}
}

func TestCodexInvalidDurations(t *testing.T) {
	for _, d := range []string{`{}`, `{"secs":1}`, `{"secs":null,"nanos":0}`, `{"secs":-1,"nanos":0}`, `{"secs":0,"nanos":1000000000}`, `{"secs":9223372036854776,"nanos":0}`} {
		raw := []byte(`{"type":"event_msg","timestamp":"2026-09-26T01:00:00Z","payload":{"type":"item_completed","item":{"type":"CommandExecution","duration":` + d + `}}}`)
		if _, err := codexRow(raw); err == nil {
			t.Fatalf("accepted duration %s", d)
		}
	}
}

func TestBuildBudgetsDuringIngestion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_, err = fmt.Fprintf(f, "{\"x\":\"%d%s\"}\n", i, strings.Repeat("a", 1<<20))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Build([]string{path}, []Field{{Name: "x", Path: "x", Type: "string"}}, false, false); err == nil || !strings.Contains(err.Error(), "dictionary strings") {
		t.Fatal(err)
	}
}
