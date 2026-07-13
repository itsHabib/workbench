package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

func TestAvailabilityZeroValueMarshalsUnknown(t *testing.T) {
	var value model.Availability[int]
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"state":"unknown"}` {
		t.Fatalf("zero availability = %s", got)
	}
}

func TestAvailabilityZeroValueUnmarshalsUnknown(t *testing.T) {
	var value model.Availability[int]
	if err := json.Unmarshal([]byte(`{}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.State != model.Unknown || value.Value != nil {
		t.Fatalf("zero availability = %+v", value)
	}
}

func TestAvailabilityStatesRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value model.Availability[int]
		state model.AvailabilityState
	}{
		{name: "available", value: model.Known(42), state: model.Available},
		{name: "unknown", value: model.Missing[int](), state: model.Unknown},
		{name: "unavailable", value: model.NotAvailable[int](), state: model.Unavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var got model.Availability[int]
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.State != test.state {
				t.Fatalf("state = %q want %q", got.State, test.state)
			}
		})
	}
}

func TestAvailabilityRejectsInvalidCombinations(t *testing.T) {
	value := 1
	for _, invalid := range []model.Availability[int]{
		{State: model.Available},
		{State: model.Unknown, Value: &value},
		{State: model.AvailabilityState("future")},
	} {
		if _, err := json.Marshal(invalid); err == nil {
			t.Fatalf("invalid availability marshaled: %+v", invalid)
		}
	}
}

func TestSourceReceiptValidation(t *testing.T) {
	if err := (model.SourceReceipt{Source: "ship", State: model.SourceOK}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (model.SourceReceipt{Source: "ship", State: model.SourceState("future")}).Validate(); err == nil {
		t.Fatal("invalid source state accepted")
	}
}

func TestSafeLinkValidation(t *testing.T) {
	valid := []model.SafeLink{
		{Label: "pull request", URL: "https://github.com/owner/repo/pull/1"},
		{Label: "spec", Path: "docs/features/example/spec.md"},
	}
	for _, link := range valid {
		if err := link.Validate(); err != nil {
			t.Fatalf("valid link rejected: %v", err)
		}
	}
	invalid := []model.SafeLink{
		{Label: "file", URL: "file:///tmp/secret"},
		{Label: "parent", Path: "../secret"},
		{Label: "absolute", Path: "C:\\secret"},
		{Label: "slash-absolute", Path: "C:/secret"},
		{Label: "ambiguous", URL: "https://example.com", Path: "docs/a.md"},
	}
	for _, link := range invalid {
		if err := link.Validate(); err == nil {
			t.Fatalf("invalid link accepted: %+v", link)
		}
	}
}

func TestSnapshotIgnoresAdditiveJSONFields(t *testing.T) {
	var snapshot model.Snapshot
	err := json.Unmarshal([]byte(`{"version":1,"future_field":true,"sources":[],"runs":[],"tasks":[],"pull_requests":[],"reliability":[],"tool_health":[],"attention":[],"repositories":[]}`), &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("version = %d", snapshot.Version)
	}
}

func TestKnownAvailabilityCarriesValue(t *testing.T) {
	value := model.Known("present")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"state":"available"`) || !strings.Contains(string(data), `"value":"present"`) {
		t.Fatalf("known availability = %s", data)
	}
}
