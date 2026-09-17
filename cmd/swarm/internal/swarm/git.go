package swarm

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Git runs one git command in dir and returns trimmed stdout.
func Git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// CurrentBranch is the branch checked out in dir, or "" when detached.
func CurrentBranch(dir string) string {
	b, err := Git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || b == "HEAD" {
		return ""
	}
	return b
}

func gitLines(dir string, args ...string) ([]string, error) {
	out, err := Git(dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
