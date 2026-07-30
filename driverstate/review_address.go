package driverstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
	"github.com/itsHabib/workbench/contracts/reviewfindings"
)

const addressStream = "dss_address"

// AddressRefusal is a deterministic boundary refusal. Code is stable for CLI
// and conformance consumers; WorkRef is set for duplicate submissions.
type AddressRefusal struct {
	Code    string
	Message string
	WorkRef string
}

func (e AddressRefusal) Error() string {
	if e.WorkRef == "" {
		return fmt.Sprintf("review address refused [%s]: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("review address refused [%s]: %s (existing work: %s)", e.Code, e.Message, e.WorkRef)
}

// AddressParked marks an ambiguity that must not auto-dispatch a second child.
type AddressParked struct {
	WorkID  string
	Message string
}

func (e AddressParked) Error() string {
	return fmt.Sprintf("review address parked [%s]: %s", e.WorkID, e.Message)
}

// PrepareAddressInput contains the live facts outside the immutable artifact.
type PrepareAddressInput struct {
	Stream    string
	LiveHead  string
	MaxCycles int
	Artifact  reviewfindings.Artifact
}

// AddressResume is the reconstructable work plus its reduced lifecycle.
type AddressResume struct {
	Work   reviewfindings.AddressWorkV1 `json:"work"`
	State  dsc.ReviewAddressRecord      `json:"state"`
	Branch string                       `json:"implementation_branch,omitempty"`
	PR     int                          `json:"pr,omitempty"`
	URL    string                       `json:"url,omitempty"`
	Parked bool                         `json:"parked"`
}

type prepareHooks struct {
	afterTemp   func() error
	afterRename func() error
	afterAppend func() error
}

// PrepareReviewAddress validates and consumes one exact-head findings artifact.
// The caller must hold the authoritative original implementation child lease.
// Work is persisted before the v0.2 prepared event, forming an atomic
// consumption/outbox boundary that is safe to retry after every file boundary.
func PrepareReviewAddress(dir string, lease Lease, in PrepareAddressInput) (reviewfindings.AddressWorkV1, Event, error) {
	return prepareReviewAddress(dir, lease, in, prepareHooks{})
}

func prepareReviewAddress(dir string, lease Lease, in PrepareAddressInput, hooks prepareHooks) (reviewfindings.AddressWorkV1, Event, error) {
	if err := validatePrepareInput(in); err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, err
	}
	state, _, cycle, err := addressContext(dir, lease.Run(), in)
	if err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, err
	}
	rec := state.Streams[in.Stream]
	digest, err := reviewfindings.CanonicalSHA256(in.Artifact)
	if err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, refuse("malformed-artifact", err.Error(), "")
	}
	if existing, found := consumedAddress(rec.ReviewAddresses, in.Artifact.ArtifactID, digest); found {
		return reviewfindings.AddressWorkV1{}, Event{}, refuse("duplicate", "artifact id or digest was already consumed", existing.WorkRef)
	}
	if existing, found := consumedCycle(rec.ReviewAddresses, cycle); found {
		return reviewfindings.AddressWorkV1{}, Event{}, refuse("cycle-consumed", "review cycle already has address work", existing.WorkRef)
	}
	work, err := reviewfindings.NewAddressWork(lease.Run(), in.Stream, cycle, in.Artifact)
	if err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, refuse("malformed-artifact", err.Error(), "")
	}
	data, err := reviewfindings.EncodeAddressWork(work)
	if err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, err
	}
	ref := addressWorkRef(work.WorkID)
	if err := installAddressWork(dir, lease.Run(), ref, data, hooks); err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, err
	}
	workDigest := sha256Hex(data)
	body := dsc.ReviewAddressPreparedBody{
		WorkID: work.WorkID, WorkRef: ref, WorkDigest: workDigest,
		ArtifactID: work.ArtifactID, ArtifactDigest: work.ArtifactDigest,
		HeadSHA: work.SourceHeadSHA, Cycle: work.Cycle,
	}
	event, err := appendAddressEvent(dir, lease, in.Stream, dsc.KindReviewAddressPrepared, body)
	if err != nil {
		return reviewfindings.AddressWorkV1{}, Event{}, err
	}
	if hooks.afterAppend != nil {
		if err := hooks.afterAppend(); err != nil {
			return reviewfindings.AddressWorkV1{}, Event{}, err
		}
	}
	return work, event, nil
}

