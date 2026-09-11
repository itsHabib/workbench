//go:build !darwin

package provider

import "fmt"

func ExecBarrier([]string) error { return fmt.Errorf("provider observer unavailable on this platform") }
func observeProcess([]string, *ProcessProof) (int, error) {
	return 1, fmt.Errorf("provider observer unavailable on this platform")
}
