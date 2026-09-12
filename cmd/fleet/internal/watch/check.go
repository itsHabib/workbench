package watch

import (
	"fmt"

	"github.com/itsHabib/workbench/cmd/fleet/internal/provider"
)

// Check inspects one configured provider without ticking or reserving delivery.
func Check(address string) (map[string]any, error) {
	targets, configError := readDeliverTargets()
	if configError != "" {
		return nil, fmt.Errorf("delivery configuration: %s", configError)
	}
	for _, target := range targets {
		if target.address != address {
			continue
		}
		return checkTarget(target)
	}
	return nil, fmt.Errorf("no delivery target for %s", address)
}

func checkTarget(target deliverTarget) (map[string]any, error) {
	if target.configError != "" {
		return nil, fmt.Errorf("delivery configuration: %s", target.configError)
	}
	if err := bound(target); err != nil {
		return nil, err
	}
	if target.provider != "codex" {
		return nil, fmt.Errorf("setup inspection supports codex only; use inspect-hooks for a static harness inventory")
	}
	result, err := provider.Check(target.cwd)
	if err != nil {
		return nil, err
	}
	result["address"] = target.address
	return result, nil
}