func validatePrepareInput(in PrepareAddressInput) error {
	if in.Stream == "" {
		return refuse("malformed", "stream is required", "")
	}
	if in.MaxCycles < 1 {
		return refuse("malformed", "max cycles must be positive", "")
	}
	if err := reviewfindings.Validate(in.Artifact); err != nil {
		return refuse("malformed-artifact", err.Error(), "")
	}
	if !validAddressHex(in.LiveHead, 40) {
		return refuse("malformed", "live head must be 40 lowercase hexadecimal characters", "")
	}
	return nil
}

func addressContext(dir, run string, in PrepareAddressInput) (dsc.RunState, []Event, int, error) {
	events, err := Events(dir, run)
	if err != nil {
		return dsc.RunState{}, nil, 0, err
	}
	state := FoldEvents(events)
	if state.Run.Parent == "" {
		return dsc.RunState{}, nil, 0, refuse("not-child-ledger", "address authority belongs on the original implementation child run", "")
	}
	rec, ok := state.Streams[in.Stream]
	if !ok || rec.Status != dsc.StatusPROpen {
		return dsc.RunState{}, nil, 0, refuse("not-pr-open", "stream must be at pr_open", "")
	}
	switch {
	case !strings.EqualFold(state.Run.Repo, in.Artifact.Subject.Repo):
		return dsc.RunState{}, nil, 0, refuse("subject-mismatch", "artifact repo does not match the child run", "")
	case rec.PR != in.Artifact.Subject.Number:
		return dsc.RunState{}, nil, 0, refuse("subject-mismatch", "artifact PR does not match the child stream", "")
	case !strings.EqualFold(in.LiveHead, in.Artifact.Subject.HeadSHA):
		return dsc.RunState{}, nil, 0, refuse("stale-head", fmt.Sprintf("artifact head %s is not live head %s", in.Artifact.Subject.HeadSHA, in.LiveHead), "")
	case rec.HeadSHA != "" && !strings.EqualFold(rec.HeadSHA, in.LiveHead):
		return dsc.RunState{}, nil, 0, refuse("stale-head", fmt.Sprintf("ledger head %s is not live head %s", rec.HeadSHA, in.LiveHead), "")
	}
	cycle, settled, findings := latestReviewCycle(events, in.Stream)
	switch {
	case cycle < 1:
		return dsc.RunState{}, nil, 0, refuse("missing-review-cycle", "no review cycle is recorded for the stream", "")
	case cycle > in.MaxCycles:
		return dsc.RunState{}, nil, 0, refuse("cycle-exhausted", fmt.Sprintf("review cycle %d exceeds engine maximum %d", cycle, in.MaxCycles), "")
	case !settled:
		return dsc.RunState{}, nil, 0, refuse("panel-incomplete", "latest review cycle is not panel-settled", "")
	case findings != len(in.Artifact.Findings):
		return dsc.RunState{}, nil, 0, refuse("finding-count-mismatch", "artifact findings do not match the recorded review cycle", "")
	}
	return state, events, cycle, nil
}

func latestReviewCycle(events []Event, stream string) (int, bool, int) {
	var latest dsc.ReviewCycleBody
	for _, event := range events {
		if event.Stream != stream || event.Kind != dsc.KindReviewCycle {
			continue
		}
		var body dsc.ReviewCycleBody
		if json.Unmarshal(event.Body, &body) == nil && body.Cycle >= latest.Cycle {
			latest = body
		}
	}
	return latest.Cycle, latest.PanelSettled, latest.Findings
}

func consumedAddress(records []dsc.ReviewAddressRecord, id, digest string) (dsc.ReviewAddressRecord, bool) {
	for _, record := range records {
		if record.ArtifactID == id || record.ArtifactDigest == digest {
			return record, true
		}
	}
	return dsc.ReviewAddressRecord{}, false
}

func consumedCycle(records []dsc.ReviewAddressRecord, cycle int) (dsc.ReviewAddressRecord, bool) {
	for _, record := range records {
		if record.Cycle == cycle {
			return record, true
		}
	}
	return dsc.ReviewAddressRecord{}, false
}

func refuse(code, message, workRef string) error {
	return AddressRefusal{Code: code, Message: message, WorkRef: workRef}
}

