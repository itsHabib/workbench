package driverstate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// This file is the shared client-side identity mechanism: canonical state-root
// resolution and the client-minted id formats. It lives beside the ledger
// mechanism (not in either tool) precisely because BOTH client surfaces — the
// MCP server and the CLI — must resolve the same root and mint the same id
// shapes. Two surfaces resolving different roots is the ship/MSIX failure mode
// the plane exists to kill (spec §6 P2), so the resolver is one function, not a
// copy per tool.

// StateDirEnv is the environment variable that pins the state root explicitly.
// When set, it wins over the user-profile fallback.
const StateDirEnv = "WORKBENCH_STATE_DIR"

// stateDirLeaf is the fixed subtree of the user profile the ledger lives under
// when no explicit root is given.
var stateDirLeaf = filepath.Join(".workbench", "driver-state")

// StateRoot resolves the canonical driver-state directory once, the same way
// for every client surface: an explicit WORKBENCH_STATE_DIR wins; otherwise the
// real user profile's .workbench/driver-state. It returns the resolved path and
// a short description of which source won, so a server can PRINT the root at
// startup (spec §6 P2 — canonical, not ambient). It resolves the path but does
// not create it; Claim mkdirs on first write.
//
// The env value is resolved to an ABSOLUTE path (filepath.Abs, which also
// cleans): a relative WORKBENCH_STATE_DIR would otherwise resolve against each
// process's working directory, so a CLI in a subdir and an MCP server at the
// repo root would split roots — the exact cross-client divergence this resolver
// exists to prevent. The user-profile fallback is already absolute.
func StateRoot() (dir, source string, err error) {
	if v := os.Getenv(StateDirEnv); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", "", fmt.Errorf("driverstate: resolve state root %q: %w", v, err)
		}
		return abs, "env " + StateDirEnv, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("driverstate: resolve state root: %w", err)
	}
	return filepath.Join(home, stateDirLeaf), "user profile", nil
}

// NewEventID mints a client event id (evt_<hex>) — the idempotency key Append
// dedupes on. A writer that supplies its own id keeps it; this is the fallback
// when none is given.
func NewEventID() (string, error) { return mintID("evt_") }

// NewRunID mints a driver-state run id (dsr_<hex>) — used when driver_record
// opens a run_imported with no run named.
func NewRunID() (string, error) { return mintID("dsr_") }

func mintID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("driverstate: mint id: %w", err)
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
