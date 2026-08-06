package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// ErrNotParked is returned by a GrantFinder when the escalation id names no
// currently-parked run — already resolved, superseded by a re-park, or never
// parked at all. It is the expected outcome of a replayed or stale Slack tap:
// once a park resolves it leaves the inbox, so the SECOND tap finds no grant to
// run under. The handler maps it to 409 so a double-tap gets a clean "already
// handled" instead of a server error — the first line of the replay defense,
// ahead of gate's own escalationIsOpen guard.
var ErrNotParked = errors.New("serve: escalation is not currently parked")

// GateGrantFinder is the production GrantFinder: it shells `gate next -json` —
// gate's read-only console feed — and joins the parked escalation id to the
// grant its run parked under. It reads gate's OUTPUT and never imports gate, the
// same composition the console and the resolve back-channel already use. The
// non-live projection is read (no GitHub reconcile): the grant lives in the log,
// so a network round-trip would add nothing but latency and a failure mode.
func GateGrantFinder(gateBin, state string) GrantFinder {
	return func(ctx context.Context, escID string) (string, error) {
		args := []string{"next", "-json"}
		if state != "" {
			args = append(args, "-state", state)
		}
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, gateBin, args...)
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("serve: run gate next: %w", err)
		}
		return grantForEscalation(out.Bytes(), escID)
	}
}

// inboxFeed is the slice of `gate next -json` serve reads: the parked runs, each
// carrying its escalation id and the grant it parked under. A deliberate small
// copy of gate's projection shape rather than an import — serve joins one
// escalation to one grant and needs nothing else from the feed.
type inboxFeed struct {
	Parked []struct {
		Escalation string `json:"escalation"`
		Grant      string `json:"grant"`
	} `json:"parked"`
}

// grantForEscalation finds the grant of the parked run whose escalation id
// matches, or errors if the id names no currently-parked escalation. That miss
// is the correct failure: a resolution can only run against a live park, so an
// id absent from the inbox — already resolved, superseded, or never parked — has
// no grant to run under. A park present but grantless (schema-valid since
// escalation.v1's second consumer) errors by name rather than falling through
// to ingest's generic "grant is required" — the operator should read "this park
// resolves out-of-band", not "you forgot a flag". It is pure policy, tested
// without a gate binary.
func grantForEscalation(feed []byte, escID string) (string, error) {
	var in inboxFeed
	if err := json.Unmarshal(feed, &in); err != nil {
		return "", fmt.Errorf("serve: decode gate next feed: %w", err)
	}
	for _, p := range in.Parked {
		if p.Escalation != escID {
			continue
		}
		if p.Grant == "" {
			return "", fmt.Errorf("%w: %s has no grant — a grantless park resolves out-of-band, not through this back-channel", ErrNotParked, escID)
		}
		return p.Grant, nil
	}
	return "", fmt.Errorf("%w: %s not in gate inbox", ErrNotParked, escID)
}
