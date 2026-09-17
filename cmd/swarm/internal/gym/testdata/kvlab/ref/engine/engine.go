package engine

import (
	"io"
	"strconv"
	"strings"
	"time"

	"kvlab/query"
	"kvlab/stats"
	"kvlab/store"
	"kvlab/wal"
)

type Engine struct {
	s   *store.Store
	log io.Writer
}

func New(now func() time.Time, log io.Writer) *Engine {
	return &Engine{s: store.New(now), log: log}
}

func (e *Engine) write(en wal.Entry) {
	if e.log != nil {
		_, _ = io.WriteString(e.log, wal.Encode(en)+"\n")
	}
}

func (e *Engine) Exec(line string) (string, error) {
	c, err := query.Parse(line)
	if err != nil {
		return "", err
	}
	switch c.Op {
	case "SET":
		e.s.Set(c.Key, c.Val, c.TTL)
		e.write(wal.Entry{Op: "set", Key: c.Key, Val: c.Val, TTLms: c.TTL.Milliseconds()})
		return "OK", nil
	case "GET":
		if v, ok := e.s.Get(c.Key); ok {
			return v, nil
		}
		return "(nil)", nil
	case "DEL":
		if e.s.Delete(c.Key) {
			e.write(wal.Entry{Op: "del", Key: c.Key})
			return "1", nil
		}
		return "0", nil
	case "KEYS":
		return strings.Join(e.s.Keys(), "\n"), nil
	}
	return strconv.Itoa(e.s.Len()), nil
}

func (e *Engine) Restore(r io.Reader) error {
	_, err := wal.Replay(r, e.s)
	return err
}

func (e *Engine) ValueSizes() stats.Sum {
	var xs []float64
	for _, k := range e.s.Keys() {
		if v, ok := e.s.Get(k); ok {
			xs = append(xs, float64(len(v)))
		}
	}
	return stats.Summary(xs)
}
