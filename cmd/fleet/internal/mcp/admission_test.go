package mcp

import (
	"strings"
	"testing"
)

func TestDispatchMCPRejectsEmptyOrMistypedAdmissionFields(t *testing.T) {
	for _, key := range []string{"requires", "head"} {
		t.Run(key, func(t *testing.T) {
			for _, value := range []any{"", " ", nil, 17, []string{"implementation"}} {
				text, isErr := dispatch("fleet_dispatch", map[string]any{key: value})
				if !isErr || !strings.Contains(text, key+" needs a non-empty string") {
					t.Fatalf("%s=%v silently dropped the demand: %s (error=%v)", key, value, text, isErr)
				}
			}
		})
	}
}
