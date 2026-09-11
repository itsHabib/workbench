//go:build !darwin

package provider

import "fmt"

// ExecBarrier refuses the private observer entry point on unsupported platforms.
func ExecBarrier([]string) error { return fmt.Errorf("provider observer unavailable on this platform") }
func observeProcess([]string, *ProcessProof) (int, error) {
	return 1, fmt.Errorf("provider observer unavailable on this platform")
}
