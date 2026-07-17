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
