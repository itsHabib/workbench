// Package install is the private registry of Runway placement adapters. It
// keeps backend/profile names and durable cleanup dispatch out of controller
// policy and the provider-neutral contracts.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	roomsbackend "github.com/itsHabib/workbench/cmd/runway/internal/backend/rooms"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Resolve returns the installed adapter for one placed request.
func Resolve(p execution.Placement) (backend.Backend, error) {
	if p.Backend == "local" {
		if p.Profile != "default" {
			return nil, fmt.Errorf("controller: placement.profile %q is not installed for local", p.Profile)
		}
		return local.New(), nil
	}
	if p.Backend == "rooms" {
		if p.Profile != "agent-cursor" {
			return nil, fmt.Errorf("controller: placement.profile %q is not installed for rooms", p.Profile)
		}
		return roomsbackend.NewFromEnvironment()
	}
	return nil, fmt.Errorf("controller: placement.backend %q is not installed", p.Backend)
}

// CleanupDurable dispatches reconcile cleanup through the adapter that owns
// the request's placement. The controller sees only the shared outcome.
func CleanupDurable(p execution.Placement, privateDir string) (backend.CleanupResult, error) {
	if p.Backend == "rooms" {
		return roomsbackend.CleanupDurable(privateDir)
	}
	if p.Backend == "local" {
		return local.CleanupDurable(privateDir)
	}
	return backend.CleanupResult{Uncertain: true, AllocationID: "unknown"}, nil
}

// AssembleAuthorityReceiptDurable assembles the reconcile-time room-authority
// receipt through the adapter that owns the placement, reading the derive
// records persisted at resolve time (§7 F). ok is false when the placement has
// no authority receipt (non-rooms, or a run that carried no custody refs).
func AssembleAuthorityReceiptDurable(p execution.Placement, privateDir, artifactsDir, runID, allocationID string, artifacts []execution.Artifact, at time.Time) (execution.Artifact, bool, error) {
	if p.Backend == "rooms" {
		return roomsbackend.AssembleReconcileReceipt(privateDir, artifactsDir, runID, allocationID, artifacts, at)
	}
	return execution.Artifact{}, false, nil
}

// ReadReceipt recovers the adapter-authored placement receipt from
// private/backend.json. Legacy local records without a receipt remain readable.
func ReadReceipt(p execution.Placement, privateDir string) execution.PlacementReceipt {
	receipt := execution.PlacementReceipt{
		Backend:        p.Backend,
		Profile:        p.Profile,
		AllocationID:   "none",
		StreamDelivery: execution.StreamDeliveryNone,
	}
	data, err := os.ReadFile(filepath.Join(privateDir, "backend.json"))
	if err != nil {
		return receipt
	}
	var durable struct {
		PID     int                        `json:"pid"`
		Receipt execution.PlacementReceipt `json:"receipt"`
	}
	if json.Unmarshal(data, &durable) != nil {
		return receipt
	}
	if durable.Receipt.Backend != "" {
		return durable.Receipt
	}
	if durable.PID > 0 {
		receipt.AllocationID = fmt.Sprintf("pid:%d", durable.PID)
	}
	return receipt
}
