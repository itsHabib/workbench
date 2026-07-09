package main

import (
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
)

func TestSweepFailsWhenASourceCannotBeRead(t *testing.T) {
	// The CLI contract: `flare sweep` exits non-zero on a config/source error.
	// A source whose file is missing must not read as a clean sweep (exit 0) —
	// a broken source that looks swept is the silent failure this guards.
	dir := t.TempDir()
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:     config.Version,
		PollSeconds: 60,
		Sources:     []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: filepath.Join(dir, "missing.jsonl")}},
		Channels:    map[string]config.Channel{"toast": {Type: config.ChannelToast}},
		CatchAll:    "toast",
	}
	if code := sweep(cfg, j); code == 0 {
		t.Fatal("sweep must exit non-zero when a configured source cannot be read")
	}
}
