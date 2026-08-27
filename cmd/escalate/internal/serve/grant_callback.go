package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// GateGrantCallback builds the production callback seam. It passes the exact
// raw body on stdin and the two original signature headers as argv to Gate,
// which independently authenticates, authorizes, validates scope, and writes.
func GateGrantCallback(bin, stateDir string) GrantCallback {
	return func(ctx context.Context, body []byte, signature, timestamp string) ([]byte, int, error) {
		args := []string{"grant-callback"}
		if stateDir != "" {
			args = append(args, "-state", stateDir)
		}
		// Key custody follows the same convention as gate resolve: Escalate never
		// accepts or forwards a -key path. Gate resolves GATE_KEY in the inherited
		// environment and refuses if the canonical signing material is unavailable.
		args = append(args, "-signature", signature, "-timestamp", timestamp)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdin = bytes.NewReader(body)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err == nil {
			return stdout.Bytes(), codeMerge, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), exitErr.ExitCode(), nil
		}
		return stdout.Bytes(), codeError, fmt.Errorf("serve: run gate grant-callback: %w", err)
	}
}
