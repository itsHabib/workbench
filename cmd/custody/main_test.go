package main

import "testing"

func TestRunGrantHelpSucceeds(t *testing.T) {
	if err := cmdGrant([]string{"-h"}); err != nil {
		t.Fatalf("cmdGrant -h: %v", err)
	}
}
