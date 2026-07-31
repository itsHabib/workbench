package reviewfindings

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsShipCompatibleArtifact(t *testing.T) {
	artifact := validArtifact()
	if err := Validate(artifact); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

func TestValidateRefusesMalformedArtifacts(t *testing.T) {
	tests := map[string]func(*Artifact){
		"unknown major":  func(a *Artifact) { a.SchemaVersion = 2 },
		"empty findings": func(a *Artifact) { a.Findings = nil },
		"unsourced":      func(a *Artifact) { a.Findings[0].Sources = nil },
		"source outside completed": func(a *Artifact) {
			a.Findings[0].Sources[0].Reviewer = "claude"
		},
		"panel overlap":            func(a *Artifact) { a.Panel.Missing = []string{"codex"} },
		"repo interior whitespace": func(a *Artifact) { a.Subject.Repo = "owner name/repo" },
		"malformed catalog revision": func(a *Artifact) {
			a.Producer.CatalogRevision = "sha256:not-a-digest"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := validArtifact()
			mutate(&artifact)
			if err := Validate(artifact); err == nil {
				t.Fatal("Validate() error = nil, want refusal")
			}
		})
	}
}

func TestCatalogRevisionCompatibility(t *testing.T) {
	tests := []string{
		"",
		strings.Repeat("a", 40),
		strings.Repeat("b", 64),
		"sha256:" + strings.Repeat("c", 64),
	}
	for _, revision := range tests {
		artifact := validArtifact()
		artifact.Producer.CatalogRevision = revision
		if err := Validate(artifact); err != nil {
			t.Fatalf("Validate(catalog_revision=%q) error = %v", revision, err)
		}
	}
}

func TestDecodeRefusesPresentEmptyCatalogRevision(t *testing.T) {
	artifact := validArtifact()
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"generated_at"`, `"catalog_revision":"","generated_at"`, 1))
	if _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "catalog_revision") {
		t.Fatalf("Decode() error = %v, want present malformed provenance refusal", err)
	}
}

func TestCanonicalSHA256IgnoresTransportAndOrdering(t *testing.T) {
	left := validArtifact()
	left.Findings[0].Evidence = "<sub>review evidence</sub>"
	right := validArtifact()
	right.Findings[0].Evidence = left.Findings[0].Evidence
	right.ArtifactID = "rf_regenerated"
	right.Producer.GeneratedAt = right.Producer.GeneratedAt.Add(time.Hour)
	right.Panel.Requested = []string{"claude", "codex"}
	right.Panel.Completed = []string{"codex"}
	right.Panel.Missing = []string{"claude"}
	leftDigest, err := CanonicalSHA256(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := CanonicalSHA256(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("digest changed across transport/order variation: %s != %s", leftDigest, rightDigest)
	}
	const shipDigest = "a9dceebffa6a5cb0669aece8c10c30ca633913ce5795b9f44577abaae2af4da1"
	if leftDigest != shipDigest {
		t.Fatalf("digest = %s, want Ship conformance vector %s", leftDigest, shipDigest)
	}
}

func TestCanonicalSHA256DoesNotMutatePanel(t *testing.T) {
	artifact := validArtifact()
	artifact.Panel.Requested = []string{"codex", "claude"}
	before := append([]string(nil), artifact.Panel.Requested...)
	if _, err := CanonicalSHA256(artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Panel.Requested[0] != before[0] || artifact.Panel.Requested[1] != before[1] {
		t.Fatalf("panel mutated: got %v want %v", artifact.Panel.Requested, before)
	}
}

func TestPanelPartitionProperty(t *testing.T) {
	const seed = int64(20260727)
	random := rand.New(rand.NewSource(seed))
	for caseIndex := 0; caseIndex < 100; caseIndex++ {
		artifact := validArtifact()
		size := random.Intn(15) + 1
		artifact.Panel = Panel{}
		for index := 0; index < size; index++ {
			member := "reviewer-" + strings.Repeat("x", index+1)
			artifact.Panel.Requested = append(artifact.Panel.Requested, member)
			if random.Intn(2) == 0 {
				artifact.Panel.Completed = append(artifact.Panel.Completed, member)
				continue
			}
			artifact.Panel.Missing = append(artifact.Panel.Missing, member)
		}
		if len(artifact.Panel.Completed) == 0 {
			artifact.Panel.Completed = append(artifact.Panel.Completed, artifact.Panel.Missing[0])
			artifact.Panel.Missing = artifact.Panel.Missing[1:]
		}
		artifact.Findings[0].Sources[0].Reviewer = artifact.Panel.Completed[0]
		if err := Validate(artifact); err != nil {
			t.Fatalf("seed=%d case=%d valid partition refused: %v", seed, caseIndex, err)
		}
	}
}

func TestAddressWorkRoundTripAndTamperRefusal(t *testing.T) {
	work, err := NewAddressWork("dsr_child", "dss_impl", 2, validArtifact())
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeAddressWork(work)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAddressWork(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkID != AddressWorkID("dsr_child", "dss_impl", work.ArtifactDigest, 2) {
		t.Fatalf("work id = %q", decoded.WorkID)
	}
	decoded.Artifact.Findings[0].Summary = "tampered"
	if err := ValidateAddressWork(decoded); err == nil || !strings.Contains(err.Error(), "artifact_digest") {
		t.Fatalf("tampered work error = %v", err)
	}
}

func TestAddressWorkSchemaMatchesType(t *testing.T) {
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(AddressWorkSchema, &schema); err != nil {
		t.Fatal(err)
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	typ := reflect.TypeOf(AddressWorkV1{})
	if len(schema.Properties) != typ.NumField() {
		t.Fatalf("schema properties=%d fields=%d", len(schema.Properties), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("schema missing %q", name)
		}
		if !required[name] {
			t.Fatalf("address work field %q must be required", name)
		}
	}
}

func validArtifact() Artifact {
	return Artifact{
		SchemaVersion: 1,
		ArtifactID:    "rf_123",
		Decision:      "address",
		Subject: Subject{
			Type: "pull_request", Repo: "itsHabib/ship", Number: 184,
			HeadSHA: strings.Repeat("a", 40),
		},
		Producer: Producer{
			ID: "codex:reviewfindings-github", Harness: "codex",
			GeneratedAt: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		},
		Panel: Panel{
			Requested: []string{"codex", "claude"},
			Completed: []string{"codex"},
			Missing:   []string{"claude"},
		},
		Findings: []Finding{{
			ID: "codex-1", Severity: "p1", Summary: "stale head",
			Evidence: "The requested head is stale.",
			Sources: []Source{{
				Reviewer: "codex", CommentID: "1",
				URL:  "https://github.com/itsHabib/ship/pull/184#discussion_r1",
				File: "packages/driver/src/engine.ts", Line: 123,
			}},
		}},
	}
}
