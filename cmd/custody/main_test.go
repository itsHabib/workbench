package main

import "testing"

func TestIsHelp(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help"} {
		if !isHelp(arg) {
			t.Errorf("isHelp(%q) = false", arg)
		}
	}
	if isHelp("grant") {
		t.Error("isHelp(grant) = true")
	}
}

func TestRunGrantHelpSucceeds(t *testing.T) {
	if err := cmdGrant([]string{"-h"}); err != nil {
		t.Fatalf("cmdGrant -h: %v", err)
	}
}
