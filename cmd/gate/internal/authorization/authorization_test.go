package authorization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

func TestExportNewestWouldMerge(t *testing.T) {
	audit := validAudit()
	artifact, err := Export(audit, "run_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Gate.ActionID != "act_0123456789abcdef" ||
		artifact.AuthorizationID != "gau_"+strings.Repeat("a", 64) {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
}

func TestExportRefusesUnsafeState(t *testing.T) {
	tests := map[string]func(*state.AuditResult){
		"tampered": func(a *state.AuditResult) {
			a.OK = false
			a.Reason = "rewrite"
		},
		"forged argv": func(a *state.AuditResult) {
			var body actionBody
			mustUnmarshal(t, a.All[1].Body, &body)
			body.Argv[9] = strings.Repeat("f", 40)
			a.All[1].Body = mustJSON(t, body)
		},
		"lineage": func(a *state.AuditResult) {
			a.All[1].Parents[0] = "vrd_other"
		},
		"parked": func(a *state.AuditResult) {
			a.All[1].Kind = state.KindEscalation
		},
		"superseded": func(a *state.AuditResult) {
			subject := contracts.Subject{
				Repo: "itsHabib/workbench", Number: 168,
				HeadSHA: strings.Repeat("b", 40),
			}
			a.All = append(a.All,
				artifact("vrd_2222222222222222", state.KindVerdict, "run_2222222222222222",
					contracts.Verdict{Subject: subject}),
				artifact("act_2222222222222222", state.KindAction, "run_2222222222222222", actionBody{Outcome: "blocked"}),
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			audit := validAudit()
			mutate(&audit)
			if _, err := Export(audit, "run_0123456789abcdef"); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestCheckPromotionValidAndDuplicate(t *testing.T) {
	artifact := validAuthorization()
	facts := validPromotionFacts(artifact)
	promotion, err := CheckPromotion(artifact, facts)
	if err != nil || promotion.Duplicate {
		t.Fatalf("promotion=%+v err=%v", promotion, err)
	}
	facts.Statuses = []Status{{
		Context: "gate", State: "success", Body: promotion.Description,
		Creator: Actor{Login: "github-actions[bot]", Type: "Bot"},
	}}
	promotion, err = CheckPromotion(artifact, facts)
	if err != nil || !promotion.Duplicate {
		t.Fatalf("duplicate promotion=%+v err=%v", promotion, err)
	}
}

func TestCheckPromotionRefusesUnsafeFacts(t *testing.T) {
	tests := map[string]func(*PromotionFacts){
		"expired":              func(f *PromotionFacts) { f.Now = validAuthorization().ExpiresAt },
		"wrong repo":           func(f *PromotionFacts) { f.Repository = "itsHabib/other" },
		"untrusted ref":        func(f *PromotionFacts) { f.WorkflowRef = "refs/heads/codex/attack" },
		"admin bypass unknown": func(f *PromotionFacts) { f.Environment.AdminBypassKnown = false },
		"admin bypass":         func(f *PromotionFacts) { f.Environment.CanAdminsBypass = true },
		"no reviewer":          func(f *PromotionFacts) { f.Environment.RequiredReviewers = 0 },
		"self review":          func(f *PromotionFacts) { f.Environment.PreventSelfReview = false },
		"open state":           func(f *PromotionFacts) { f.PullRequest.State = "closed" },
		"stale head":           func(f *PromotionFacts) { f.PullRequest.HeadSHA = strings.Repeat("f", 40) },
		"shared head":          func(f *PromotionFacts) { f.OpenPRsForHead = []int{168, 169} },
		"query absence":        func(f *PromotionFacts) { f.OpenPRsForHead = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := validAuthorization()
			facts := validPromotionFacts(artifact)
			mutate(&facts)
			if _, err := CheckPromotion(artifact, facts); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func validAudit() state.AuditResult {
	subject := contracts.Subject{
		Repo: "itsHabib/workbench", Number: 168,
		HeadSHA: strings.Repeat("b", 40),
	}
	argv := gateauthorization.ExpectedMergeArgv(gateauthorization.Subject(subject))
	verdict := artifact("vrd_0123456789abcdef", state.KindVerdict, "run_0123456789abcdef",
		contracts.Verdict{Subject: subject})
	action := artifact("act_0123456789abcdef", state.KindAction, "run_0123456789abcdef",
		actionBody{
			Outcome: gateauthorization.OutcomeWouldMerge,
			Verdict: verdict.ID, Grant: "grt_testfixture",
			Command: strings.Join(argv, " "), Argv: argv,
		})
	action.Parents = []string{verdict.ID, "grt_testfixture"}
	action.Hash = strings.Repeat("a", 64)
	return state.AuditResult{OK: true, All: []state.Artifact{verdict, action}}
}

func validAuthorization() gateauthorization.Artifact {
	artifact, err := Export(validAudit(), "run_0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return artifact
}

func validPromotionFacts(artifact gateauthorization.Artifact) PromotionFacts {
	return PromotionFacts{
		Now:           artifact.IssuedAt.Add(time.Minute),
		Repository:    artifact.Subject.Repo,
		DefaultBranch: "main", WorkflowRef: "refs/heads/main",
		WorkflowEvent: "workflow_dispatch", GitHubActions: true,
		Environment: Environment{
			Name: gateauthorization.DefaultEnvironment, AdminBypassKnown: true,
			ProtectedBranchesOnly: true,
			RequiredReviewers:     1, PreventSelfReview: true,
		},
		PullRequest: PullRequest{
			Number: artifact.Subject.Number, State: "open", HeadSHA: artifact.Subject.HeadSHA,
		},
		OpenPRsForHead: []int{artifact.Subject.Number},
	}
}

func artifact(id, kind, run string, body any) state.Artifact {
	return state.Artifact{
		ID: id, Kind: kind, Run: run,
		Time: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Body: mustJSON(nil, body), Hash: strings.Repeat("c", 64),
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}
	return data
}

func mustUnmarshal(t *testing.T, data []byte, value any) {
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
