package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/contracts/automode"
)

const maxAuditEventBytes = 1024 * 1024

// FileAudit is a process-safe, append-only JSONL audit.
type FileAudit struct {
	path string
}

// NewFileAudit returns an audit rooted at path.
func NewFileAudit(path string) FileAudit {
	return FileAudit{path: path}
}

// Append validates, appends, and syncs one complete event before returning.
func (a FileAudit) Append(event automode.AuditEvent) error {
	if err := automode.ValidateAuditEvent(event); err != nil {
		return fmt.Errorf("hook audit: invalid event: %w", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("hook audit: encode event: %w", err)
	}
	if len(data) > maxAuditEventBytes {
		return errors.New("hook audit: event exceeds 1 MiB")
	}
	return a.withFile(func(file *os.File) error {
		data = append(data, '\n')
		return appendLine(file, data)
	})
}

// Decision finds the newest persisted decision for invocationID.
func (a FileAudit) Decision(invocationID string) (automode.Decision, bool, error) {
	var found automode.Decision
	ok := false
	err := a.withFile(func(file *os.File) error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("hook audit: seek start: %w", err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), maxAuditEventBytes)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			event, err := automode.DecodeAuditEvent(line)
			if err != nil {
				return fmt.Errorf("hook audit: decode event: %w", err)
			}
			if event.InvocationID != invocationID || event.Kind != automode.EventDecision {
				continue
			}
			found = event.Decision
			ok = true
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("hook audit: read: %w", err)
		}
		return nil
	})
	return found, ok, err
}

func (a FileAudit) withFile(action func(*os.File) error) error {
	if a.path == "" {
		return errors.New("hook audit: path is required")
	}
	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("hook audit: create directory: %w", err)
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("hook audit: open: %w", err)
	}
	defer file.Close()
	if err := lockFile(file); err != nil {
		return fmt.Errorf("hook audit: lock: %w", err)
	}
	return action(file)
}

type appendFile interface {
	Seek(int64, int) (int64, error)
	Write([]byte) (int, error)
	Truncate(int64) error
	Sync() error
}

func appendLine(file appendFile, data []byte) error {
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("hook audit: seek end: %w", err)
	}
	written := 0
	for written < len(data) {
		n, writeErr := file.Write(data[written:])
		written += n
		if writeErr != nil {
			return rollbackAppend(file, start, fmt.Errorf("hook audit: append: %w", writeErr))
		}
		if n == 0 {
			return rollbackAppend(file, start, errors.New("hook audit: append: short write"))
		}
	}
	if err := file.Sync(); err != nil {
		return rollbackAppend(file, start, fmt.Errorf("hook audit: sync: %w", err))
	}
	return nil
}

func rollbackAppend(file appendFile, start int64, cause error) error {
	if err := file.Truncate(start); err != nil {
		return errors.Join(cause, fmt.Errorf("hook audit: rollback truncate: %w", err))
	}
	if err := file.Sync(); err != nil {
		return errors.Join(cause, fmt.Errorf("hook audit: rollback sync: %w", err))
	}
	return cause
}

// DefaultAuditPath resolves process-owned configuration without consulting
// hook input.
func DefaultAuditPath() (string, error) {
	if path := os.Getenv("CODEXGUARD_AUDIT"); path != "" {
		return path, nil
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("hook audit: resolve home: %w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}
	return filepath.Join(home, "state", "codexguard", "audit.jsonl"), nil
}
