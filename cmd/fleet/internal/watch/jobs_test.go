package watch

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/jobs"
)

func TestMaxBudgetValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		budget   any
		wantErr  bool
	}{
		{name: "claude positive", provider: "claude", budget: 1.25},
		{name: "zero", provider: "claude", budget: 0.0, wantErr: true},
		{name: "codex unsupported", provider: "codex", budget: 1.0, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets := parseDeliverTargets(fleet.Rec{"hub:lead": fleet.Rec{"cwd": "/tmp/seat", "provider": tc.provider, "max_budget_usd": tc.budget}})
			if got := targets[0].configError != ""; got != tc.wantErr {
				t.Fatalf("config error=%q", targets[0].configError)
			}
		})
	}
}

func configureJobs(t *testing.T, home, state string, fields fleet.Rec) {
	t.Helper()
	binding := fleet.Rec{"state": state}
	for key, value := range fields {
		binding[key] = value
	}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), fleet.Rec{"hub:lead": fleet.Rec{"cwd": home, "provider": "codex", "jobs": binding}}); err != nil {
		t.Fatal(err)
	}
}

func submitJob(t *testing.T, state, id, brief string) {
	t.Helper()
	if _, err := (&jobs.Store{Dir: state}).Execute(jobs.Request{Op: "submit", ID: id, Brief: brief}); err != nil {
		t.Fatal(err)
	}
}

func TestJobBindingEmptyQueueDoesNotStartProvider(t *testing.T) {
	home, _ := deliverEnv(t)
	state := filepath.Join(t.TempDir(), "jobs")
	configureJobs(t, home, state, nil)
	called := false
	original := providerCommand
	providerCommand = func(string, map[string]any) (*exec.Cmd, error) {
		called = true
		return nil, errors.New("unexpected provider start")
	}
	t.Cleanup(func() { providerCommand = original })
	if observed := deliver(fleet.Now()); len(observedWhat(observed, "mail-delivery-started")) != 0 {
		t.Fatalf("unexpected launch: %v", observed)
	}
	if called {
		t.Fatal("empty queue prepared a provider command")
	}
}

func TestJobClaimPopulatesProviderRequestAndPrompt(t *testing.T) {
	home, _ := deliverEnv(t)
	state := filepath.Join(t.TempDir(), "jobs")
	submitJob(t, state, "job-1", "repair the failing build")
	configureJobs(t, home, state, fleet.Rec{"ttl_seconds": 90})
	var request map[string]any
	original := providerCommand
	providerCommand = func(_ string, got map[string]any) (*exec.Cmd, error) {
		request = got
		return nil, errors.New("fixture stops before start")
	}
	t.Cleanup(func() { providerCommand = original })
	_ = deliver(fleet.Now())
	job := request["job"].(map[string]any)
	if job["state"] != state || job["id"] != "job-1" || job["worker"] != "hub:lead" || job["ttl_seconds"] != 90 || job["token"] == "" || job["executable"] == "" {
		t.Fatalf("job request: %#v", job)
	}
	if prompt := request["prompt"].(string); !strings.Contains(prompt, "job-1") || !strings.Contains(prompt, "repair the failing build") {
		t.Fatalf("job prompt: %q", prompt)
	}
	launch := fleet.ReadJSON(launchPath(deliverTarget{cwd: home}))
	if fleet.S(launch, "job_retry_key") == "" || fleet.S(fleet.M(launch, "job"), "id") != "job-1" {
		t.Fatalf("launch did not retain job identity: %v", launch)
	}
}

func TestJobClaimConflictDoesNotStartProvider(t *testing.T) {
	home, _ := deliverEnv(t)
	state := filepath.Join(t.TempDir(), "jobs")
	submitJob(t, state, "occupied", "already running")
	store := &jobs.Store{Dir: state}
	if _, err := store.Execute(jobs.Request{Op: "claim", Worker: "hub:lead", Key: "other", TTLSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	submitJob(t, state, "waiting", "must wait")
	configureJobs(t, home, state, fleet.Rec{"id": "waiting"})
	called := false
	original := providerCommand
	providerCommand = func(string, map[string]any) (*exec.Cmd, error) { called = true; return nil, nil }
	t.Cleanup(func() { providerCommand = original })
	_ = deliver(fleet.Now())
	if called {
		t.Fatal("claim conflict started provider")
	}
}

func TestClaimingRecordReplaysDurableClaim(t *testing.T) {
	home, _ := deliverEnv(t)
	state := filepath.Join(t.TempDir(), "jobs")
	submitJob(t, state, "job-1", "resume claim")
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex", job: &jobBinding{state: state, ttlSeconds: 300}}
	key := "durable-key"
	result, err := (&jobs.Store{Dir: state}).Execute(jobs.Request{Op: "claim", Worker: target.address, Key: key, TTLSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	claimed := result.(jobs.Job)
	if err := fleet.WriteJSON(launchPath(target), fleet.Rec{"at": fleet.Now(), "status": "claiming", "job_retry_key": key, "address": target.address, "cwd": home, "provider": "codex"}); err != nil {
		t.Fatal(err)
	}
	launch := fleet.Rec{"at": fleet.Now(), "status": "claiming", "address": target.address, "cwd": home, "provider": "codex"}
	replayed, err := claimJob(target, fleet.ReadJSON(launchPath(target)), launch, launchPath(target))
	if err != nil || replayed.ID != claimed.ID || replayed.Attempts[len(replayed.Attempts)-1].Token != claimed.Attempts[len(claimed.Attempts)-1].Token {
		t.Fatalf("replay: %#v %v", replayed, err)
	}
}

func TestDifferentJobDoesNotReuseProviderSession(t *testing.T) {
	target := deliverTarget{provider: "codex"}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	last := fleet.Rec{"status": "running", "provider": "codex", "resume": "old-session", "work_identity": workIdentity(target), "job": fleet.Rec{"id": "old"}, "attempt": "attempt-1", "state_file": stateFile}
	if err := fleet.WriteJSON(stateFile, fleet.Rec{"attempt": "attempt-1", "provider": "codex", "provider_session": "old-session", "provider_terminal": true}); err != nil {
		t.Fatal(err)
	}
	job := &jobs.Job{ID: "new"}
	if got, err := resumeSessionForJob(target, last, job); err != nil || got != "" {
		t.Fatalf("different job resumed %q: %v", got, err)
	}
	job.ID = "old"
	if got, err := resumeSessionForJob(target, last, job); err != nil || got != "old-session" {
		t.Fatalf("same job did not resume %q: %v", got, err)
	}
}
