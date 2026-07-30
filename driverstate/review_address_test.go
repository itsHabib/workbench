package driverstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/contracts/reviewfindings"
)

const (
	addressTestRun    = "dsr_impl"
	addressTestStream = "dss_impl"
)

func TestPrepareReviewAddressConsumesExactHeadOnce(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	work, event, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.V != dsc.AddressVersion || event.Kind != dsc.KindReviewAddressPrepared {
		t.Fatalf("prepared event = %+v", event)
	}
	data, err := os.ReadFile(filepath.Join(dir, addressTestRun, filepath.FromSlash(addressWorkRef(work.WorkID))))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := reviewfindings.DecodeAddressWork(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkID != work.WorkID || decoded.ArtifactID != artifact.ArtifactID {
		t.Fatalf("persisted work = %+v", decoded)
	}
	state, err := Reduce(dir, addressTestRun)
	if err != nil {
		t.Fatal(err)
	}
	record := state.Streams[addressTestStream].ReviewAddresses[0]
	if record.Status != "pending" || record.WorkID != work.WorkID {
		t.Fatalf("reduced address = %+v", record)
	}

	_, _, err = PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	var refusal AddressRefusal
	if !errors.As(err, &refusal) || refusal.Code != "duplicate" || refusal.WorkRef == "" {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestAddressLifecycleRejectsCrossRootBeforeMutation(t *testing.T) {
	_, foreignLease, _ := addressFixture(t, 1)
	dir, lease, artifact := addressFixture(t, 1)
	input := PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	}
	before := snapshotTree(t, dir)
	if _, _, err := PrepareReviewAddress(dir, foreignLease, input); err == nil {
		t.Fatal("cross-root prepare must reject")
	}
	assertTreeUnchanged(t, dir, before)

	work, _, err := PrepareReviewAddress(dir, lease, input)
	if err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, dir)
	if _, _, err := ClaimReviewAddress(dir, foreignLease, addressTestStream, work.WorkID, "session:address"); err == nil {
		t.Fatal("cross-root claim must reject")
	}
	assertTreeUnchanged(t, dir, before)

	if _, _, err := ClaimReviewAddress(dir, lease, addressTestStream, work.WorkID, "session:address"); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, dir)
	if _, err := StartReviewAddress(dir, foreignLease, addressTestStream, work.WorkID, "task_1"); err == nil {
		t.Fatal("cross-root start must reject")
	}
	assertTreeUnchanged(t, dir, before)

	if _, err := StartReviewAddress(dir, lease, addressTestStream, work.WorkID, "task_1"); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, dir)
	if _, err := CompleteReviewAddress(dir, foreignLease, addressTestStream, work.WorkID, strings.Repeat("b", 40)); err == nil {
		t.Fatal("cross-root complete must reject")
	}
	assertTreeUnchanged(t, dir, before)
}

func TestPrepareReviewAddressRequiresLiveLeaseBeforeWorkWrite(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	if err := ExpireLeaseForTest(dir, addressTestRun); err != nil {
		t.Fatal(err)
	}
	_, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, addressTestRun, "review-address")); !os.IsNotExist(err) {
		t.Fatalf("expired lease wrote address work: %v", err)
	}
}

func TestClaimReviewAddressRequiresLiveLeaseBeforeChildWrite(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	work, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ExpireLeaseForTest(dir, addressTestRun); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, dir)
	_, _, err = ClaimReviewAddress(dir, lease, addressTestStream, work.WorkID, "session:address")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease error = %v", err)
	}
	assertTreeUnchanged(t, dir, before)
}

func snapshotTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	snapshot := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[filepath.ToSlash(relative)+"/"] = nil
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertTreeUnchanged(t *testing.T, dir string, before map[string][]byte) {
	t.Helper()
	after := snapshotTree(t, dir)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("cross-root operation mutated %s:\nbefore=%v\nafter=%v", dir, before, after)
	}
}

