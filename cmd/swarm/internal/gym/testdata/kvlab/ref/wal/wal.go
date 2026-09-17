package wal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"kvlab/store"
)

type Entry struct {
	Op, Key, Val string
	TTLms        int64
}

func Encode(e Entry) string {
	b, _ := json.Marshal(e)
	return string(b)
}

func Decode(line string) (Entry, error) {
	var e Entry
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Entry{}, err
	}
	if e.Op != "set" && e.Op != "del" {
		return Entry{}, fmt.Errorf("wal: bad op %q", e.Op)
	}
	return e, nil
}

func Replay(r io.Reader, s *store.Store) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	n, ln := 0, 0
	for sc.Scan() {
		ln++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		e, err := Decode(line)
		if err != nil {
			return n, fmt.Errorf("wal: line %d: %w", ln, err)
		}
		if e.Op == "set" {
			s.Set(e.Key, e.Val, time.Duration(e.TTLms)*time.Millisecond)
		} else {
			s.Delete(e.Key)
		}
		n++
	}
	return n, sc.Err()
}
