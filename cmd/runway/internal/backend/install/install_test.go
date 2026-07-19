package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/contracts/execution"
)

func TestResolveInstalledPlacement(t *testing.T) {
	adapter, err := Resolve(execution.Placement{Backend: "local", Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter == nil {
		t.Fatal("local adapter is nil")
	}
	if _, err := Resolve(execution.Placement{Backend: "local", Profile: "other"}); err == nil {
		t.Fatal("unsupported local profile was accepted")
	}
	if _, err := Resolve(execution.Placement{Backend: "other", Profile: "default"}); err == nil {
		t.Fatal("unsupported backend was accepted")
	}
}

func TestReadReceiptSupportsLegacyAndAdapterRecords(t *testing.T) {
	dir := t.TempDir()
	placement := execution.Placement{Backend: "local", Profile: "default"}
	path := filepath.Join(dir, "backend.json")
	if err := os.WriteFile(path, []byte(`{"pid":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := ReadReceipt(placement, dir)
	if receipt.AllocationID != "pid:42" {
		t.Fatalf("legacy allocation=%q", receipt.AllocationID)
	}
	data := []byte(`{"receipt":{"backend":"rooms","profile":"agent-cursor","allocation_id":"room-a","stream_delivery":"terminal_replay"}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	receipt = ReadReceipt(execution.Placement{Backend: "rooms", Profile: "agent-cursor"}, dir)
	if receipt.AllocationID != "room-a" || receipt.StreamDelivery != execution.StreamDeliveryTerminalReplay {
		t.Fatalf("adapter receipt=%+v", receipt)
	}
}
