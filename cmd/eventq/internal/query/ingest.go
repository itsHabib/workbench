package query

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxLine = 32 << 20

// CodexFields deliberately omit commands, arguments, prompts, and output bodies.
func CodexFields() []Field {
	return []Field{
		{Name: "timestamp", Type: "string"}, {Name: "day", Type: "string"}, {Name: "session", Type: "string"},
		{Name: "item_id", Type: "string"}, {Name: "cwd", Type: "string"},
		{Name: "status", Type: "string"}, {Name: "duration_ms", Type: "int"},
		{Name: "exit_code", Type: "int"},
	}
}

// Files expands directories recursively, selects .jsonl files, and deduplicates paths.
func Files(paths []string) ([]string, error) {
	seen := map[string]bool{}
	for _, path := range paths {
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink input is not supported: %s", p)
			}
			if filepath.Ext(p) != ".jsonl" {
				return nil
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return err
			}
			seen[abs] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	files := make([]string, 0, len(seen))
	for p := range seen {
		files = append(files, p)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no .jsonl inputs found")
	}
	return files, nil
}

// Build reads a bounded snapshot of each input. Only an incomplete final JSON
// record may be ignored, explicitly; malformed complete records always fail.
func Build(paths []string, fields []Field, codex, allowTail bool) (*Table, error) {
	if codex {
		fields = CodexFields()
	}
	t, err := New(fields)
	if err != nil {
		return nil, err
	}
	files, err := Files(paths)
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		if err := t.ingestFile(p, codex, allowTail); err != nil {
			return nil, err
		}
	}
	t.BuiltAt = time.Now().UTC().Format(time.RFC3339)
	return t, nil
}

func (t *Table) ingestFile(path string, codex, allowTail bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	h := sha256.New()
	r := bufio.NewReader(io.TeeReader(io.NewSectionReader(f, 0, st.Size()), h))
	for line := int64(1); ; line++ {
		data, readErr := readLine(r)
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("%s:%d: %w", path, line, readErr)
		}
		if len(bytes.TrimSpace(data)) != 0 {
			if err := t.ingestLine(data, path, line, codex, allowTail && readErr == io.EOF); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	end, err := f.Stat()
	if err != nil {
		return err
	}
	if end.Size() < st.Size() {
		return fmt.Errorf("input truncated while reading: %s", path)
	}
	t.Sources = append(t.Sources, Source{Path: path, Bytes: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil))})
	return nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var data []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(data)+len(part) > maxLine {
			return nil, fmt.Errorf("line exceeds %d bytes", maxLine)
		}
		data = append(data, part...)
		if err != bufio.ErrBufferFull {
			return data, err
		}
	}
}

func (t *Table) ingestLine(data []byte, source string, line int64, codex, tail bool) error {
	if !json.Valid(data) {
		var raw json.RawMessage
		parseErr := json.Unmarshal(data, &raw)
		if tail && parseErr != nil && parseErr.Error() == "unexpected end of JSON input" {
			t.IgnoredTails++
			return nil
		}
		return fmt.Errorf("%s:%d: invalid JSON", source, line)
	}
	var values []any
	var err error
	if codex {
		values, err = codexRow(data)
	}
	if !codex {
		values, err = genericRow(data, t.Columns[:len(t.Columns)-2])
	}
	if err != nil {
		return fmt.Errorf("%s:%d: %w", source, line, err)
	}
	if values == nil {
		return nil
	}
	if t.Rows >= 10_000_000 {
		return fmt.Errorf("index exceeds 10000000 rows; partition the input")
	}
	if err := t.appendRow(values, source, line); err != nil {
		return fmt.Errorf("%s:%d: %w", source, line, err)
	}
	return nil
}

func genericRow(data []byte, columns []Column) ([]any, error) {
	var obj map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	values := make([]any, len(columns))
	for i, c := range columns {
		values[i] = atPath(obj, c.Path)
	}
	return values, nil
}

func atPath(obj map[string]any, path string) any {
	var v any = obj
	for _, key := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func codexRow(data []byte) ([]any, error) {
	var ev struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type     string          `json:"type"`
			ThreadID string          `json:"thread_id"`
			Item     json.RawMessage `json:"item"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, err
	}
	if ev.Type != "event_msg" || ev.Payload.Type != "item_completed" {
		return nil, nil
	}
	var tag struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(ev.Payload.Item, &tag); err != nil {
		return nil, err
	}
	if tag.Type != "CommandExecution" {
		return nil, nil
	}
	var item struct {
		ID       string `json:"id"`
		Cwd      string `json:"cwd"`
		Status   string `json:"status"`
		ExitCode *int64 `json:"exit_code"`
		Duration *struct {
			Secs  *int64 `json:"secs"`
			Nanos *int64 `json:"nanos"`
		} `json:"duration"`
	}
	if err := json.Unmarshal(ev.Payload.Item, &item); err != nil {
		return nil, err
	}
	var duration, exit any
	if item.Duration != nil {
		d := item.Duration
		if d.Secs == nil || d.Nanos == nil {
			return nil, fmt.Errorf("command duration requires secs and nanos")
		}
		if *d.Secs < 0 || *d.Nanos < 0 || *d.Nanos >= 1e9 || *d.Secs > (math.MaxInt64-*d.Nanos/1e6)/1000 {
			return nil, fmt.Errorf("invalid command duration")
		}
		duration = *d.Secs*1000 + *d.Nanos/1e6
	}
	if item.ExitCode != nil {
		exit = *item.ExitCode
	}
	stamp, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid command timestamp: %w", err)
	}
	return []any{ev.Timestamp, stamp.UTC().Format("2006-01-02"), ev.Payload.ThreadID, item.ID, item.Cwd, item.Status, duration, exit}, nil
}
