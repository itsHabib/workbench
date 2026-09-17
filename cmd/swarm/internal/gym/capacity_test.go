package gym

import (
	"strconv"
	"testing"
)

// The capacity grader must pass a perfect tracker, fail a stale one, and
// count silence as dropped.
func TestCapacityGrader(t *testing.T) {
	msgs := generate(4, 10, 7)
	if len(msgs) != 40 {
		t.Fatalf("%d messages", len(msgs))
	}
	if again := generate(4, 10, 7); len(again) != len(msgs) || again[17] != msgs[17] {
		t.Fatal("generation is not deterministic")
	}
	asks := 0
	perfect := &mailbox{Batch: 5, Msgs: msgs}
	stale := &mailbox{Batch: 5, Msgs: msgs}
	for i, m := range msgs {
		if (i+1)%5 == 0 || i == len(msgs)-1 {
			perfect.Cursor, stale.Cursor = i+1, i+1
		}
		if m.Op != "ASK" {
			continue
		}
		asks++
		at := ((i / 5) + 1) * 5
		perfect.Replies = append(perfect.Replies, mailReply{m.Thread, m.Key, strconv.Itoa(m.Want), at})
		stale.Replies = append(stale.Replies, mailReply{m.Thread, m.Key, strconv.Itoa(m.Want + 1), at})
	}
	perfect.Cursor, stale.Cursor = len(msgs), len(msgs)
	if a, c, w, d := gradeBox(perfect); a != asks || c != asks || w != 0 || d != 0 {
		t.Fatalf("perfect tracker graded %d/%d wrong %d dropped %d", c, a, w, d)
	}
	if _, c, w, _ := gradeBox(stale); c != 0 || w != asks {
		t.Fatalf("stale tracker graded correct %d wrong %d", c, w)
	}
	silent := &mailbox{Batch: 5, Msgs: msgs, Cursor: len(msgs)}
	if _, c, _, d := gradeBox(silent); c != 0 || d != asks {
		t.Fatalf("silent tracker graded correct %d dropped %d of %d", c, d, asks)
	}
	if asks < 4 {
		t.Fatalf("only %d asks in 40 messages", asks)
	}
}
