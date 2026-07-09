package journal

import (
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

func TestSeenSettlesDeliveredDroppedThrottledButRetriesErrors(t *testing.T) {
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for id, kind := range map[string]string{
		"a": Delivered, "b": Dropped, "c": Throttled, "d": Errored, "e": CursorAlert,
	} {
		if err := j.Append(Entry{Time: now, Kind: kind, EventID: id}); err != nil {
			t.Fatal(err)
		}
	}
	seen, err := j.Seen()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !seen[SeenKey("", id)] {
			t.Fatalf("%s must be settled", id)
		}
	}
	if seen[SeenKey("", "d")] {
		t.Fatal("an errored delivery must stay unsettled so it retries")
	}
	if seen[SeenKey("", "e")] {
		t.Fatal("a cursor-alert entry is not an event settlement")
	}
}

func TestSeenIsScopedBySource(t *testing.T) {
	// The same producer-local event ID from two sources is two distinct facts:
	// settling one must not mark the other as already seen.
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(Entry{Time: time.Now(), Kind: Delivered, Source: "gate-prod", EventID: "vrd_1"}); err != nil {
		t.Fatal(err)
	}
	seen, err := j.Seen()
	if err != nil {
		t.Fatal(err)
	}
	if !seen[SeenKey("gate-prod", "vrd_1")] {
		t.Fatal("the delivered event must be settled for its own source")
	}
	if seen[SeenKey("gate-staging", "vrd_1")] {
		t.Fatal("the same ID from another source must not be treated as seen")
	}
}

func TestCursorsRoundTripAndStartEmpty(t *testing.T) {
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := j.LoadCursors()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 0 || !c.LastPoll.IsZero() {
		t.Fatalf("fresh state must be empty, got %+v", c)
	}
	c.LastPoll = time.Now()
	c.Sources["gate"] = source.Cursor{Offset: 42, LastHash: "h9"}
	if err := j.SaveCursors(c); err != nil {
		t.Fatal(err)
	}
	got, err := j.LoadCursors()
	if err != nil {
		t.Fatal(err)
	}
	if got.Sources["gate"] != (source.Cursor{Offset: 42, LastHash: "h9"}) {
		t.Fatalf("cursor round-trip lost data: %+v", got.Sources["gate"])
	}
	if got.LastPoll.IsZero() {
		t.Fatal("last_poll must persist — it is the liveness fact status reads")
	}
}
