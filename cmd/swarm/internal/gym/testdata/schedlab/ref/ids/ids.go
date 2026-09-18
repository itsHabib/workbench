package ids

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

type ID string

func (id ID) String() string { return string(id) }

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func Parse(s string) (ID, error) {
	if len(s) != 19 || s[12] != '-' || !isHex(s[:12]) || !isHex(s[13:]) {
		return "", fmt.Errorf("ids: %q: %w", s, errs.ErrInvalid)
	}
	return ID(s), nil
}

func (id ID) Time() (time.Time, error) {
	if _, err := Parse(string(id)); err != nil {
		return time.Time{}, err
	}
	ms, _ := strconv.ParseInt(string(id[:12]), 16, 64)
	return time.UnixMilli(ms).UTC(), nil
}

func Sort(ids []ID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

type Gen struct {
	mu   sync.Mutex
	c    clock.Clock
	ms   int64
	seq  int64
	used bool
}

func New(c clock.Clock) *Gen { return &Gen{c: c} }

func (g *Gen) Next() ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := clock.Millis(g.c)
	if t < 0 {
		t = 0
	}
	if t < g.ms {
		t = g.ms
	}
	seq := int64(0)
	if g.used && t == g.ms {
		seq = g.seq + 1
		if seq > 0xffffff {
			t++
			seq = 0
		}
	}
	g.ms, g.seq, g.used = t, seq, true
	return ID(fmt.Sprintf("%012x-%06x", t, seq))
}
