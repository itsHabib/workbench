package watch

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestJobTTLRequiresExactPositiveInteger(t *testing.T) {
	state := t.TempDir()
	for _, value := range []any{0.0, 1.5, "90"} {
		binding, message := parseJobBinding(fleet.Rec{"state": state, "ttl_seconds": value})
		if binding != nil || message == "" {
			t.Fatalf("accepted ttl %#v: %#v %q", value, binding, message)
		}
	}
	binding, message := parseJobBinding(fleet.Rec{"state": state})
	if message != "" || binding.ttlSeconds != 300 {
		t.Fatalf("default ttl: %#v %q", binding, message)
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
	for range 3 {
		if observed := deliver(fleet.Now()); len(observedWhat(observed, "mail-delivery-started")) != 0 {
			t.Fatalf("unexpected launch: %v", observed)
		}
	}
	if called {
		t.Fatal("empty queue prepared a provider command")
	}
	for _, pattern := range []string{"*.log", "*.meta.json", "*.request.json"} {
		matches, err := filepath.Glob(filepath.Join(fleet.Path("watch", "delivery"), pattern))
		if err != nil || len(matches) != 0 {
			t.Fatalf("empty polls left %s artifacts: %v %v", pattern, matches, err)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "jobs.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty polls mutated job state: %v", err)
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
	if err := fleet.WriteJSON(launchPath(target), fleet.Rec{"at": fleet.Now(), "status": "claiming", "job_retry_key": key, "address": target.address, "cwd": home, "provider": "codex", "job": fleet.Rec{"state": state, "id": "", "worker": target.address, "ttl_seconds": 300}}); err != nil {
		t.Fatal(err)
	}
	launch := fleet.Rec{"at": fleet.Now(), "status": "claiming", "address": target.address, "cwd": home, "provider": "codex"}
	replayed, err := claimJob(target, fleet.ReadJSON(launchPath(target)), launch, launchPath(target))
	if err != nil || replayed.ID != claimed.ID || replayed.Attempts[len(replayed.Attempts)-1].Token != claimed.Attempts[len(claimed.Attempts)-1].Token {
		t.Fatalf("replay: %#v %v", replayed, err)
	}
}

func TestClaimingRecordRejectsChangedJobBinding(t *testing.T) {
	target := deliverTarget{address: "hub:lead", cwd: t.TempDir(), provider: "codex", job: &jobBinding{state: filepath.Join(t.TempDir(), "new"), ttlSeconds: 300}}
	last := fleet.Rec{"at": fleet.Now(), "status": "claiming", "job_retry_key": "key", "job": fleet.Rec{"state": filepath.Join(t.TempDir(), "old"), "worker": target.address, "ttl_seconds": 300}}
	_, err := claimJob(target, last, fleet.Rec{"at": fleet.Now(), "status": "claiming"}, launchPath(target))
	if err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("changed binding accepted: %v", err)
	}
}

func TestExpiredFirstClaimGetsNewKeyAndToken(t *testing.T) {
	state := filepath.Join(t.TempDir(), "jobs")
	submitJob(t, state, "job-1", "recover first claim")
	target := deliverTarget{address: "hub:lead", cwd: t.TempDir(), provider: "codex", job: &jobBinding{state: state, id: "job-1", ttlSeconds: 300}}
	store := &jobs.Store{Dir: state}
	result, err := store.Execute(jobs.Request{Op: "claim", ID: "job-1", Worker: target.address, Key: "crashed-first-key", TTLSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	first := result.(jobs.Job).Attempts[0]
	expireStoredClaim(t, state)
	last := fleet.Rec{"at": fleet.Now(), "status": "claiming", "job_retry_key": first.Key, "job": fleet.Rec{"state": state, "id": "job-1", "worker": target.address, "ttl_seconds": 300}}
	path := launchPath(target)
	if err := fleet.WriteJSON(path, last); err != nil {
		t.Fatal(err)
	}
	launch := fleet.Rec{"at": fleet.Now(), "status": "claiming", "address": target.address, "cwd": target.cwd, "provider": target.provider}
	if _, err := claimJob(target, last, launch, path); !errors.Is(err, errNoEligibleJob) {
		t.Fatalf("expired replay: %v", err)
	}
	failed := fleet.ReadJSON(path)
	if fleet.S(failed, "status") != "failed" {
		t.Fatalf("expired first claim stayed unresolved: %v", failed)
	}
	second, err := claimJob(target, failed, launch, path)
	if err != nil {
		t.Fatal(err)
	}
	latest := second.Attempts[len(second.Attempts)-1]
	if latest.Key == first.Key || latest.Token == first.Token {
		t.Fatalf("claim identity reused: first=%v latest=%v", first, latest)
	}
}

func expireStoredClaim(t *testing.T, state string) {
	t.Helper()
	path := filepath.Join(state, "jobs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := struct {
		Version int        `json:"version"`
		Jobs    []jobs.Job `json:"jobs"`
	}{}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot.Jobs[0].Attempts[0].StartedAt = started
	snapshot.Jobs[0].Attempts[0].ExpiresAt = started.Add(5 * time.Minute)
	if err := fleet.WriteJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestDifferentJobDoesNotReuseProviderSession(t *testing.T) {
	target := deliverTarget{provider: "codex", job: &jobBinding{state: "/tmp/jobs"}}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	last := fleet.Rec{"status": "running", "provider": "codex", "resume": "old-session", "work_identity": workIdentity(target), "job": fleet.Rec{"id": "old", "state": "/tmp/jobs"}, "attempt": "attempt-1", "state_file": stateFile}
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
	target.job = &jobBinding{state: "/other/jobs"}
	if got, err := resumeSessionForJob(target, last, job); err != nil || got != "" {
		t.Fatalf("different queue resumed %q: %v", got, err)
	}
}

func TestSuccessiveAttemptResumesLatestSameJobSession(t *testing.T) {
	target := deliverTarget{provider: "codex"}
	old := fleet.Rec{"status": "running", "provider": "codex", "job": fleet.Rec{"id": "older"}}
	for attempt := 1; attempt <= 3; attempt++ {
		stateFile := filepath.Join(t.TempDir(), "state.json")
		session := "session-" + string(rune('0'+attempt))
		target.job = &jobBinding{state: "/tmp/jobs"}
		last := fleet.Rec{"status": "running", "provider": "codex", "resume": session, "work_identity": workIdentity(target), "job": fleet.Rec{"id": "same", "state": "/tmp/jobs"}, "attempt": session, "state_file": stateFile, "previous_launch": old}
		if err := fleet.WriteJSON(stateFile, fleet.Rec{"attempt": session, "provider": "codex", "provider_session": session, "provider_terminal": true}); err != nil {
			t.Fatal(err)
		}
		job := &jobs.Job{ID: "same"}
		got, err := resumeSessionForJob(target, last, job)
		if err != nil || got != session {
			t.Fatalf("attempt %d resumed %q: %v", attempt, got, err)
		}
		old = last
	}
}

func TestRuntimeStatusSurfacesJobOutcomeWithoutToken(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	last := fleet.Rec{"at": fleet.Now(), "status": "failed", "address": target.address, "cwd": home, "provider": "codex", "attempt": "attempt", "state_file": stateFile, "job": fleet.Rec{"id": "job-1", "state": "/tmp/jobs", "worker": target.address, "token": "secret", "ttl_seconds": 300}}
	if err := fleet.WriteJSON(launchPath(target), last); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(stateFile, fleet.Rec{"attempt": "attempt", "provider": "codex", "job_status": "interrupted", "job_error": "lease lost"}); err != nil {
		t.Fatal(err)
	}
	row := runtimeRow(target, nil)
	if fleet.S(row, "job_status") != "interrupted" || fleet.S(row, "job_error") != "lease lost" {
		t.Fatalf("job outcome missing: %v", row)
	}
	if fleet.Has(fleet.M(row, "job"), "token") {
		t.Fatalf("status exposed token: %v", row)
	}
}

func TestGoneBridgeSurfacesPendingProviderCleanup(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	last := fleet.Rec{"status": "running", "provider": "codex", "attempt": "attempt", "state_file": stateFile, "exit_file": filepath.Join(t.TempDir(), "missing-exit.json"), "pid": 999999999}
	if err := fleet.WriteJSON(stateFile, fleet.Rec{"attempt": "attempt", "provider": "codex", "provider_started": true, "provider_terminal": false}); err != nil {
		t.Fatal(err)
	}
	state, _ := processState(last)
	if state != "gone_exit_unknown" {
		t.Fatalf("fixture state: %s", state)
	}
	row := fleet.Rec{"state": state}
	providerActivity(row, last)
	if !fleet.B(row, "provider_cleanup_pending") {
		t.Fatalf("missing cleanup warning: %v", row)
	}
}

func TestPrelaunchRecordsDoNotBecomeUnknownProcesses(t *testing.T) {
	if launchPresent(fleet.Rec{"status": "claiming", "provider": "codex"}) {
		t.Fatal("claiming record without a PID must permit claim replay")
	}
	for _, expired := range []bool{false, true} {
		state := t.TempDir()
		now := time.Now()
		if expired {
			now = now.Add(-time.Minute)
		}
		store := &jobs.Store{Dir: state, Now: func() time.Time { return now }}
		if _, err := store.Execute(jobs.Request{Op: "submit", ID: "job", Brief: "recover before launch"}); err != nil {
			t.Fatal(err)
		}
		result, err := store.Execute(jobs.Request{Op: "claim", ID: "job", Worker: "worker", Key: "key", TTLSeconds: 30})
		if err != nil {
			t.Fatal(err)
		}
		job := result.(jobs.Job)
		record := fleet.Rec{"status": "claimed", "provider": "codex", "job": fleet.Rec{"state": state, "id": "job", "token": job.Attempts[0].Token}}
		if got := launchPresent(record); got == expired {
			t.Fatalf("expired=%v reserved=%v", expired, got)
		}
	}
	if !launchPresent(fleet.Rec{"status": "running", "provider": "codex"}) {
		t.Fatal("uncertain launched provider must remain reserved")
	}
}
