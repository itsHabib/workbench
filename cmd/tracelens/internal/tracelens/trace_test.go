package tracelens

import "testing"

func TestCallSig_AbsentArgsNormalizeToEmptyObject(t *testing.T) {
	noArgs := Step{Tool: "grep_search"}
	emptyArgs := Step{Tool: "grep_search", Args: map[string]any{}}
	if noArgs.callSig() != emptyArgs.callSig() {
		t.Fatalf("nil and empty args must share a signature: %q vs %q",
			noArgs.callSig(), emptyArgs.callSig())
	}
	if noArgs.callSig() != "grep_search{}" {
		t.Fatalf("signature should read tool{}, got %q", noArgs.callSig())
	}
}
