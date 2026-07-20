package rooms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type lifecycleRecord struct {
	Seq      int64    `json:"seq"`
	Time     string   `json:"ts"`
	RoomID   string   `json:"room_id"`
	Event    string   `json:"event"`
	Slot     int      `json:"slot,omitempty"`
	Tap      string   `json:"tap,omitempty"`
	PID      *int     `json:"pid,omitempty"`
	Cap      int      `json:"cap,omitempty"`
	ExitCode int      `json:"exit_code,omitempty"`
	Status   string   `json:"status,omitempty"`
	Command  []string `json:"command,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type lifecycleItem struct {
	record lifecycleRecord
	err    error
}

func tailLifecycle(path string, processDone <-chan struct{}, poll time.Duration) <-chan lifecycleItem {
	out := make(chan lifecycleItem, 32)
	go func() {
		defer close(out)
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		var file *os.File
		defer func() {
			if file != nil {
				_ = file.Close()
			}
		}()
		pending := []byte{}
		buffer := make([]byte, 32*1024)
		wantSeq := int64(1)
		roomID := ""
		for {
			var err error
			file, err = openLifecycle(path, file)
			if err != nil {
				out <- lifecycleItem{err: err}
				return
			}
			if file != nil {
				pending, wantSeq, roomID, err = readLifecycle(file, buffer, pending, wantSeq, roomID, out)
				if err != nil {
					out <- lifecycleItem{err: err}
					return
				}
			}
			select {
			case <-processDone:
				if file == nil {
					out <- lifecycleItem{err: fmt.Errorf("rooms: lifecycle missing after process exit")}
					return
				}
				pending, _, _, err = readLifecycle(file, buffer, pending, wantSeq, roomID, out)
				if err != nil {
					out <- lifecycleItem{err: err}
					return
				}
				if len(bytes.TrimSpace(pending)) > 0 {
					out <- lifecycleItem{err: fmt.Errorf("rooms: lifecycle ended with an incomplete JSON line")}
				}
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}

func openLifecycle(path string, file *os.File) (*os.File, error) {
	if file != nil {
		return file, nil
	}
	opened, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: open lifecycle: %w", err)
	}
	return opened, nil
}

func readLifecycle(file *os.File, buffer, pending []byte, wantSeq int64, roomID string, out chan<- lifecycleItem) ([]byte, int64, string, error) {
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return pending, wantSeq, roomID, fmt.Errorf("rooms: lifecycle position: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return pending, wantSeq, roomID, fmt.Errorf("rooms: stat lifecycle: %w", err)
	}
	if info.Size() < position {
		return pending, wantSeq, roomID, fmt.Errorf("rooms: lifecycle truncated while running")
	}
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			pending = append(pending, buffer[:n]...)
			pending, wantSeq, roomID, err = emitCompleteLines(pending, wantSeq, roomID, out)
			if err != nil {
				return pending, wantSeq, roomID, err
			}
		}
		if readErr == io.EOF {
			return pending, wantSeq, roomID, nil
		}
		if readErr != nil {
			return pending, wantSeq, roomID, fmt.Errorf("rooms: read lifecycle: %w", readErr)
		}
		if n == 0 {
			return pending, wantSeq, roomID, nil
		}
	}
}

func emitCompleteLines(data []byte, wantSeq int64, roomID string, out chan<- lifecycleItem) ([]byte, int64, string, error) {
	rest := data
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			return append([]byte(nil), rest...), wantSeq, roomID, nil
		}
		line := bytes.TrimSpace(rest[:i])
		rest = rest[i+1:]
		if len(line) == 0 {
			continue
		}
		var record lifecycleRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return rest, wantSeq, roomID, fmt.Errorf("rooms: decode lifecycle seq %d: %w", wantSeq, err)
		}
		if record.Seq != wantSeq {
			return rest, wantSeq, roomID, fmt.Errorf("rooms: lifecycle seq %d, want %d", record.Seq, wantSeq)
		}
		if record.RoomID == "" {
			return rest, wantSeq, roomID, fmt.Errorf("rooms: lifecycle record missing room_id")
		}
		if roomID == "" {
			roomID = record.RoomID
		}
		if record.RoomID != roomID {
			return rest, wantSeq, roomID, fmt.Errorf("rooms: lifecycle room_id changed from %q to %q", roomID, record.RoomID)
		}
		out <- lifecycleItem{record: record}
		wantSeq++
	}
}
