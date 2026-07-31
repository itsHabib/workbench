package verify

import (
	"strings"
	"testing"
)

func TestParseFloorOutputValidTier(t *testing.T) {
	res, err := parseFloorOutput([]byte(`{"floor":"T2","files":3,"added":10,"removed":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Floor != "T2" || res.Files != 3 {
		t.Fatalf("parsed wrong result: %+v", res)
	}
}

// A floor binary that exits 0 with valid JSON but no usable tier must be an
// operational failure, never a verdict: tier.Rank maps unknown values to the
// highest rank, so an invalid floor recorded as a pass would read as
// "assessed at maximum risk" when nothing was assessed — and a wide grant
// would clear it.
func TestParseFloorOutputRefusesMissingTier(t *testing.T) {
	_, err := parseFloorOutput([]byte(`{"files":3,"added":10,"removed":2}`))
	if err == nil {
		t.Fatal("absent floor tier parsed as a valid result")
	}
	if !strings.Contains(err.Error(), "invalid tier") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestParseFloorOutputRefusesUnknownTier(t *testing.T) {
	_, err := parseFloorOutput([]byte(`{"floor":"T9","files":1}`))
	if err == nil {
		t.Fatal("unknown floor tier parsed as a valid result")
	}
	if !strings.Contains(err.Error(), "invalid tier") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestFloorWhyNamesMaxTierDriver(t *testing.T) {
	res, err := parseFloorOutput([]byte(`{
		"floor":"T3",
		"files":35,
		"added":2729,
		"removed":45,
		"signals":[
			{"signal":"docs","tier":"T0","why":"docs/copy: README.md"},
			{"signal":"internal-change","tier":"T1","why":"code change: internal/thing.go"},
			{"signal":"auth-crypto-secrets","tier":"T3","why":"auth/crypto/secret surface: internal/auth/session.go"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := floorWhy(res)
	want := "deterministic T3 floor: auth-crypto-secrets: auth/crypto/secret surface: internal/auth/session.go (35 files, +2729/-45)"
	if got != want {
		t.Fatalf("floorWhy() = %q, want %q", got, want)
	}
}

func TestFloorWhyLimitsMaxTierDrivers(t *testing.T) {
	res, err := parseFloorOutput([]byte(`{
		"floor":"T3",
		"files":4,
		"added":8,
		"removed":2,
		"signals":[
			{"signal":"one","tier":"T3","why":"first"},
			{"signal":"two","tier":"T3","why":"second"},
			{"signal":"three","tier":"T3","why":"third"},
			{"signal":"four","tier":"T3","why":"fourth"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := floorWhy(res)
	want := "deterministic T3 floor: one: first; two: second; three: third; +1 more max-tier drivers (4 files, +8/-2)"
	if got != want {
		t.Fatalf("floorWhy() = %q, want %q", got, want)
	}
}

func TestOrderFloorSignalsShowsMaxTierFirst(t *testing.T) {
	res, err := parseFloorOutput([]byte(`{
		"floor":"T3",
		"signals":[
			{"signal":"docs","tier":"T0","why":"docs/copy: README.md"},
			{"signal":"internal-change","tier":"T1","why":"code change: internal/thing.go"},
			{"signal":"auth-crypto-secrets","tier":"T3","why":"auth/crypto/secret surface: internal/auth/session.go"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := orderFloorSignals(res.Signals)
	if got[0].Tier != "T3" || got[1].Tier != "T1" || got[2].Tier != "T0" {
		t.Fatalf("signals not ordered high-to-low: %+v", got)
	}
}
