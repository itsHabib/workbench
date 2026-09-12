package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

const traceWindow = 1 << 20

var errNoTrace = errors.New("no observed transcript or output")

// tailSource uses observed paths, never a guessed transcript directory or session.
func tailSource(address string) (string, error) {
	tenant, err := fleet.MailRoleTenant(address)
	if err != nil {
		return "", err
	}
	directory, err := tailDirectory(address, tenant)
	if err != nil {
		return "", err
	}
	row := runtimeRow(deliverTarget{address: address, cwd: directory}, sessionRecords())
	if path := fleet.S(row, "transcript_path"); readableTrace(path) {
		return path, nil
	}
	if path := fleet.S(row, "output"); readableTrace(path) {
		return path, nil
	}
	return "", fmt.Errorf("%w for %s; inspect fleet watch status", errNoTrace, address)
}

func readableTrace(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func awaitTrace(ctx context.Context, address string, follow bool) (string, error) {
	for {
		path, err := tailSource(address)
		if !follow || !errors.Is(err, errNoTrace) {
			return path, err
		}
		if !tailWait(ctx) {
			return "", nil
		}
	}
}

func tailWait(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Tail reads visible events without a scheduler tick. Follow polls only this
// observed file, in Go, and re-resolves it when a replacement session starts.
func Tail(ctx context.Context, out io.Writer, address string, n int, follow bool) error {
	var previous string
	var offset int64
	var prior os.FileInfo
	for {
		path, err := awaitTrace(ctx, address, follow)
		if err != nil || path == "" {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		reset := path != previous || prior == nil || !os.SameFile(info, prior) || info.Size() < offset
		start := offset
		if reset {
			start = max(0, info.Size()-traceWindow)
		}
		_, err = f.Seek(start, io.SeekStart)
		if err != nil {
			_ = f.Close()
			return err
		}
		b, err := io.ReadAll(io.LimitReader(f, traceWindow))
		_ = f.Close()
		if err != nil {
			return err
		}
		consumed, lines := traceLines(b, reset && start > 0)
		if consumed == 0 && len(b) == traceWindow {
			return fmt.Errorf("trace record exceeds %d bytes; inspect %s", traceWindow, path)
		}
		if reset {
			fmt.Fprintf(out, "[%s · %s]\n", address, path)
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
		}
		for _, line := range lines {
			fmt.Fprintln(out, line)
		}
		previous, prior, offset = path, info, start+int64(consumed)
		if !follow {
			return nil
		}
		if !tailWait(ctx) {
			return nil
		}
	}
}

// Keep an incomplete JSONL record for the next read instead of printing torn text.
func traceLines(b []byte, skipFirst bool) (int, []string) {
	raw := strings.Split(string(b), "\n")
	var lines []string
	consumed := 0
	for i, line := range raw {
		record := fleet.ReadJSONBytes([]byte(line))
		complete := i < len(raw)-1 || record != nil
		if !complete {
			break
		}
		consumed += len(line)
		if i < len(raw)-1 {
			consumed++
		}
		if skipFirst && i == 0 {
			continue
		}
		if text := fleet.TranscriptLine(record); text != "" {
			lines = append(lines, text)
			continue
		}
		if record == nil && strings.TrimSpace(line) != "" {
			lines = append(lines, "output: "+line)
		}
	}
	return consumed, lines
}

func lastResult(path string) fleet.Rec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	start := max(0, st.Size()-traceWindow)
	partial := false
	if start > 0 {
		previous := make([]byte, 1)
		if _, err := f.ReadAt(previous, start-1); err != nil {
			return nil
		}
		partial = previous[0] != '\n'
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, traceWindow))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	if partial && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		r := fleet.ReadJSONBytes([]byte(lines[i]))
		if fleet.S(r, "type") == "result" {
			return r
		}
	}
	return nil
}

func tailDirectory(address, tenant string) (string, error) {
	_, mappings := fleet.MapRows(fleet.RolesMap())
	paths := map[string]bool{}
	for _, m := range mappings {
		match := m.Slot == address || (m.Slot == "" && m.Role == address)
		if m.Tenant == tenant && match {
			paths[fleet.CanonPath(m.Path)] = true
		}
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("%s has %d directory bindings; use a distinct seat address", address, len(paths))
	}
	for path := range paths {
		return path, nil
	}
	return "", fmt.Errorf("no directory binding for %s", address)
}
