//go:build !linux && !windows && !darwin

package watch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func processIdentity(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	identity := strings.TrimSpace(string(b))
	if identity == "" {
		return "", fmt.Errorf("missing process start time")
	}
	return identity, nil
}
