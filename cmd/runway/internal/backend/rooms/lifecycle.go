package rooms

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		consumed := 0
		wantSeq := int64(1)
		roomID := ""
		for {
			data, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				out <- lifecycleItem{err: fmt.Errorf("rooms: read lifecycle: %w", err)}
				return
			}
			if err == nil {
				var parseErr error
				consumed, wantSeq, roomID, parseErr = emitCompleteLines(data, consumed, wantSeq, roomID, out)
				if parseErr != nil {
					out <- lifecycleItem{err: parseErr}
					return
				}
			}
			select {
			case <-processDone:
				data, finalErr := os.ReadFile(path)
				if finalErr != nil {
					out <- lifecycleItem{err: fmt.Errorf("rooms: lifecycle missing after process exit: %w", finalErr)}
					return
				}
				finalConsumed, _, _, finalErr := emitCompleteLines(data, consumed, wantSeq, roomID, out)
				if finalErr != nil {
					out <- lifecycleItem{err: finalErr}
					return
				}
				if len(bytes.TrimSpace(data[finalConsumed:])) > 0 {
					out <- lifecycleItem{err: fmt.Errorf("rooms: lifecycle ended with an incomplete JSON line")}
				}
				return
			case <-time.After(poll):
			}
		}
	}()
	return out
}

func emitCompleteLines(data []byte, consumed int, wantSeq int64, roomID string, out chan<- lifecycleItem) (int, int64, string, error) {
	if consumed > len(data) {
		return consumed, wantSeq, roomID, fmt.Errorf("rooms: lifecycle truncated while running")
	}
	rest := data[consumed:]
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			return consumed, wantSeq, roomID, nil
		}
		line := bytes.TrimSpace(rest[:i])
		consumed += i + 1
		rest = data[consumed:]
		if len(line) == 0 {
			continue
		}
		var record lifecycleRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return consumed, wantSeq, roomID, fmt.Errorf("rooms: decode lifecycle seq %d: %w", wantSeq, err)
		}
		if record.Seq != wantSeq {
			return consumed, wantSeq, roomID, fmt.Errorf("rooms: lifecycle seq %d, want %d", record.Seq, wantSeq)
		}
		if roomID == "" {
			roomID = record.RoomID
		}
		if record.RoomID == "" || record.RoomID != roomID {
			return consumed, wantSeq, roomID, fmt.Errorf("rooms: lifecycle room_id changed from %q to %q", roomID, record.RoomID)
		}
		out <- lifecycleItem{record: record}
		wantSeq++
	}
}
