// Command eval measures how well the local model does a task, by running a
// labeled dataset through the primitive and scoring each answer against a known
// correct one. This is the local-exportability oracle: a high score means the
// task is safe to route local; a low score means keep it on cloud. It turns the
// hand-tallying we did by eye into a repeatable `eval` command for any task.
//
//	eval -prompt "<task>" -schema '<json-schema|@file>' -dataset <jsonl> -field <name>
//
// The dataset is JSONL, one {"input": "...", "expected": "..."} per line. The
// harness compares the named field of the model's output against "expected";
// "expected" may list acceptable answers separated by "|" (a bot may state a
// severity as "High" or "High Severity" — both are its own label).
//
// -verbatim <field> additionally checks that the named output field appears
// verbatim in the input (normalized: lowercased, punctuation stripped). This is
// the extract-shaped verifier measured as a rate: an extraction that is not a
// quote of its source is a confabulation even when the scored field is right.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/itsHabib/workbench/local"
)

type row struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Meta     string `json:"meta"`
}

func main() {
	prompt := flag.String("prompt", "", "the task instruction")
	schema := flag.String("schema", "", "output JSON Schema (inline or @file)")
	dataset := flag.String("dataset", "", "path to JSONL of {input, expected}")
	field := flag.String("field", "", "field of the model output to score against expected")
	verbatim := flag.String("verbatim", "", "field that must appear verbatim in the input (normalized substring)")
	jsonl := flag.Bool("jsonl", false, "emit one JSON line per row {meta,expected,output} for downstream scoring, instead of the table")
	model := flag.String("model", "", "model override (blank = local default; e.g. qwen2.5:14b to re-gate a task at a higher tier)")
	flag.Parse()

	if *prompt == "" || *schema == "" || *dataset == "" || *field == "" {
		fmt.Fprintln(os.Stderr, "usage: eval -prompt <task> -schema <@file|inline> -dataset <jsonl> -field <name>")
		os.Exit(2)
	}
	if *model != "" {
		fmt.Fprintf(os.Stderr, "eval model: %s\n", *model)
	}

	schemaBytes, err := readMaybeFile(*schema)
	if err != nil {
		fmt.Fprintln(os.Stderr, "schema:", err)
		os.Exit(1)
	}
	promptBytes, err := readMaybeFile(*prompt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prompt:", err)
		os.Exit(1)
	}
	rows, err := loadRows(*dataset)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dataset:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pass, quoted := 0, 0
	for i, r := range rows {
		raw, err := local.Local(ctx, local.Req{Prompt: string(promptBytes), Input: r.Input, Schema: json.RawMessage(schemaBytes), Model: *model})
		if err != nil {
			if *jsonl {
				line, _ := json.Marshal(map[string]any{"meta": r.Meta, "expected": r.Expected, "error": err.Error()})
				fmt.Println(string(line))
				continue
			}
			fmt.Printf("%2d  ERR  %v\n", i+1, err)
			continue
		}
		if *jsonl {
			line, _ := json.Marshal(map[string]any{"meta": r.Meta, "expected": r.Expected, "output": json.RawMessage(raw)})
			fmt.Println(string(line))
			continue
		}
		vmark := verbatimMark(raw, r.Input, *verbatim, &quoted)
		got := fieldOf(raw, *field)
		if matches(got, r.Expected) {
			pass++
			fmt.Printf("%2d  ✓%s  %-12s | %s\n", i+1, vmark, got, trunc(r.Input, 58))
			continue
		}
		fmt.Printf("%2d  ✗%s  got %-10s want %-10s | %s\n", i+1, vmark, got, r.Expected, trunc(r.Input, 48))
	}

	if *jsonl {
		return
	}
	rate := 0.0
	if len(rows) > 0 {
		rate = float64(pass) / float64(len(rows))
	}
	fmt.Printf("\nscore: %d/%d (%.0f%%)\n", pass, len(rows), rate*100)
	if *verbatim != "" {
		fmt.Printf("verbatim %s: %d/%d (%.0f%%)\n", *verbatim, quoted, len(rows), float64(quoted)/float64(len(rows))*100)
	}
	if rate >= 0.8 {
		fmt.Println("verdict: GO for local")
		return
	}
	fmt.Println("verdict: NO-GO for local — keep on cloud (or raise the model tier)")
}

func loadRows(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r row
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// matches reports whether got equals any of the "|"-separated acceptable answers.
func matches(got, expected string) bool {
	for _, want := range strings.Split(expected, "|") {
		if strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

// verbatimMark checks the named output field appears in the input after
// normalization, bumps the counter, and returns a one-rune result marker.
func verbatimMark(raw json.RawMessage, input, field string, quoted *int) string {
	if field == "" {
		return ""
	}
	if normalize(input) == "" || !strings.Contains(normalize(input), normalize(fieldOf(raw, field))) {
		return "✗"
	}
	*quoted++
	return "✓"
}

// normalize lowercases and reduces text to space-separated alphanumeric runs,
// so markdown decoration doesn't break a substring comparison.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func fieldOf(raw json.RawMessage, field string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	return fmt.Sprint(v)
}

func readMaybeFile(s string) ([]byte, error) {
	if len(s) > 0 && s[0] == '@' {
		return os.ReadFile(s[1:])
	}
	return []byte(s), nil
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
