package reviewpanel

import (
	"encoding/json"
	"testing"
)

func TestValidateExactHeadPartition(t *testing.T) {
	e := validEvidence()
	if err := Validate(e); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Evidence){
		"stale completed": func(e *Evidence) { e.Completed[0].HeadSHA = "old" },
		"overlap":         func(e *Evidence) { e.Pending = append(e.Pending, "codex") },
		"gap":             func(e *Evidence) { e.Missing = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			e := validEvidence()
			mutate(&e)
			if err := Validate(e); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestSchemaCarriesVersion(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Const int `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties["schema_version"].Const; got != SchemaVersion {
		t.Fatalf("schema_version const = %d, want %d", got, SchemaVersion)
	}
}

func validEvidence() Evidence {
	return Evidence{
		SchemaVersion: 1,
		Subject:       Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   Declaration{Path: ".ship.json", Revision: "blob", Expected: []string{"codex", "cursor", "claude"}},
		Completed:     []Reviewer{{Name: "codex", Actor: "codex[bot]", State: "APPROVED", HeadSHA: "head", ReviewID: 1}},
		Pending:       []string{"cursor"},
		Missing:       []string{"claude"},
	}
}
