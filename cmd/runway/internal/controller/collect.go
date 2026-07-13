package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/contracts/execution"
)

// collectOutputs verifies declared outputs under the out root without
// rewriting bytes (Flow A step 10 / D7). Missing required outputs return a
// collection failure; optional missing outputs are skipped.
func collectOutputs(outRoot string, declared []execution.Output) ([]execution.Artifact, error) {
	var arts []execution.Artifact
	for _, o := range declared {
		art, err := collectOne(outRoot, o)
		if err != nil {
			if !o.Required {
				continue
			}
			return arts, err
		}
		arts = append(arts, art)
	}
	return arts, nil
}

func collectOne(outRoot string, o execution.Output) (execution.Artifact, error) {
	abs := filepath.Join(outRoot, filepath.FromSlash(o.Path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return execution.Artifact{}, fmt.Errorf("controller: required output %q missing: %w", o.Name, err)
	}
	sum := sha256.Sum256(data)
	return execution.Artifact{
		Name:   o.Name,
		Path:   filepath.ToSlash(filepath.Join("artifacts", o.Path)),
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(data)),
	}, nil
}
