package watch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestPreparationFailureReturnsMailAndPreservesPreviousLaunch(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	previous := fleet.Rec{"at": fleet.Now() - 120, "status": "failed", "provider": "codex", "resume": "previous-session", "work_identity": workIdentity(target)}
	if err := fleet.WriteJSON(launchPath(target), previous); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(launchPath(target))
	if err != nil {
		t.Fatal(err)
	}
	working := providerCommand
	providerCommand = func(string, map[string]any) (*exec.Cmd, error) { return nil, os.ErrPermission }
	putStoreMail(t, "hub:lead", "preparation", fleet.Now()-60, nil)
	observed := deliver(fleet.Now())
	if len(observedWhat(observed, "mail-delivery-failed")) != 1 {
		t.Fatalf("failure not reported: %v", observed)
	}
	after, err := os.ReadFile(launchPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("preparation replaced prior launch: %s", after)
	}
	mail := fleet.ReadJSON(filepath.Join(storeDirs(t, "t1", "hub:lead")[0], "preparation.json"))
	if fleet.Has(mail, "delivered_at") || fleet.Has(mail, "delivered_by") {
		t.Fatalf("mail retained reservation: %v", mail)
	}
	providerCommand = working
	if got := len(observedWhat(deliver(fleet.Now()), "mail-delivery-started")); got != 1 {
		t.Fatalf("retry starts: %d", got)
	}
	launched(t, sink)
	if got := len(observedWhat(deliver(fleet.Now()), "mail-delivery-started")); got != 0 {
		t.Fatalf("duplicate starts: %d", got)
	}
}

func TestLaunchPublicationFailureDoesNotStartPreparedCommand(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	working := providerCommand
	providerCommand = func(path string, request map[string]any) (*exec.Cmd, error) {
		cmd, err := working(path, request)
		if err != nil {
			return nil, err
		}
		// A directory at the destination forces atomic publication to fail on every OS.
		if err := os.Mkdir(launchPath(target), 0700); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	if pid, err := run(target, "probe", "", fleet.Now()); err == nil || pid != 0 {
		t.Fatalf("run: pid %d, error %v", pid, err)
	}
	if _, err := os.Stat(sink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared process ran: %v", err)
	}
	if err := os.Remove(launchPath(target)); err != nil {
		t.Fatal(err)
	}
	providerCommand = working
	if pid, err := run(target, "retry", "", fleet.Now()); err != nil || pid == 0 {
		t.Fatalf("retry: pid %d, error %v", pid, err)
	}
	launched(t, sink)
}

func TestAmbiguousStartingStillBlocksDelivery(t *testing.T) {
	home, sink := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	if err := fleet.WriteJSON(launchPath(target), fleet.Rec{"at": fleet.Now() - 3600, "status": "starting"}); err != nil {
		t.Fatal(err)
	}
	putStoreMail(t, "hub:lead", "ambiguous", fleet.Now()-60, nil)
	if _, err := readLaunch(target); err == nil {
		t.Fatal("ambiguous start accepted")
	}
	if got := len(observedWhat(deliver(fleet.Now()), "mail-delivery-started")); got != 0 {
		t.Fatalf("ambiguous retry starts: %d", got)
	}
	if _, err := os.Stat(sink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ambiguous process ran: %v", err)
	}
}
