package provider

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "app-server" && os.Getenv("FLEET_TEST_CODEX_SCRIPT") != "" {
		node := os.Getenv("FLEET_TEST_NODE")
		_ = syscall.Exec(node, []string{node, os.Getenv("FLEET_TEST_CODEX_SCRIPT")}, os.Environ())
		os.Exit(127)
	}
	if len(os.Args) > 1 && os.Args[1] == "_provider-exec" {
		if ExecBarrier(os.Args[2:]) != nil {
			os.Exit(127)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "_provider-process" {
		code, _ := ObserveCommand(os.Args[2:])
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestOwnedNoForkLifetimeAndEscapedDescendant(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	marker := filepath.Join(t.TempDir(), "survived")
	script := `require('child_process').spawn(process.execPath,['-e',"setTimeout(()=>require('fs').writeFileSync(process.argv[1],'alive'),1000)",process.argv[1]],{detached:true,stdio:'ignore'}).unref()`
	for _, tc := range []struct {
		name, script string
		quiescent    bool
	}{{"no fork", "", true}, {"escaped child", script, false}} {
		t.Run(tc.name, func(t *testing.T) {
			proofFile := filepath.Join(t.TempDir(), "proof.json")
			code, err := ObserveCommand([]string{"attempt", proofFile, node, "-e", tc.script, marker})
			if err != nil || code != 0 {
				t.Fatalf("run: %d %v", code, err)
			}
			raw, err := os.ReadFile(proofFile)
			if err != nil {
				t.Fatal(err)
			}
			var p ProcessProof
			if err = json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if !p.Armed || !p.ExecObserved || !p.ExitObserved || p.Quiescent != tc.quiescent || p.ForkObserved == tc.quiescent {
				t.Fatalf("proof: %+v", p)
			}
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("escaped child did not survive the observed parent")
}

func TestFailedObservationCannotProveQuiescence(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	original := armProcess
	armProcess = func(int) (int, error) { return -1, errors.New("observer unavailable") }
	defer func() { armProcess = original }()
	proof := ProcessProof{}
	_, err = observeProcess([]string{node, "-e", ""}, &proof)
	if err == nil || proof.Armed || proof.Quiescent || proof.NeverStarted {
		t.Fatalf("unknown observation released process: %+v %v", proof, err)
	}
}
