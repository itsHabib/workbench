package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Channel URL paths on the capture sink. Each webhook channel points a distinct
// path at the in-process server, so a recorded delivery names the channel its
// route selected.
const (
	pathEscalations = "/escalations"
	pathBlocks      = "/blocks"
	pathParks       = "/parks"
	pathCatch       = "/catch"
)

// routesFile is the shape flare's config.Load accepts, rebuilt locally so the
// e2e treats flare as a black box (binary + artifacts) instead of importing its
// internal config package. A little copying to keep the boundary clean.
type routesFile struct {
	Version     int                `json:"version"`
	PollSeconds int                `json:"poll_seconds"`
	Sources     []sourceDef        `json:"sources"`
	Channels    map[string]channel `json:"channels"`
	Routes      []routeDef         `json:"routes"`
	CatchAll    string             `json:"catch_all"`
}

type sourceDef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type channel struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
}

type routeDef struct {
	Match   matchDef `json:"match"`
	Channel string   `json:"channel"`
}

type matchDef struct {
	Source   string `json:"source,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Decision string `json:"decision,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
}

// writeRoutes writes a routes config pointing the sources at the seeded fixtures
// and every channel at the capture sink. The routes select a distinct channel
// per fact so a delivery's path proves route selection.
func writeRoutes(t *testing.T, dir, gatePath, shipPath string, s *sink) string {
	t.Helper()
	cfg := routesFile{
		Version:     1,
		PollSeconds: 60,
		Sources: []sourceDef{
			{Name: "gate", Kind: "gate-log", Path: gatePath},
			{Name: "ship", Kind: "ship-receipts", Path: shipPath},
		},
		Channels: map[string]channel{
			"escalations": {Type: "webhook", URL: s.url(pathEscalations)},
			"blocks":      {Type: "webhook", URL: s.url(pathBlocks)},
			"parks":       {Type: "webhook", URL: s.url(pathParks)},
			"catch":       {Type: "webhook", URL: s.url(pathCatch)},
		},
		Routes: []routeDef{
			{Match: matchDef{Source: "gate", Kind: "escalation"}, Channel: "escalations"},
			{Match: matchDef{Source: "gate", Decision: "block"}, Channel: "blocks"},
			{Match: matchDef{Source: "ship", Outcome: "parked"}, Channel: "parks"},
		},
		CatchAll: "catch",
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal routes: %v", err)
	}
	path := filepath.Join(dir, "routes.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	return path
}

// scenario seeds the fixtures + routes config into a fresh temp tree and returns
// the config path, the shared state dir, and the capture sink.
func scenario(t *testing.T) (string, string, *sink) {
	t.Helper()
	root := t.TempDir()
	s := newSink()
	t.Cleanup(s.close)
	gatePath := seedGateLog(t, root)
	shipPath := seedReceipts(t, root)
	cfg := writeRoutes(t, root, gatePath, shipPath, s)
	state := filepath.Join(root, "state")
	return cfg, state, s
}

func TestE2ESweepDeliversExpectedPagesToCaptureSink(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds and execs the flare binary")
	}
	cfg, state, s := scenario(t)

	runSweep(t, cfg, state)

	got := s.deliveries()
	// Exactly three page-worthy facts reach the sink: the escalation, the block
	// verdict, and the park receipt. The passing verdict and the merged receipt
	// never become events, so nothing lands on the catch-all.
	if len(got) != 3 {
		t.Fatalf("expected 3 deliveries, got %d: %+v", len(got), got)
	}
	assertDelivered(t, got, pathEscalations, "esc-1")
	assertDelivered(t, got, pathBlocks, "vrd-block-1")
	assertDelivered(t, got, pathParks, "run-7:parked")
	if n := countPath(got, pathCatch); n != 0 {
		t.Fatalf("catch-all received %d deliveries; the pass verdict / merged receipt must not page", n)
	}
}

func TestE2EResweepIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds and execs the flare binary")
	}
	cfg, state, s := scenario(t)

	runSweep(t, cfg, state)
	first := len(s.deliveries())
	if first != 3 {
		t.Fatalf("first sweep: expected 3 deliveries, got %d", first)
	}

	// A second sweep against the same -state dir over unchanged fixtures must
	// re-page nothing: the journal's seen-set and the advanced cursors hold.
	runSweep(t, cfg, state)
	if second := len(s.deliveries()); second != first {
		t.Fatalf("resweep produced %d new deliveries; dedupe must hold (want %d total, got %d)", second-first, first, second)
	}
}

func assertDelivered(t *testing.T, got []delivery, path, id string) {
	t.Helper()
	for _, d := range got {
		if d.Path == path && d.Payload["id"] == id {
			return
		}
	}
	t.Fatalf("no delivery of event %q on channel %q; got %+v", id, path, got)
}

func countPath(got []delivery, path string) int {
	n := 0
	for _, d := range got {
		if d.Path == path {
			n++
		}
	}
	return n
}
