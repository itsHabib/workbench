package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGateGrantCallbackPreservesDeadlineError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test callback fixture is a POSIX script")
	}
	bin := callbackFixture(t, "sleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, code, err := GateGrantCallback(bin, "")(ctx, nil, "signature", "timestamp")
	if code != codeError || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestGateGrantCallbackRejectsExitOutsideGateContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test callback fixture is a POSIX script")
	}
	bin := callbackFixture(t, "exit 9")
	_, code, err := GateGrantCallback(bin, "")(context.Background(), nil, "signature", "timestamp")
	if code != codeError || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func callbackFixture(t *testing.T, command string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+command+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
