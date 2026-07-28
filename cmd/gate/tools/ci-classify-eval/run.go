package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

type evalRow struct {
	Input    string          `json:"input"`
	Expected string          `json:"expected"`
	Meta     json.RawMessage `json:"meta"`
}

type evalInputs struct {
	prompt []byte
	schema []byte
	rows   []evalRow
}

func loadEvalInputs(evalDir string) (evalInputs, error) {
	prompt, err := os.ReadFile(filepath.Join(evalDir, "ci-classify.prompt.txt"))
	if err != nil {
		return evalInputs{}, fmt.Errorf("read prompt: %w", err)
	}
	schema, err := os.ReadFile(filepath.Join(evalDir, "ci-classify.schema.json"))
	if err != nil {
		return evalInputs{}, fmt.Errorf("read schema: %w", err)
	}

	f, err := os.Open(filepath.Join(evalDir, "ci-lines-v2.jsonl"))
	if err != nil {
		return evalInputs{}, fmt.Errorf("open fixtures: %w", err)
	}
	defer f.Close()

	var rows []evalRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var row evalRow
		if err := json.Unmarshal(line, &row); err != nil {
			return evalInputs{}, fmt.Errorf("decode fixture row: %w", err)
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return evalInputs{}, fmt.Errorf("read fixtures: %w", err)
	}
	if len(rows) == 0 {
		return evalInputs{}, fmt.Errorf("no fixture rows in %s", filepath.Join(evalDir, "ci-lines-v2.jsonl"))
	}

	return evalInputs{prompt: prompt, schema: schema, rows: rows}, nil
}

func runCloudEval(paths resolvedPaths) error {
	inputs, err := loadEvalInputs(paths.evalDir)
	if err != nil {
		return err
	}

	model, err := verify.ModelBackend("cloud")
	if err != nil {
		return err
	}

	out, err := os.Create(paths.outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, row := range inputs.rows {
		content, err := verify.ModelChat(context.Background(), model, string(inputs.prompt), row.Input, inputs.schema)
		if err != nil {
			return fmt.Errorf("row error: %w", err)
		}
		var output map[string]any
		if err := json.Unmarshal([]byte(content), &output); err != nil {
			return fmt.Errorf("bad model json: %w", err)
		}
		rec := map[string]any{
			"expected": row.Expected,
			"meta":     row.Meta,
			"output":   output,
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := out.Write(b); err != nil {
			return err
		}
		if _, err := out.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}