func addressWorkRef(workID string) string {
	return filepath.ToSlash(filepath.Join("review-address", workID+".json"))
}

func installAddressWork(dir, run, ref string, data []byte, hooks prepareHooks) error {
	target := filepath.Join(runDir(dir, run), filepath.FromSlash(ref))
	existing, err := os.ReadFile(target)
	if err == nil {
		if bytes.Equal(existing, data) {
			return syncAddressDir(filepath.Dir(target))
		}
		return refuse("work-collision", "deterministic work path contains different bytes", ref)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("driverstate: read address work: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("driverstate: create address work directory: %w", err)
	}
	tmp := target + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("driverstate: open address work temp: %w", err)
	}
	if err := finishWrite(file, tmp, data); err != nil {
		return err
	}
	if hooks.afterTemp != nil {
		if err := hooks.afterTemp(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("driverstate: install address work: %w", err)
	}
	if hooks.afterRename != nil {
		if err := hooks.afterRename(); err != nil {
			return err
		}
	}
	return syncAddressDir(filepath.Dir(target))
}

func syncAddressDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("driverstate: open address work directory: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("driverstate: sync address work directory: %w", err)
	}
	return nil
}

// ClaimReviewAddress imports the deterministic address child before linking it
// from the authoritative ledger. A retry adopts the same child and event.
func ClaimReviewAddress(dir string, lease Lease, stream, workID, actor string) (string, Event, error) {
	record, err := findAddressRecord(dir, lease.Run(), stream, workID)
	if err != nil {
		return "", Event{}, err
	}
	if record.ChildRun != "" {
		return record.ChildRun, Event{}, nil
	}
	childRun := addressChildRun(workID)
	childLease, err := Claim(dir, childRun, actor)
	if err != nil {
		return "", Event{}, err
	}
	repo, err := recordRepo(dir, lease.Run())
	if err != nil {
		return "", Event{}, err
	}
	importBody := dsc.RunImportedBody{
		Repo: repo, Source: record.WorkRef,
		GeneratedAt: workID, Manifest: json.RawMessage(`{"address_work":"` + workID + `"}`),
		Streams: []dsc.StreamSpec{{Stream: addressStream, DocPath: record.WorkRef}},
		Parent:  lease.Run(), ParentStream: stream, DoneBoundary: dsc.DoneBoundaryGreen,
	}
	_, importErr := appendLegacyEvent(dir, childLease, "", dsc.KindRunImported, actor, importBody)
	_ = childLease.Release()
	if importErr != nil {
		return "", Event{}, importErr
	}
	event, err := appendAddressEvent(dir, lease, stream, dsc.KindReviewAddressClaimed, dsc.ReviewAddressClaimedBody{
		WorkID: workID, ChildRun: childRun,
	})
	return childRun, event, err
}

// StartReviewAddress records the external Codex task id returned after spawn.
func StartReviewAddress(dir string, lease Lease, stream, workID, taskID string) (Event, error) {
	record, err := findAddressRecord(dir, lease.Run(), stream, workID)
	if err != nil {
		return Event{}, err
	}
	if record.Status != "claimed" && !(record.Status == "started" && record.TaskID == taskID) {
		return Event{}, refuse("invalid-lifecycle", "address work must be claimed before started", record.WorkRef)
	}
	return appendAddressEvent(dir, lease, stream, dsc.KindReviewAddressStarted, dsc.ReviewAddressStartedBody{
		WorkID: workID, TaskID: taskID,
	})
}

// CompleteReviewAddress records the exact new PR head produced by the child.
func CompleteReviewAddress(dir string, lease Lease, stream, workID, head string) (Event, error) {
	if !validAddressHex(head, 40) {
		return Event{}, refuse("malformed", "completed head must be 40 lowercase hexadecimal characters", "")
	}
	record, err := findAddressRecord(dir, lease.Run(), stream, workID)
	if err != nil {
		return Event{}, err
	}
	if strings.EqualFold(record.SourceHeadSHA, head) {
		return Event{}, refuse("unchanged-head", "address completion must record a new PR head", record.WorkRef)
	}
	if record.Status != "started" && !(record.Status == "completed" && strings.EqualFold(record.ResultHeadSHA, head)) {
		return Event{}, refuse("invalid-lifecycle", "address work must be started before completed", record.WorkRef)
	}
	return appendAddressEvent(dir, lease, stream, dsc.KindReviewAddressCompleted, dsc.ReviewAddressCompletedBody{
		WorkID: workID, HeadSHA: head,
	})
}

