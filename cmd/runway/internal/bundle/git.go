package bundle

import (
	"context"
	"os/exec"
	"time"
)

// gitCommand builds a git invocation whose context cancellation kills the
// whole process TREE, not just git itself: git spawns helper children that
// inherit handles under the clone temp dir, and on Windows a surviving
// helper blocks the deferred RemoveAll and violates the zero-orphan bar.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	setTreeKill(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	// Bound Wait after a kill so an unkillable straggler cannot hang
	// preparation's goroutine join forever.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
