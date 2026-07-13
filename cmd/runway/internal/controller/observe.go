package controller

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Watch reads durable events.ndjson only — never backend stdout (FR11).
// after < 0 means start from the beginning; follow polls until terminal when
// true.
func Watch(stateRoot, runID string, after int64, follow bool, w io.Writer) error {
	run, err := state.Open(stateRoot, runID)
	if err != nil {
		return err
	}
	last := after
	for {
		n, term, err := writeEventsAfter(run.EventsPath(), last, w)
		if err != nil {
			return err
		}
		if n > last {
			last = n
		}
		if term || !follow {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeEventsAfter(path string, after int64, w io.Writer) (int64, bool, error) {
	events, err := journal.ReadHistory(path)
	if err != nil {
		if os.IsNotExist(err) {
			return after, false, nil
		}
		return after, false, err
	}
	st, err := execution.Reduce(events)
	if err != nil {
		return after, false, err
	}
	last := after
	for _, ev := range events {
		if ev.Seq <= after {
			continue
		}
		line, err := json.Marshal(ev)
		if err != nil {
			return last, false, err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return last, false, err
		}
		last = ev.Seq
	}
	return last, st.Terminal, nil
}

// TailLogs copies buffered workload log bytes. follow polls until result.json
// exists. Delivery may lose the unflushed tail on abrupt controller loss.
func TailLogs(stateRoot, runID, stream string, follow bool, w io.Writer) error {
	run, err := state.Open(stateRoot, runID)
	if err != nil {
		return err
	}
	path, err := logPath(run, stream)
	if err != nil {
		return err
	}
	var offset int64
	for {
		n, err := copyFrom(path, offset, w)
		if err != nil {
			return err
		}
		offset += n
		if !follow {
			return nil
		}
		if _, ok, err := readResultIfPresent(run); err != nil {
			return err
		} else if ok {
			_, err := copyFrom(path, offset, w)
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func logPath(run state.RunDir, stream string) (string, error) {
	switch stream {
	case "", "stdout":
		return run.StdoutLog(), nil
	case "stderr":
		return run.StderrLog(), nil
	}
	return "", fmt.Errorf("controller: unknown log stream %q", stream)
}

func copyFrom(path string, offset int64, w io.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.Copy(w, f)
	return n, err
}

// ReadResult returns the terminal receipt. With wait, timeout is mandatory and
// the call watches durable state only — never reconciles (TDD §6).
func ReadResult(stateRoot, runID string, wait bool, timeout time.Duration) (execution.Result, error) {
	if wait && timeout <= 0 {
		return execution.Result{}, fmt.Errorf("controller: --timeout is mandatory with --wait")
	}
	run, err := state.Open(stateRoot, runID)
	if err != nil {
		return execution.Result{}, err
	}
	if !wait {
		res, ok, err := readResultIfPresent(run)
		if err != nil {
			return execution.Result{}, err
		}
		if !ok {
			return execution.Result{}, fmt.Errorf("controller: result.json not present")
		}
		return res, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		res, ok, err := readResultIfPresent(run)
		if err != nil {
			return execution.Result{}, err
		}
		if ok {
			return res, nil
		}
		if !time.Now().Before(deadline) {
			return execution.Result{}, fmt.Errorf("controller: timed out waiting for result")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// WriteJSON encodes v as one JSON object on w.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// ScanNDJSON is a small helper for tests — reads NDJSON objects from r.
func ScanNDJSON(r io.Reader, dst *[]json.RawMessage) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		*dst = append(*dst, append(json.RawMessage(nil), line...))
	}
	return sc.Err()
}