// ResumeReviewAddress reconstructs persisted work. A claimed work item without
// a recorded task id is ambiguous (spawn may have succeeded before a crash), so
// it parks and never tells a caller to auto-spawn another child.
func ResumeReviewAddress(dir, run, stream, workID string) (AddressResume, error) {
	if err := validateRunID(run); err != nil {
		return AddressResume{}, refuse("malformed", err.Error(), "")
	}
	if stream == "" || !validAddressWorkID(workID) {
		return AddressResume{}, refuse("malformed", "stream and canonical work id are required", "")
	}
	record, err := findAddressRecord(dir, run, stream, workID)
	if err != nil {
		return AddressResume{}, err
	}
	data, err := os.ReadFile(filepath.Join(runDir(dir, run), filepath.FromSlash(record.WorkRef)))
	if err != nil {
		return AddressResume{}, fmt.Errorf("driverstate: read address work: %w", err)
	}
	if sha256Hex(data) != record.WorkDigest {
		return AddressResume{}, refuse("work-digest-mismatch", "persisted work does not match the ledger digest", record.WorkRef)
	}
	work, err := reviewfindings.DecodeAddressWork(data)
	if err != nil {
		return AddressResume{}, refuse("malformed-work", err.Error(), record.WorkRef)
	}
	streamRecord := stateAddressStream(dir, run, stream)
	resume := AddressResume{
		Work: work, State: record, Branch: streamRecord.Branch,
		PR: streamRecord.PR, URL: streamRecord.URL,
	}
	if record.Status == "claimed" {
		resume.Parked = true
		return resume, AddressParked{WorkID: workID, Message: "child is claimed but no task id is recorded; reconcile the external task before retrying"}
	}
	return resume, nil
}

func stateAddressStream(dir, run, stream string) dsc.StreamRecord {
	state, err := Reduce(dir, run)
	if err != nil {
		return dsc.StreamRecord{}
	}
	return state.Streams[stream]
}

func findAddressRecord(dir, run, stream, workID string) (dsc.ReviewAddressRecord, error) {
	state, err := Reduce(dir, run)
	if err != nil {
		return dsc.ReviewAddressRecord{}, err
	}
	for _, record := range state.Streams[stream].ReviewAddresses {
		if record.WorkID == workID {
			return record, nil
		}
	}
	return dsc.ReviewAddressRecord{}, refuse("unknown-work", "work id is not prepared on this stream", "")
}

func recordRepo(dir, run string) (string, error) {
	state, err := Reduce(dir, run)
	if err != nil {
		return "", err
	}
	return state.Run.Repo, nil
}

func appendAddressEvent(dir string, lease Lease, stream string, kind dsc.Kind, body any) (Event, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Event{}, err
	}
	event := Event{
		ID:  deterministicEventID(lease.Run(), stream, kind, data),
		Run: lease.Run(), V: dsc.AddressVersion, Kind: kind, Stream: stream,
		Time: time.Now().UTC(), Actor: lease.Actor(), Body: data,
	}
	return Append(dir, lease, event)
}

func appendLegacyEvent(dir string, lease Lease, stream string, kind dsc.Kind, actor string, body any) (Event, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Event{}, err
	}
	event := Event{
		ID:  deterministicEventID(lease.Run(), stream, kind, data),
		Run: lease.Run(), V: dsc.Version, Kind: kind, Stream: stream,
		Time: time.Now().UTC(), Actor: actor, Body: data,
	}
	return Append(dir, lease, event)
}

func deterministicEventID(run, stream string, kind dsc.Kind, body []byte) string {
	sum := sha256.Sum256(bytes.Join([][]byte{[]byte(run), []byte(stream), []byte(kind), body}, []byte{0}))
	return "evt_" + hex.EncodeToString(sum[:16])
}

func addressChildRun(workID string) string {
	sum := sha256.Sum256([]byte("address-child\x00" + workID))
	return "dsr_" + hex.EncodeToString(sum[:16])
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validAddressHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validAddressWorkID(value string) bool {
	const prefix = "raw_"
	return strings.HasPrefix(value, prefix) && validAddressHex(value[len(prefix):], 32)
}