func TestPrepareReviewAddressRefusalMatrix(t *testing.T) {
	tests := []struct {
		name      string
		cycles    int
		maxCycles int
		mutate    func(*reviewfindings.Artifact, *string)
		code      string
	}{
		{"stale head", 1, 3, func(_ *reviewfindings.Artifact, head *string) { *head = strings.Repeat("b", 40) }, "stale-head"},
		{"cycle exhausted", 2, 1, func(_ *reviewfindings.Artifact, _ *string) {}, "cycle-exhausted"},
		{"subject mismatch", 1, 3, func(a *reviewfindings.Artifact, _ *string) { a.Subject.Number++ }, "subject-mismatch"},
		{"panel mismatch", 1, 3, func(a *reviewfindings.Artifact, _ *string) { a.Findings[0].Sources[0].Reviewer = "missing" }, "malformed-artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, lease, artifact := addressFixture(t, test.cycles)
			head := artifact.Subject.HeadSHA
			test.mutate(&artifact, &head)
			_, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
				Stream: addressTestStream, LiveHead: head,
				MaxCycles: test.maxCycles, Artifact: artifact,
			})
			var refusal AddressRefusal
			if !errors.As(err, &refusal) || refusal.Code != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestPrepareReviewAddressCrashAdoptsOrphan(t *testing.T) {
	crash := errors.New("crash")
	for _, test := range []struct {
		name  string
		hooks prepareHooks
	}{
		{"after temp", prepareHooks{afterTemp: func() error { return crash }}},
		{"after rename", prepareHooks{afterRename: func() error { return crash }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, lease, artifact := addressFixture(t, 1)
			input := PrepareAddressInput{
				Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
				MaxCycles: 3, Artifact: artifact,
			}
			if _, _, err := prepareReviewAddress(dir, lease, input, test.hooks); !errors.Is(err, crash) {
				t.Fatalf("injected error = %v", err)
			}
			work, _, err := PrepareReviewAddress(dir, lease, input)
			if err != nil {
				t.Fatalf("retry must adopt deterministic orphan: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, addressTestRun, filepath.FromSlash(addressWorkRef(work.WorkID)))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareReviewAddressLostResponseRefusesAsDuplicate(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	input := PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	}
	crash := errors.New("lost response")
	if _, _, err := prepareReviewAddress(dir, lease, input, prepareHooks{
		afterAppend: func() error { return crash },
	}); !errors.Is(err, crash) {
		t.Fatalf("injected error = %v", err)
	}
	_, _, err := PrepareReviewAddress(dir, lease, input)
	var refusal AddressRefusal
	if !errors.As(err, &refusal) || refusal.Code != "duplicate" {
		t.Fatalf("retry error = %v", err)
	}
}

func TestPrepareReviewAddressRefusesWorkCollisionAndSecondArtifactInCycle(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	digest, err := reviewfindings.CanonicalSHA256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	workID := reviewfindings.AddressWorkID(addressTestRun, addressTestStream, digest, 1)
	path := filepath.Join(dir, addressTestRun, filepath.FromSlash(addressWorkRef(workID)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("collision"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	var refusal AddressRefusal
	if !errors.As(err, &refusal) || refusal.Code != "work-collision" {
		t.Fatalf("collision error = %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	}); err != nil {
		t.Fatal(err)
	}
	artifact.ArtifactID = "rf_other"
	artifact.Findings[0].Summary = "different semantic artifact"
	_, _, err = PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	if !errors.As(err, &refusal) || refusal.Code != "cycle-consumed" {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestReviewAddressLifecycleAndAmbiguousResume(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	work, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := ClaimReviewAddress(dir, lease, addressTestStream, work.WorkID, "session:address")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(child, "dsr_") {
		t.Fatalf("child = %q", child)
	}
	if retryChild, _, err := ClaimReviewAddress(dir, lease, addressTestStream, work.WorkID, "session:address"); err != nil || retryChild != child {
		t.Fatalf("claim retry child=%q err=%v", retryChild, err)
	}
	resume, err := ResumeReviewAddress(dir, addressTestRun, addressTestStream, work.WorkID)
	var parked AddressParked
	if !errors.As(err, &parked) || !resume.Parked {
		t.Fatalf("claimed resume = %+v, %v", resume, err)
	}
	if _, err := StartReviewAddress(dir, lease, addressTestStream, work.WorkID, "task_123"); err != nil {
		t.Fatal(err)
	}
	if _, err := StartReviewAddress(dir, lease, addressTestStream, work.WorkID, "task_123"); err != nil {
		t.Fatalf("start lost-response retry: %v", err)
	}
	resume, err = ResumeReviewAddress(dir, addressTestRun, addressTestStream, work.WorkID)
	if err != nil || resume.State.TaskID != "task_123" || resume.Parked {
		t.Fatalf("started resume = %+v, %v", resume, err)
	}
	newHead := strings.Repeat("c", 40)
	if _, err := CompleteReviewAddress(dir, lease, addressTestStream, work.WorkID, newHead); err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteReviewAddress(dir, lease, addressTestStream, work.WorkID, newHead); err != nil {
		t.Fatalf("complete lost-response retry: %v", err)
	}
	assertCompletedAddressState(t, dir, newHead)
}

func assertCompletedAddressState(t *testing.T, dir, newHead string) {
	t.Helper()
	state, err := Reduce(dir, addressTestRun)
	if err != nil {
		t.Fatal(err)
	}
	record := state.Streams[addressTestStream].ReviewAddresses[0]
	if record.Status != "completed" || record.ResultHeadSHA != newHead || state.Streams[addressTestStream].HeadSHA != newHead {
		t.Fatalf("completed state = %+v", state.Streams[addressTestStream])
	}
	events, err := Events(dir, addressTestRun)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []dsc.Kind{dsc.KindReviewAddressClaimed, dsc.KindReviewAddressStarted, dsc.KindReviewAddressCompleted} {
		if got := countKind(events, kind); got != 1 {
			t.Fatalf("%s events = %d, want 1", kind, got)
		}
	}
}

func countKind(events []Event, kind dsc.Kind) int {
	count := 0
	for _, event := range events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func TestMixedLedgerExcludesOldWriterAfterAddressEvent(t *testing.T) {
	dir, lease, artifact := addressFixture(t, 1)
	if _, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
		Stream: addressTestStream, LiveHead: artifact.Subject.HeadSHA,
		MaxCycles: 3, Artifact: artifact,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, addressTestRun, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := decodeLedger(data)
	if err != nil {
		t.Fatalf("new mixed reader: %v", err)
	}
	if events[len(events)-1].V != dsc.AddressVersion {
		t.Fatalf("last version = %q", events[len(events)-1].V)
	}
	if err := oldWriterRead(data); !errors.Is(err, dsc.ErrUnknownVersion) {
		t.Fatalf("old writer must refuse before append: %v", err)
	}
}

type addressCorpusCase struct {
	Name                  string   `json:"name"`
	ArtifactJSON          string   `json:"artifact_json"`
	LiveHead              string   `json:"live_head"`
	Cycle                 int      `json:"cycle"`
	MaxCycles             int      `json:"max_cycles"`
	ConsumedIDs           []string `json:"consumed_ids"`
	ConsumedArtifactJSONs []string `json:"consumed_artifact_jsons"`
	Calls                 []string `json:"calls"`
	ExpectedRefusal       string   `json:"expected_refusal"`
	Projection            struct {
		Accepted   bool `json:"accepted"`
		Consumed   bool `json:"consumed"`
		AtMostOnce bool `json:"at_most_once"`
	} `json:"projection"`
	Workbench struct {
		WorkStatus string `json:"work_status"`
		ClaimCount int    `json:"claim_count"`
	} `json:"workbench"`
	Ship struct {
		ProviderCallCount int `json:"provider_call_count"`
	} `json:"ship"`
}

func TestWorkbenchExecutesAddressV1Corpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "contracts", "reviewfindings", "testdata", "address-v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if filepath.Base(path) == "manifest.json" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var scenario addressCorpusCase
		if err := json.Unmarshal(data, &scenario); err != nil {
			t.Fatal(err)
		}
		t.Run(scenario.Name, func(t *testing.T) {
			runAddressCorpusCase(t, scenario)
		})
	}
}

func runAddressCorpusCase(t *testing.T, scenario addressCorpusCase) {
	t.Helper()
	dir, lease, _ := addressFixture(t, scenario.Cycle)
	seedConsumedArtifacts(t, dir, lease, scenario)
	artifact, decodeErr := reviewfindings.Decode([]byte(scenario.ArtifactJSON))
	var work reviewfindings.AddressWorkV1
	accepted := false
	providerCalls := 0
	refusalCode := ""
	for _, call := range scenario.Calls {
		switch call {
		case "accept":
			if decodeErr != nil {
				refusalCode = "malformed-artifact"
				continue
			}
			next, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
				Stream: addressTestStream, LiveHead: scenario.LiveHead,
				MaxCycles: scenario.MaxCycles, Artifact: artifact,
			})
			if err == nil {
				work = next
				accepted = true
				providerCalls++
				continue
			}
			var refusal AddressRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("accept error = %v", err)
			}
			refusalCode = refusal.Code
		case "resume":
			if work.WorkID == "" {
				t.Fatal("resume call has no accepted work")
			}
			if _, err := ResumeReviewAddress(dir, addressTestRun, addressTestStream, work.WorkID); err != nil {
				t.Fatalf("resume: %v", err)
			}
		default:
			t.Fatalf("unknown corpus call %q", call)
		}
	}
	state, err := Reduce(dir, addressTestRun)
	if err != nil {
		t.Fatal(err)
	}
	records := state.Streams[addressTestStream].ReviewAddresses
	if accepted != scenario.Projection.Accepted || (len(records) == 1) != scenario.Projection.Consumed {
		t.Fatalf("projection accepted=%v consumed=%v records=%d", accepted, scenario.Projection.Consumed, len(records))
	}
	if scenario.Projection.AtMostOnce && len(records) > 1 {
		t.Fatalf("at-most-once violated: %d records", len(records))
	}
	if refusalCode != scenario.ExpectedRefusal {
		t.Fatalf("refusal = %q, want %q", refusalCode, scenario.ExpectedRefusal)
	}
	if len(records) == 1 && records[0].Status != scenario.Workbench.WorkStatus {
		t.Fatalf("work status = %q, want %q", records[0].Status, scenario.Workbench.WorkStatus)
	}
	if scenario.Workbench.ClaimCount != 0 {
		t.Fatalf("workbench claim count = %d, want 0", scenario.Workbench.ClaimCount)
	}
	if providerCalls != scenario.Ship.ProviderCallCount {
		t.Fatalf("ship provider calls = %d, want %d", providerCalls, scenario.Ship.ProviderCallCount)
	}
}

func seedConsumedArtifacts(t *testing.T, dir string, lease Lease, scenario addressCorpusCase) {
	t.Helper()
	if len(scenario.ConsumedIDs) != len(scenario.ConsumedArtifactJSONs) {
		t.Fatalf("consumed ids=%d artifacts=%d", len(scenario.ConsumedIDs), len(scenario.ConsumedArtifactJSONs))
	}
	for index, raw := range scenario.ConsumedArtifactJSONs {
		artifact, err := reviewfindings.Decode([]byte(raw))
		if err != nil {
			t.Fatalf("decode consumed artifact %d: %v", index, err)
		}
		if artifact.ArtifactID != scenario.ConsumedIDs[index] {
			t.Fatalf("consumed artifact id = %q, want %q", artifact.ArtifactID, scenario.ConsumedIDs[index])
		}
		if _, _, err := PrepareReviewAddress(dir, lease, PrepareAddressInput{
			Stream: addressTestStream, LiveHead: scenario.LiveHead,
			MaxCycles: scenario.MaxCycles, Artifact: artifact,
		}); err != nil {
			t.Fatalf("seed consumed artifact %s: %v", artifact.ArtifactID, err)
		}
	}
}

func oldWriterRead(data []byte) error {
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event dsc.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		if event.V != dsc.Version {
			return dsc.ErrUnknownVersion
		}
	}
	return nil
}

func addressFixture(t *testing.T, cycles int) (string, Lease, reviewfindings.Artifact) {
	t.Helper()
	dir := t.TempDir()
	lease, err := Claim(dir, addressTestRun, "session:impl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	head := strings.Repeat("a", 40)
	events := []Event{
		ev("evt_import", dsc.KindRunImported, "", "session:impl", baseTime, dsc.RunImportedBody{
			Repo: "itsHabib/workbench", Source: "docs/task.md", GeneratedAt: "2026-07-29T00:00:00Z",
			Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: addressTestStream, DocPath: "docs/task.md"}},
			Parent: "dsr_parent", ParentStream: "dss_parent", DoneBoundary: dsc.DoneBoundaryGreen,
		}),
		ev("evt_dispatch", dsc.KindStreamDispatched, addressTestStream, "session:impl", baseTime.Add(time.Second), nil),
		ev("evt_attempt", dsc.KindStreamAttempt, addressTestStream, "session:impl", baseTime.Add(2*time.Second), dsc.StreamAttemptBody{
			Seq: 1, DocPath: "docs/task.md", Terminal: true, Commit: head,
		}),
		ev("evt_pr", dsc.KindStreamPROpened, addressTestStream, "session:impl", baseTime.Add(3*time.Second), dsc.StreamPROpenedBody{
			PR: 42, URL: "https://github.com/itsHabib/workbench/pull/42", HeadSHA: head,
		}),
	}
	for cycle := 1; cycle <= cycles; cycle++ {
		events = append(events, ev(
			"evt_cycle_"+string(rune('0'+cycle)), dsc.KindReviewCycle, addressTestStream,
			"session:impl", baseTime.Add(time.Duration(3+cycle)*time.Second),
			dsc.ReviewCycleBody{Cycle: cycle, PanelSettled: true, Findings: 1},
		))
	}
	for _, event := range events {
		if _, err := Append(dir, lease, event); err != nil {
			t.Fatalf("append %s: %v", event.Kind, err)
		}
	}
	return dir, lease, validAddressArtifact(head)
}

func validAddressArtifact(head string) reviewfindings.Artifact {
	return reviewfindings.Artifact{
		SchemaVersion: 1, ArtifactID: "rf_test", Decision: reviewfindings.DecisionAddress,
		Subject:  reviewfindings.Subject{Type: reviewfindings.SubjectPullRequest, Repo: "itsHabib/workbench", Number: 42, HeadSHA: head},
		Producer: reviewfindings.Producer{ID: "codex:test", Harness: "codex", GeneratedAt: baseTime},
		Panel:    reviewfindings.Panel{Requested: []string{"codex"}, Completed: []string{"codex"}, Missing: []string{}},
		Findings: []reviewfindings.Finding{{
			ID: "f1", Severity: "P1", Summary: "fix it", Evidence: "line is wrong",
			Sources: []reviewfindings.Source{{
				Reviewer: "codex", CommentID: "1", URL: "https://github.com/itsHabib/workbench/pull/42#discussion_r1",
				File: "x.go", Line: 10,
			}},
		}},
	}
}
