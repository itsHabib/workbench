package watch

import (
	"fmt"

	"github.com/itsHabib/workbench/cmd/fleet/internal/provider"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
)

// Check inspects one configured provider without ticking or reserving delivery.
func Check(address string) (map[string]any, error) {
	targets, configError := readDeliverTargets()
	for _, target := range targets {
		if target.address != address {
			continue
		}
		result, err := checkTarget(target)
		if err == nil && configError != "" {
			result["configuration_warning"] = configError
		}
		return result, err
	}
	if configError != "" {
		return nil, &verbs.Refusal{Code: 1, Msg: "delivery configuration: " + configError}
	}
	return nil, &verbs.Refusal{Code: 1, Msg: fmt.Sprintf("no delivery target for %q", address)}
}

func checkTarget(target deliverTarget) (map[string]any, error) {
	if target.configError != "" {
		return nil, &verbs.Refusal{Code: 1, Msg: "delivery configuration: " + target.configError}
	}
	if err := bound(target); err != nil {
		return nil, &verbs.Refusal{Code: 1, Msg: err.Error()}
	}
	if target.provider != "codex" {
		return nil, &verbs.Refusal{Code: 1, Msg: "setup inspection supports codex only; use inspect-hooks for a static harness inventory"}
	}
	result, err := provider.Check(target.cwd)
	if err != nil {
		return nil, err
	}
	result["address"] = target.address
	return result, nil
}
