package reviewpanel

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestValidateExactHeadPartition(t *testing.T) {
	e := validEvidence()
	if err := Validate(e); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Evidence){
		"stale completed": func(e *Evidence) { e.Completed[0].HeadSHA = "old" },
		"invalid state":   func(e *Evidence) { e.Completed[0].State = "PENDING" },
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

func TestMarshalRequiredCollectionsAsArrays(t *testing.T) {
	e := Evidence{
		SchemaVersion: SchemaVersion,
		Subject:       Subject{Repo: "o/r", Number: 1, HeadSHA: "head"},
		Declaration:   Declaration{Path: ".ship.json"},
	}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	assertArray := func(name string, value any) {
		t.Helper()
		got, ok := value.([]any)
		if !ok || len(got) != 0 {
			t.Fatalf("%s = %#v, want empty array", name, value)
		}
	}
	declaration, ok := decoded["declaration"].(map[string]any)
	if !ok {
		t.Fatalf("declaration = %#v", decoded["declaration"])
	}
	assertArray("declaration.expected", declaration["expected"])
	for _, name := range []string{"completed", "pending", "missing", "unknown"} {
		assertArray(name, decoded[name])
	}
}

func TestSchemaCollectionAndStateConformance(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Type  string `json:"type"`
			Ref   string `json:"$ref"`
			Items struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
		Defs map[string]struct {
			Type string `json:"type"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["completed"].Type != "array" ||
		schema.Defs["names"].Type != "array" {
		t.Fatal("ReviewPanelV1 collections must be arrays")
	}
	for _, name := range []string{"pending", "missing", "unknown"} {
		if schema.Properties[name].Ref != "#/$defs/names" {
			t.Fatalf("%s does not reference array names definition", name)
		}
	}
	got := schema.Properties["completed"].Items.Properties["state"].Enum
	want := []string{"APPROVED", "COMMENTED", "CHANGES_REQUESTED", "CLEAN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state enum = %v, want %v", got, want)
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
