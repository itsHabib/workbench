package watch

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Inspect resolves an address from the current board, never from a caller-supplied path.
// It reads authored context and observations without ticking or changing the store.
func Inspect(address string) (fleet.Rec, error) {
	before := fleet.ReadOnly
	fleet.ReadOnly = true
	defer func() { fleet.ReadOnly = before }()
	row, at, err := agentAt(address)
	if err != nil {
		return nil, err
	}
	out := fleet.Rec{"at": at, "agent": row, "handoff": nil, "role_handoff": "", "messages": []fleet.Rec{}}
	if fleet.S(row, "head_error") == "" {
		cwd, branch := fleet.S(row, "cwd"), fleet.S(row, "branch")
		if branch != "" {
			out["handoff"] = fleet.ReadJSON(fleet.KeyFile("handoff", fleet.Scope(cwd, branch)))
		}

	}
	if info, err := os.Stat(fleet.S(row, "cwd")); err == nil && info.IsDir() {
		out["role_handoff"] = fleet.RoleHandoffLine(fleet.Rec{"cwd": row["cwd"]})
	}
	inspectMail(out, row)
	trace, err := traceForRow(row)
	if err != nil {
		out["trace_error"] = err.Error()
		return out, nil
	}
	delete(trace, "data")
	out["trace"] = trace
	return out, nil
}

func inspectMail(out, row fleet.Rec) {
	tenant, err := fleet.MailRoleTenant(fleet.S(row, "address"))
	if err != nil {
		out["mail_error"] = err.Error()
		return
	}
	mail, partial, err := fleet.MailSnapshot(tenant, fleet.S(row, "address"))
	if err != nil {
		out["mail_error"] = err.Error()
		return
	}
	out["message_count"] = nil
	if !partial {
		out["message_count"] = len(mail)
	}
	out["mail_partial"] = partial
	messages := []fleet.Rec{}
	for _, m := range mail[:min(len(mail), 30)] {
		messages = append(messages, excerptMailItem(m))
	}
	out["messages"] = messages
}

func excerptMailItem(m fleet.Rec) fleet.Rec {
	item := fleet.Rec{}
	for _, key := range []string{"id", "at", "from_address", "kind", "subject", "body", "acked_at", "reply_to"} {
		value := m[key]
		if text, ok := value.(string); ok {
			value = excerptMailText(text)
		}
		item[key] = value
	}
	return item
}

func excerptMailText(text string) string {
	runes := []rune(text)
	if len(runes) <= 1000 {
		return text
	}
	return string(runes[:1000]) + " … [excerpt]"
}

// Trace exports the last bounded window of an observed trace as JSON data.
// A partial window is explicit: it cannot establish that the whole run is healthy.
func Trace(address string) (fleet.Rec, error) {
	before := fleet.ReadOnly
	fleet.ReadOnly = true
	defer func() { fleet.ReadOnly = before }()
	row, _, err := agentAt(address)
	if err != nil {
		return nil, err
	}
	return traceForRow(row)
}

func agentAt(address string) (fleet.Rec, any, error) {
	status := AllStatus()
	var row fleet.Rec
	for _, candidate := range status["workers"].([]fleet.Rec) {
		if fleet.S(candidate, "address") != address {
			continue
		}
		if row != nil {
			return nil, nil, fmt.Errorf("ambiguous address %q; inspect role bindings", address)
		}
		row = candidate
	}
	if row == nil {
		return nil, nil, fmt.Errorf("no current agent at address %q", address)
	}
	return row, status["at"], nil
}

func traceForRow(row fleet.Rec) (fleet.Rec, error) {
	address := fleet.S(row, "address")
	var path string
	for _, key := range []string{"trace", "transcript_path", "output"} {
		if candidate := fleet.S(row, key); readableTrace(candidate) {
			path = candidate
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no observed readable trace for %s", address)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("trace is not a regular file")
	}
	start := max(int64(0), info.Size()-traceWindow)
	data, err := io.ReadAll(io.NewSectionReader(f, start, traceWindow))
	if err != nil {
		return nil, err
	}
	partial := start > 0
	var previous [1]byte
	if start > 0 {
		if _, err := f.ReadAt(previous[:], start-1); err != nil {
			return nil, err
		}
	}
	if start > 0 && previous[0] != '\n' {
		cut := bytes.IndexByte(data, '\n')
		if cut < 0 {
			return nil, fmt.Errorf("no complete record in trace window")
		}
		data = data[cut+1:]
	}
	consumed, lines := traceLines(data, false)
	if consumed < len(data) {
		partial = true
		data = data[:consumed]
	}
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	coverage := "complete observed file"
	if partial {
		coverage = "partial window; earlier or incomplete records excluded"
	}
	return fleet.Rec{"address": address, "source": path, "at": fleet.Now(), "modified_at": float64(info.ModTime().UnixNano()) / 1e9, "file_bytes": info.Size(), "partial": partial, "coverage": coverage, "data": string(data), "fingerprint": fmt.Sprintf("%x", sha256.Sum256(data)), "lines": lines, "note": strings.TrimSpace("Observed activity is not task completion. Display shows at most 100 recent events.")}, nil
}
