package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestMailWatchProcess(_ *testing.T) {
	switch os.Getenv("FLEET_TEST_MAIL_WATCH") {
	case "once":
		runWatch([]string{"--once"})
	case "serve":
		runWatch([]string{"--interval", "1m"})
	}
}

func TestMailWatchFailureExitAndWriteBoundary(t *testing.T) {
	for _, mode := range []string{"once", "serve"} {
		t.Run(mode, func(t *testing.T) { testMailWatchFailure(t, mode) })
	}
}

func testMailWatchFailure(t *testing.T, mode string) {
	t.Helper()
	root := t.TempDir()
	state, org, cwd := filepath.Join(root, "state"), filepath.Join(root, "org"), filepath.Join(root, "role")
	for _, dir := range []string{state, org, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(org, "roles.map"), []byte(fmt.Sprintf("%s t hub:a\n", cwd)), 0600)
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = state, org
	_, err := fleet.PutMail(fleet.Rec{"id": "m1", "to": "hub:a", "from_role": "hub:b", "from_session": "s", "kind": "report", "subject": "test", "body": "test"})
	if err == nil {
		err = fleet.WriteJSON(fleet.Path("deliver.json"), fleet.Rec{"hub:a": fleet.Rec{"cwd": cwd, "cmd": []string{filepath.Join(root, "missing-command")}}})
	}
	fleet.State, fleet.OrgState = oldState, oldOrg
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_STATE", state)
	t.Setenv("ORG_STATE", org)
	t.Setenv("FLEET_GITHUB", "off")
	t.Setenv("FLEET_MAIL_GRACE", "0s")
	t.Setenv("FLEET_TEST_MAIL_WATCH", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMailWatchProcess$")
	if mode == "once" {
		out, err := cmd.CombinedOutput()
		if err == nil || cmd.ProcessState.ExitCode() != 1 || (!strings.Contains(string(out), "missing-command") || !strings.Contains(string(out), "# fleet board")) {
			t.Fatalf("exit=%v output=%s", err, out)
		}
		return
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(filepath.Join(state, "watch", "observed.jsonl"))
		if strings.Contains(string(log), "watch-error") {
			if _, err := os.Stat(filepath.Join(state, "hook-errors.jsonl")); !os.IsNotExist(err) {
				t.Fatal("watch wrote outside watch/", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watch failure not recorded")
}
