package authorization

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

var testNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

func TestAuthorizeClaimVerifyAndRecordResult(t *testing.T) {
	fixture := newFixture(t)
	request := fixture.request(t)
	authorization := authorize(t, request)
	claimArtifact, claim, err := fixture.claim(t, authorization, fixture.live(), testNow.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	audit := fixture.audit(t)
	gotArtifact, gotClaim, err := VerifyDurableClaim(
		audit, claim.ClaimID, fixture.live(), testNow.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotArtifact.ID != claimArtifact.ID || gotClaim.ClaimID != claim.ClaimID {
		t.Fatalf("durable claim mismatch: %+v %+v", gotArtifact, gotClaim)
	}
	result := gateauthorization.ExecutionResult{
		SchemaVersion: gateauthorization.SchemaVersion,
		ExecutionID:   claim.ExecutionID, ClaimID: claim.ClaimID,
		Outcome:     gateauthorization.ExecutionMerged,
		MergeArgv:   append([]string(nil), claim.MergeArgv...),
		CompletedAt: testNow.Add(5 * time.Minute),
		MergeCommit: strings.Repeat("e", 40),
	}
	if _, err := RecordResult(fixture.store, claimArtifact, claim, result); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyDurableClaim(
		fixture.audit(t), claim.ClaimID, fixture.live(), testNow.Add(6*time.Minute),
	); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("verify after result = %v, want already claimed", err)
	}
	if _, err := RecordResult(fixture.store, claimArtifact, claim, result); !errors.Is(err, ErrResultDuplicate) {
		t.Fatalf("second result = %v, want duplicate", err)
	}
}

func TestPreflightClaimRefusesWithoutMutatingState(t *testing.T) {
	fixture := newFixture(t)
	authorization := authorize(t, fixture.request(t))
	before := fixture.audit(t)
	if err := PreflightClaim(
		before, authorization, fixture.live(), testNow.Add(3*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	after := fixture.audit(t)
	if len(after.All) != len(before.All) {
		t.Fatal("preflight mutated state")
	}
	moved := fixture.live()
	moved.HeadSHA = strings.Repeat("f", 40)
	if err := PreflightClaim(
		after, authorization, moved, testNow.Add(3*time.Minute),
	); !errors.Is(err, ErrLiveMismatch) {
		t.Fatalf("moved-head preflight = %v, want live mismatch", err)
	}
}

func TestBootstrapMergeArgvUsesNewestExactAction(t *testing.T) {
	fixture := newFixture(t)
	got, err := BootstrapMergeArgv(
		fixture.audit(t), fixture.action.ID, fixture.subject,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := gateauthorization.ExpectedMergeArgv(fixture.subject)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	got[0] = "changed"
	again, err := BootstrapMergeArgv(
		fixture.audit(t), fixture.action.ID, fixture.subject,
	)
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != "gh" {
		t.Fatal("returned argv aliases stored action")
	}
	if _, err := fixture.store.Append(
		state.KindEscalation, fixture.action.Run, nil, map[string]string{"reason": "newer"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapMergeArgv(
		fixture.audit(t), fixture.action.ID, fixture.subject,
	); err == nil {
		t.Fatal("superseded bootstrap action accepted")
	}
}

func TestValidateRepositoryScope(t *testing.T) {
	audit := state.AuditResult{
		OK: true,
		All: []state.Artifact{
			{
				ID:   "grt_one",
				Body: json.RawMessage(`{"repo":"o/r"}`),
			},
			{
				ID:   "vrd_one",
				Body: json.RawMessage(`{"subject":{"repo":"o/r"},"data":"{\"repo\":\"other/r\"}"}`),
			},
		},
	}
	if err := ValidateRepositoryScope(audit, "o/r"); err != nil {
		t.Fatal(err)
	}
	audit.All = append(audit.All, state.Artifact{
		ID:   "grt_other",
		Body: json.RawMessage(`{"repo":"other/r"}`),
	})
	if err := ValidateRepositoryScope(audit, "o/r"); !errors.Is(err, ErrRepositoryScope) {
		t.Fatalf("cross-repository ledger = %v, want scope refusal", err)
	}
}

func TestClaimIsPermanentAndAtomic(t *testing.T) {
	fixture := newFixture(t)
	authorization := authorize(t, fixture.request(t))
	if _, _, err := fixture.claim(t, authorization, fixture.live(), testNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.claim(t, authorization, fixture.live(), testNow.Add(4*time.Minute)); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("duplicate claim = %v, want already claimed", err)
	}
	subject := contracts.Subject{
		Repo: fixture.subject.Repo, Number: fixture.subject.Number,
		HeadSHA: fixture.subject.HeadSHA,
	}
	open, err := SubjectHasOpenClaim(fixture.audit(t), subject)
	if err != nil {
		t.Fatal(err)
	}
	if !open {
		t.Fatal("claim must remain open until a terminal result")
	}
}

func TestVerifyExpiredClaimClosesOnlyOneExpiredOpenClaim(t *testing.T) {
	fixture := newFixture(t)
	authorization := authorize(t, fixture.request(t))
	claimArtifact, claim, err := fixture.claim(
		t, authorization, fixture.live(), testNow.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyExpiredClaim(
		fixture.audit(t), claim.ClaimID, testNow.Add(19*time.Minute),
	); !errors.Is(err, ErrClaimNotExpired) {
		t.Fatalf("active claim = %v, want not expired", err)
	}
	gotArtifact, gotClaim, err := VerifyExpiredClaim(
		fixture.audit(t), claim.ClaimID, claim.ExpiresAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotArtifact.ID != claimArtifact.ID || gotClaim.ClaimID != claim.ClaimID {
		t.Fatalf("expired claim mismatch: %+v %+v", gotArtifact, gotClaim)
	}
	result := gateauthorization.ExecutionResult{
		SchemaVersion: gateauthorization.SchemaVersion,
		ExecutionID:   claim.ExecutionID,
		ClaimID:       claim.ClaimID,
		Outcome:       gateauthorization.ExecutionFailed,
		MergeArgv:     append([]string(nil), claim.MergeArgv...),
		CompletedAt:   claim.ExpiresAt,
		ErrorCode:     "executor_claim_expired",
	}
	if _, err := RecordResult(fixture.store, claimArtifact, claim, result); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyExpiredClaim(
		fixture.audit(t), claim.ClaimID, claim.ExpiresAt,
	); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("resolved claim = %v, want already claimed", err)
	}
}

func TestVerifyExpiredClaimRefusesMissingAndMalformedClaims(t *testing.T) {
	fixture := newFixture(t)
	claimID := "gxc_" + strings.Repeat("0", 64)
	if _, _, err := VerifyExpiredClaim(
		fixture.audit(t), claimID, testNow,
	); !errors.Is(err, ErrActionMissing) {
		t.Fatalf("missing claim = %v, want action missing", err)
	}
	if _, err := fixture.store.Append(
		state.KindExecutionClaim, fixture.action.Run,
		[]string{fixture.action.ID}, map[string]string{"claim_id": claimID},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyExpiredClaim(
		fixture.audit(t), claimID, testNow,
	); err == nil || !strings.Contains(err.Error(), "authorization_claim_malformed") {
		t.Fatalf("malformed claim = %v", err)
	}
}

func TestConcurrentClaimHasExactlyOneWinner(t *testing.T) {
	fixture := newFixture(t)
	authorization := authorize(t, fixture.request(t))
	grantID := fixture.mintGrant(t, authorization)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := Claim(
				fixture.store, fixture.keyPath, authorization, fixture.live(),
				grantID, testNow.Add(3*time.Minute),
			)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	successes, duplicates := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyClaimed):
			duplicates++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("successes=%d duplicates=%d, want 1/1", successes, duplicates)
	}
}

func TestAuthorizeRequiresRunBoundIndependentLatestApproval(t *testing.T) {
	fixture := newFixture(t)
	request := fixture.request(t)
	id, err := gateauthorization.AuthorizationID(request)
	if err != nil {
		t.Fatal(err)
	}
	comment := gateauthorization.ExpectedApprovalComment(id, request)
	valid := ApprovalReview{
		WorkflowRunID: 1234, ActorLogin: "gate-approver", ActorID: 77,
		State: gateauthorization.ApprovalStateApproved, Comment: comment,
		Environments: []string{gateauthorization.DefaultEnvironment},
	}
	tests := map[string]struct {
		facts ApprovalFacts
		want  error
	}{
		"missing": {
			facts: ApprovalFacts{WorkflowRunID: 1234, WorkflowActorID: 12, TriggeringActorID: 13, ObservedAt: testNow.Add(2 * time.Minute)},
			want:  ErrApprovalMissing,
		},
		"wrong run": {
			facts: ApprovalFacts{
				WorkflowRunID: 9999, WorkflowActorID: 12, TriggeringActorID: 13, ObservedAt: testNow.Add(2 * time.Minute),
				Reviews: []ApprovalReview{valid},
			},
			want: ErrApprovalMissing,
		},
		"self approval": {
			facts: ApprovalFacts{
				WorkflowRunID: 1234, WorkflowActorID: 77, TriggeringActorID: 13, ObservedAt: testNow.Add(2 * time.Minute),
				Reviews: []ApprovalReview{valid},
			},
			want: ErrApprovalDependent,
		},
		"rerun actor approval": {
			facts: ApprovalFacts{
				WorkflowRunID: 1234, WorkflowActorID: 12, TriggeringActorID: 77, ObservedAt: testNow.Add(2 * time.Minute),
				Reviews: []ApprovalReview{valid},
			},
			want: ErrApprovalDependent,
		},
		"wrong comment": {
			facts: ApprovalFacts{
				WorkflowRunID: 1234, WorkflowActorID: 12, TriggeringActorID: 13, ObservedAt: testNow.Add(2 * time.Minute),
				Reviews: []ApprovalReview{func() ApprovalReview {
					review := valid
					review.Comment = "approve"
					return review
				}()},
			},
			want: ErrApprovalMismatch,
		},
		"later rejection": {
			facts: ApprovalFacts{
				WorkflowRunID: 1234, WorkflowActorID: 12, TriggeringActorID: 13, ObservedAt: testNow.Add(2 * time.Minute),
				Reviews: []ApprovalReview{
					valid,
					{
						WorkflowRunID: 1234, ActorLogin: "gate-approver", ActorID: 77,
						State: "rejected", Comment: "changed my mind",
						Environments: []string{gateauthorization.DefaultEnvironment},
					},
				},
			},
			want: ErrApprovalMismatch,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Authorize(request, test.facts); !errors.Is(err, test.want) {
				t.Fatalf("Authorize() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClaimRefusesLiveDriftAndAmbiguousHead(t *testing.T) {
	tests := map[string]func(*LivePullRequest){
		"wrong repo":       func(l *LivePullRequest) { l.Repository = "o/other" },
		"wrong pr":         func(l *LivePullRequest) { l.Number++ },
		"closed":           func(l *LivePullRequest) { l.State = "closed" },
		"moved head":       func(l *LivePullRequest) { l.HeadSHA = strings.Repeat("f", 40) },
		"retargeted":       func(l *LivePullRequest) { l.BaseRef = "release" },
		"base moved":       func(l *LivePullRequest) { l.BaseSHA = strings.Repeat("f", 40) },
		"mergebase moved":  func(l *LivePullRequest) { l.MergeBaseSHA = strings.Repeat("f", 40) },
		"shared head":      func(l *LivePullRequest) { l.OpenPRsForHead = []int{168, 169} },
		"association lost": func(l *LivePullRequest) { l.OpenPRsForHead = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newFixture(t)
			authorization := authorize(t, fixture.request(t))
			live := fixture.live()
			mutate(&live)
			_, _, err := fixture.claim(t, authorization, live, testNow.Add(3*time.Minute))
			want := ErrLiveMismatch
			if name == "shared head" || name == "association lost" {
				want = ErrHeadAmbiguous
			}
			if !errors.Is(err, want) {
				t.Fatalf("Claim() = %v, want %v", err, want)
			}
		})
	}
}

func TestBuildRequestAndClaimRefuseSupersededAction(t *testing.T) {
	fixture := newFixture(t)
	oldRequest := fixture.request(t)
	oldAuthorization := authorize(t, oldRequest)

	evd, err := fixture.store.Append(state.KindEvidence, "run_new", nil, map[string]string{"diff": "new"})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := fixture.store.Append(state.KindVerdict, "run_new", []string{evd.ID}, contracts.Verdict{
		Subject: contracts.Subject{
			Repo: fixture.subject.Repo, Number: fixture.subject.Number,
			HeadSHA: fixture.subject.HeadSHA,
		},
		Source: "test", Producer: contracts.Producer{Class: contracts.ClassCode},
		Decision: contracts.DecisionBlock, Tier: "T1", Confidence: 1, Why: "blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Append(state.KindAction, "run_new", []string{verdict.ID}, map[string]any{
		"outcome": "blocked", "verdict": verdict.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRequest(fixture.audit(t), RequestInput{
		ActionID: fixture.action.ID, Subject: fixture.subject,
		JudgmentQuestion: "May Gate execute this exact merge action?",
		ReplayID:         "evt_" + strings.Repeat("8", 32),
		IssuedAt:         testNow, ExpiresAt: testNow.Add(20 * time.Minute),
	}); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("BuildRequest() = %v, want superseded", err)
	}
	if _, _, err := fixture.claim(
		t, oldAuthorization, fixture.live(), testNow.Add(3*time.Minute),
	); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("Claim() = %v, want superseded", err)
	}
}

// TestBuildRequestAndClaimRefuseAfterMergedRefusal pins the terminal-indexing
// fallback: an already_merged refusal is recorded before any verdict exists, so
// its run carries no verdict to derive a subject from — the action body carries
// repo/number itself. It must still supersede an older would_merge for the same
// PR, or a stale pass could be sent for protected approval after the merge
// already happened.
func TestBuildRequestAndClaimRefuseAfterMergedRefusal(t *testing.T) {
	fixture := newFixture(t)
	oldRequest := fixture.request(t)
	oldAuthorization := authorize(t, oldRequest)

	evd, err := fixture.store.Append(state.KindEvidence, "run_merged", nil, map[string]string{"view": "state=MERGED"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Append(state.KindAction, "run_merged", []string{evd.ID}, map[string]any{
		"outcome": "already_merged",
		"repo":    fixture.subject.Repo, "number": fixture.subject.Number,
		"merge_commit": strings.Repeat("d", 40),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRequest(fixture.audit(t), RequestInput{
		ActionID: fixture.action.ID, Subject: fixture.subject,
		JudgmentQuestion: "May Gate execute this exact merge action?",
		ReplayID:         "evt_" + strings.Repeat("9", 32),
		IssuedAt:         testNow, ExpiresAt: testNow.Add(20 * time.Minute),
	}); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("BuildRequest() = %v, want superseded by the merged refusal", err)
	}
	if _, _, err := fixture.claim(
		t, oldAuthorization, fixture.live(), testNow.Add(3*time.Minute),
	); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("Claim() = %v, want superseded by the merged refusal", err)
	}
}

func TestBuildRequestRefusesForgedArgv(t *testing.T) {
	fixture := newFixtureWithArgv(t, []string{
		"gh", "pr", "merge", "168", "-R", "itsHabib/workbench",
		"--squash", "--match-head-commit", strings.Repeat("f", 40),
	})
	if _, err := BuildRequest(fixture.audit(t), fixture.requestInput()); !errors.Is(err, ErrActionMismatch) {
		t.Fatalf("BuildRequest() = %v, want action mismatch", err)
	}
}

type fixture struct {
	store   *state.Store
	keyPath string
	action  state.Artifact
	subject gateauthorization.Subject
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	subject := gateauthorization.Subject{
		Repo: "itsHabib/workbench", Number: 168,
		HeadSHA: strings.Repeat("b", 40), BaseRef: "main",
		BaseSHA: strings.Repeat("c", 40), MergeBaseSHA: strings.Repeat("d", 40),
	}
	return newFixtureWithArgv(t, gateauthorization.ExpectedMergeArgv(subject))
}

func newFixtureWithArgv(t *testing.T, argv []string) fixture {
	t.Helper()
	root := t.TempDir()
	st, err := state.Open(filepath.Join(root, "state"), func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	subject := gateauthorization.Subject{
		Repo: "itsHabib/workbench", Number: 168,
		HeadSHA: strings.Repeat("b", 40), BaseRef: "main",
		BaseSHA: strings.Repeat("c", 40), MergeBaseSHA: strings.Repeat("d", 40),
	}
	run := "run_0123456789abcdef"
	evd, err := st.Append(state.KindEvidence, run, nil, map[string]string{"diff": "exact"})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := st.Append(state.KindVerdict, run, []string{evd.ID}, contracts.Verdict{
		Subject: contracts.Subject{
			Repo: subject.Repo, Number: subject.Number, HeadSHA: subject.HeadSHA,
		},
		Source: "test", Producer: contracts.Producer{Class: contracts.ClassCode},
		Decision: contracts.DecisionPass, Tier: "T1", Confidence: 1, Why: "clean",
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := st.Append(state.KindGrant, "run_mint", nil, map[string]string{"scope": "test"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := st.Append(state.KindAction, run, []string{verdict.ID, grant.ID}, actionBody{
		Outcome: gateauthorization.OutcomeWouldMerge,
		Verdict: verdict.ID, Grant: grant.ID,
		Command: join(argv), Argv: argv,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		store: st, keyPath: filepath.Join(root, "keys", "grant.key"),
		action: action, subject: subject,
	}
}

func (f fixture) requestInput() RequestInput {
	return RequestInput{
		ActionID: f.action.ID, Subject: f.subject,
		JudgmentQuestion: "May Gate execute this exact merge action?",
		ReplayID:         "evt_" + strings.Repeat("8", 32),
		IssuedAt:         testNow, ExpiresAt: testNow.Add(20 * time.Minute),
	}
}

func (f fixture) request(t *testing.T) gateauthorization.Request {
	t.Helper()
	request, err := BuildRequest(f.audit(t), f.requestInput())
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (f fixture) live() LivePullRequest {
	return LivePullRequest{
		Repository: f.subject.Repo, Number: f.subject.Number, State: "open",
		HeadSHA: f.subject.HeadSHA, BaseRef: f.subject.BaseRef,
		BaseSHA: f.subject.BaseSHA, MergeBaseSHA: f.subject.MergeBaseSHA,
		OpenPRsForHead: []int{f.subject.Number},
	}
}

func (f fixture) claim(t *testing.T, authorization gateauthorization.Artifact, live LivePullRequest, now time.Time) (state.Artifact, gateauthorization.ExecutionClaim, error) {
	t.Helper()
	grantID := f.mintGrant(t, authorization)
	return Claim(f.store, f.keyPath, authorization, live, grantID, now)
}

func (f fixture) mintGrant(t *testing.T, authorization gateauthorization.Artifact) string {
	t.Helper()
	grant, err := capability.MintBound(
		f.store, f.keyPath, f.subject.Repo, "merge", "T3", 1,
		"gh:gate-approver via environment:1234", 20*time.Minute,
		f.subject.HeadSHA, f.subject.Number, authorization.AuthorizationID,
		func() time.Time { return testNow.Add(2 * time.Minute) },
	)
	if err != nil {
		t.Fatal(err)
	}
	return grant.ID
}

func (f fixture) audit(t *testing.T) state.AuditResult {
	t.Helper()
	audit, err := f.store.Audit()
	if err != nil {
		t.Fatal(err)
	}
	return audit
}

func authorize(t *testing.T, request gateauthorization.Request) gateauthorization.Artifact {
	t.Helper()
	id, err := gateauthorization.AuthorizationID(request)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := Authorize(request, ApprovalFacts{
		WorkflowRunID: 1234, WorkflowActorID: 12, TriggeringActorID: 13,
		ObservedAt: testNow.Add(2 * time.Minute),
		Reviews: []ApprovalReview{{
			WorkflowRunID: 1234, ActorLogin: "gate-approver", ActorID: 77,
			State:        gateauthorization.ApprovalStateApproved,
			Comment:      gateauthorization.ExpectedApprovalComment(id, request),
			Environments: []string{gateauthorization.DefaultEnvironment},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestExecutionClaimJSONContainsApprovalReceipt(t *testing.T) {
	fixture := newFixture(t)
	authorization := authorize(t, fixture.request(t))
	_, claim, err := fixture.claim(t, authorization, fixture.live(), testNow.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), authorization.Receipt.ReceiptDigest) ||
		!strings.Contains(string(data), "gate-approver") {
		t.Fatal("durable claim must carry the verified approval receipt")
	}
}
