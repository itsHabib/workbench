package main

import (
	"strings"
	"testing"
)

func TestRequireLoopback(t *testing.T) {
	ok := []string{"127.0.0.1:8127", "localhost:8127", "[::1]:8127"}
	for _, a := range ok {
		if err := requireLoopback(a); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"0.0.0.0:8127", "10.0.0.5:8127", "example.com:80", "8127"}
	for _, a := range bad {
		if err := requireLoopback(a); err == nil {
			t.Errorf("requireLoopback(%q) = nil, want refusal", a)
		}
	}
}

func TestKeysUsageAndBadSubcommand(t *testing.T) {
	if err := cmdKeys([]string{"-h"}); err != nil {
		t.Fatalf("keys -h: %v", err)
	}
	if err := cmdKeys([]string{"bogus"}); err == nil {
		t.Fatal("keys bogus: want error")
	}
	err := cmdKeys([]string{"set"})
	if err == nil || !strings.Contains(err.Error(), "-name") {
		t.Fatalf("keys set without -name: %v", err)
	}
}

func TestServeHelpSucceeds(t *testing.T) {
	if err := cmdServe([]string{"-h"}); err != nil {
		t.Fatalf("serve -h: %v", err)
	}
}
